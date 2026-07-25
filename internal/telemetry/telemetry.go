package telemetry

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/bridges/otellogrus"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/kaiko-ai/loki-mcp/internal/logging"
)

const instrumentationName = "github.com/kaiko-ai/loki-mcp"

// Config controls OpenTelemetry setup.
type Config struct {
	ServiceVersion string
	Stderr         bool
}

var (
	// shutdownMu guards shutdowns. It stays a Mutex rather than an atomic
	// because registration and drain are both trivial critical sections with
	// no I/O under the lock (Shutdown copies the slice out and invokes the
	// functions after unlocking), and the process has exactly one telemetry
	// stack, so contention is nonexistent.
	shutdownMu sync.Mutex
	shutdowns  []func(context.Context) error

	// currentInstruments holds the metric instruments recorded on the hot
	// path. Written only at init/shutdown (initInstruments builds the whole
	// struct and stores it atomically); read on every tool call via Load.
	currentInstruments atomic.Pointer[instruments]
)

// instruments bundles the four metric instruments so they can be swapped in
// as one consistent snapshot when the real meter provider is installed.
type instruments struct {
	toolRequests        metric.Int64Counter
	toolDuration        metric.Float64Histogram
	secretFindings      metric.Int64Counter
	secretLinesFiltered metric.Int64Counter
}

// Initialize configures OpenTelemetry providers and instruments.
func Initialize(ctx context.Context, cfg Config) error {
	initInstruments()

	if otelDisabled() {
		return nil
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("loki-mcp"),
		semconv.ServiceVersion(cfg.ServiceVersion),
	)

	var shutdownFns []func(context.Context) error
	var errs []error

	tracerProvider, err := newTracerProvider(ctx, res, cfg)
	if err != nil {
		errs = append(errs, err)
	}
	if tracerProvider != nil {
		otel.SetTracerProvider(tracerProvider)
		shutdownFns = append(shutdownFns, tracerProvider.Shutdown)
	}

	meterProvider, err := newMeterProvider(ctx, res, cfg)
	if err != nil {
		errs = append(errs, err)
	}
	if meterProvider != nil {
		otel.SetMeterProvider(meterProvider)
		shutdownFns = append(shutdownFns, meterProvider.Shutdown)
		initInstruments()
	}

	loggerProvider, err := newLoggerProvider(ctx, res, cfg)
	if err != nil {
		errs = append(errs, err)
	}
	if loggerProvider != nil {
		global.SetLoggerProvider(loggerProvider)
		shutdownFns = append(shutdownFns, loggerProvider.Shutdown)
	}

	shutdownMu.Lock()
	shutdowns = append(shutdowns, shutdownFns...)
	shutdownMu.Unlock()

	return errors.Join(errs...)
}

// newTracerProvider builds a TracerProvider from the configured span
// exporters: OTLP when the OTEL environment asks for it, plus stderr when
// cfg.Stderr is set. It returns (nil, nil) when no exporter is configured.
// Exporter construction errors are reported but do not suppress a provider
// built from the remaining exporters.
func newTracerProvider(ctx context.Context, res *resource.Resource, cfg Config) (*sdktrace.TracerProvider, error) {
	options := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	var errs []error

	if shouldUseOTLP("OTEL_TRACES_EXPORTER", "TRACES") {
		exp, err := autoexport.NewSpanExporter(ctx)
		if err != nil {
			errs = append(errs, err)
		} else if !autoexport.IsNoneSpanExporter(exp) {
			options = append(options, sdktrace.WithBatcher(exp))
		}
	}
	if cfg.Stderr {
		exp, err := stdouttrace.New(stdouttrace.WithWriter(os.Stderr))
		if err != nil {
			errs = append(errs, err)
		} else {
			options = append(options, sdktrace.WithBatcher(exp))
		}
	}

	if len(options) == 1 {
		return nil, errors.Join(errs...)
	}
	return sdktrace.NewTracerProvider(options...), errors.Join(errs...)
}

// newMeterProvider is the metric-reader analogue of newTracerProvider.
func newMeterProvider(ctx context.Context, res *resource.Resource, cfg Config) (*sdkmetric.MeterProvider, error) {
	options := []sdkmetric.Option{sdkmetric.WithResource(res)}
	var errs []error

	if shouldUseOTLP("OTEL_METRICS_EXPORTER", "METRICS") {
		reader, err := autoexport.NewMetricReader(ctx)
		if err != nil {
			errs = append(errs, err)
		} else if !autoexport.IsNoneMetricReader(reader) {
			options = append(options, sdkmetric.WithReader(reader))
		}
	}
	if cfg.Stderr {
		exp, err := stdoutmetric.New(stdoutmetric.WithWriter(os.Stderr))
		if err != nil {
			errs = append(errs, err)
		} else {
			options = append(options, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)))
		}
	}

	if len(options) == 1 {
		return nil, errors.Join(errs...)
	}
	return sdkmetric.NewMeterProvider(options...), errors.Join(errs...)
}

// newLoggerProvider is the log-exporter analogue of newTracerProvider.
func newLoggerProvider(ctx context.Context, res *resource.Resource, cfg Config) (*sdklog.LoggerProvider, error) {
	options := []sdklog.LoggerProviderOption{sdklog.WithResource(res)}
	var errs []error

	if shouldUseOTLP("OTEL_LOGS_EXPORTER", "LOGS") {
		exp, err := autoexport.NewLogExporter(ctx)
		if err != nil {
			errs = append(errs, err)
		} else if !autoexport.IsNoneLogExporter(exp) {
			options = append(options, sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
		}
	}
	if cfg.Stderr {
		exp, err := stdoutlog.New(stdoutlog.WithWriter(os.Stderr))
		if err != nil {
			errs = append(errs, err)
		} else {
			options = append(options, sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
		}
	}

	if len(options) == 1 {
		return nil, errors.Join(errs...)
	}
	return sdklog.NewLoggerProvider(options...), errors.Join(errs...)
}

// NewLogrusHook bridges logrus records into the configured OpenTelemetry logger.
func NewLogrusHook(version string) logrus.Hook {
	return otellogrus.NewHook(instrumentationName, otellogrus.WithVersion(version))
}

// Shutdown flushes and shuts down configured providers.
func Shutdown(ctx context.Context) error {
	shutdownMu.Lock()
	fns := shutdowns
	shutdowns = nil
	shutdownMu.Unlock()

	var errs []error
	for i := len(fns) - 1; i >= 0; i-- {
		if err := fns[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// StartSpan starts a span with the package tracer.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName).Start(ctx, name, trace.WithAttributes(attrs...))
}

// ToolHandlerMiddleware records spans and metrics for MCP tool calls.
func ToolHandlerMiddleware() mcpserver.ToolHandlerMiddleware {
	return func(next mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			toolName := request.Params.Name
			attrs := []attribute.KeyValue{attribute.String("mcp.tool.name", toolName)}
			ctx, span := StartSpan(ctx, "mcp.tool."+toolName, attrs...)
			start := time.Now()

			result, err := next(ctx, request)
			isError := err != nil || (result != nil && result.IsError)
			if err != nil {
				span.RecordError(err)
			}
			span.SetAttributes(attribute.Bool("error", isError))
			span.End()

			metricAttrs := metric.WithAttributes(
				attribute.String("mcp.tool.name", toolName),
				attribute.Bool("error", isError),
			)
			if inst := currentInstruments.Load(); inst != nil {
				if inst.toolRequests != nil {
					inst.toolRequests.Add(ctx, 1, metricAttrs)
				}
				if inst.toolDuration != nil {
					inst.toolDuration.Record(ctx, time.Since(start).Seconds(), metricAttrs)
				}
			}

			return result, err
		}
	}
}

// RecordSecretFiltering records secret detection and omission metrics.
func RecordSecretFiltering(ctx context.Context, findings, lines int) {
	inst := currentInstruments.Load()
	if inst == nil {
		return
	}
	if findings > 0 && inst.secretFindings != nil {
		inst.secretFindings.Add(ctx, int64(findings))
	}
	if lines > 0 && inst.secretLinesFiltered != nil {
		inst.secretLinesFiltered.Add(ctx, int64(lines))
	}
}

func initInstruments() {
	meter := otel.Meter(instrumentationName)
	var errs []error

	requests, err := meter.Int64Counter(
		"loki_mcp_tool_requests_total",
		metric.WithDescription("Total MCP tool requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		errs = append(errs, err)
	}
	duration, err := meter.Float64Histogram(
		"loki_mcp_tool_duration_seconds",
		metric.WithDescription("MCP tool request duration"),
		metric.WithUnit("s"),
	)
	if err != nil {
		errs = append(errs, err)
	}
	findings, err := meter.Int64Counter(
		"loki_mcp_secret_findings_total",
		metric.WithDescription("Total secret findings detected in returned data"),
		metric.WithUnit("{finding}"),
	)
	if err != nil {
		errs = append(errs, err)
	}
	lines, err := meter.Int64Counter(
		"loki_mcp_secret_lines_filtered_total",
		metric.WithDescription("Total log lines omitted due to detected secrets"),
		metric.WithUnit("{line}"),
	)
	if err != nil {
		errs = append(errs, err)
	}

	// Practically unreachable, but do not drop these silently. Callers must
	// not see a behaviour change, so log instead of returning the error.
	if err := errors.Join(errs...); err != nil {
		logging.Errorf("OpenTelemetry instrument creation error: %v", err)
	}

	currentInstruments.Store(&instruments{
		toolRequests:        requests,
		toolDuration:        duration,
		secretFindings:      findings,
		secretLinesFiltered: lines,
	})
}

func otelDisabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")))
	return v == "true" || v == "1" || v == "yes"
}

func shouldUseOTLP(exporterEnv, signal string) bool {
	if exporter := strings.TrimSpace(os.Getenv(exporterEnv)); exporter != "" {
		return true
	}

	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_" + signal + "_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_" + signal + "_PROTOCOL",
		"OTEL_EXPORTER_OTLP_HEADERS",
		"OTEL_EXPORTER_OTLP_" + signal + "_HEADERS",
	} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}
