// Package telemetry owns the process-wide OpenTelemetry stack: the trace,
// metric and log providers built from the standard OTEL_* environment (plus
// optional stderr console exporters), the four MCP metric instruments, the
// tool-handler middleware that spans and times every tool call, and the
// logrus→OTel log bridge.
//
// Initialize is called once at startup by cmd and Shutdown once on the way
// out; everything else here is safe to call before Initialize (it degrades to
// no-ops) and safe for concurrent use. The package imports only
// internal/logging: it must not reach up into handlers, server or cmd.
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

// Config is everything Initialize takes that does not come from the
// environment. Exporter selection, endpoints, protocols, headers and sampling
// are all read from the standard OTEL_* variables instead, so this struct
// stays two fields.
//
// The zero Config is valid: it installs OTLP providers if the OTEL_*
// environment asks for them and nothing otherwise.
type Config struct {
	// ServiceVersion becomes the service.version resource attribute on every
	// span, metric and log record. "" is allowed and yields an empty
	// attribute rather than an error.
	ServiceVersion string
	// Stderr adds console exporters for all three signals, writing to
	// os.Stderr. Never stdout: under the stdio transport stdout carries the
	// MCP protocol. It is additive — with OTLP also configured, both
	// exporters receive everything.
	Stderr bool
}

var (
	// shutdownMu guards shutdowns. It stays a Mutex rather than an atomic
	// because registration and drain are both trivial critical sections with
	// no I/O under the lock (Shutdown copies the slice out and invokes the
	// functions after unlocking), and the process has exactly one telemetry
	// stack, so contention is nonexistent.
	//nolint:forbidigo // guards exactly one field, the package-level shutdowns slice; it is the innermost and only lock in the process, so there is no lock ordering to observe (never held across a call into other code — see docs/adr/0002-telemetry-shutdown-registry-keeps-a-mutex.md).
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

// Initialize builds the metric instruments and, when the environment asks for
// it, installs OpenTelemetry providers as the global tracer, meter and logger
// providers, registering their Shutdown functions for a later Shutdown call.
//
// It is not required. StartSpan, ToolHandlerMiddleware and RecordSecretFiltering
// all work without it — they fall back to OpenTelemetry's global no-op
// providers — so nothing in this package needs a "did you call Initialize"
// guard.
//
// A provider is installed for a signal only if an exporter is configured for
// it, which means either cfg.Stderr, or an OTEL_*_EXPORTER variable for that
// signal, or one of the OTLP endpoint/protocol/headers variables (generic or
// per-signal). Signals are decided independently: traces to a collector with
// metrics off is a normal outcome. With none of those set, Initialize builds
// the instruments, installs nothing, registers no shutdown function and
// returns nil.
//
// OTEL_SDK_DISABLED set to true, 1 or yes short-circuits everything after the
// instruments: no provider is built even if cfg.Stderr is set, and nil is
// returned.
//
// Errors are collected, not fatal. A failing exporter is joined into the
// returned error while the providers that did build are still installed, so a
// non-nil return does not mean telemetry is off, and the caller may keep
// running. Instrument creation errors are logged rather than returned.
//
// Calling Initialize twice replaces the global providers and appends a second
// set of shutdown functions; it is meant to run once, at startup, before any
// goroutine records anything.
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

// NewLogrusHook returns a logrus hook that mirrors every record it sees into
// the OpenTelemetry logger provider that is global at the time the record is
// emitted, not at the time of this call. Registering it before Initialize is
// therefore fine, and is what cmd does.
//
// It never replaces stderr output: records still go to the logrus writer as
// well. Without a logger provider installed the hook resolves to a no-op, so
// registering it in a process with no OTLP configuration costs a lookup per
// record and nothing else.
//
// version is recorded as the instrumentation scope version. The hook is safe
// for concurrent use, fires synchronously on the logging goroutine, and cannot
// be removed once added.
func NewLogrusHook(version string) logrus.Hook {
	return otellogrus.NewHook(instrumentationName, otellogrus.WithVersion(version))
}

// Shutdown flushes and stops every provider Initialize installed, in reverse
// order of registration, and joins whatever errors they report.
//
// It drains the registry rather than setting a flag: the pending functions are
// taken out under the lock and the registry is left empty, so a second call is
// a no-op returning nil, and two concurrent calls cannot shut the same
// provider down twice. It is idempotent in that sense only — a later
// Initialize repopulates the registry, so Shutdown will do real work again.
//
// Shutdown never uninstalls the global providers. After it returns, StartSpan
// and the recording helpers still work; they just record into providers that
// have stopped exporting. This is deliberate — it keeps shutdown-path code
// from having to nil-check anything.
//
// ctx bounds the flush. The callers pass a 5s timeout: long enough for a
// healthy collector, short enough not to hang the CLI on an unreachable one.
// A deadline that expires comes back as an error and the process should still
// exit.
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

// StartSpan starts a span named name on this module's tracer, as a child of
// whatever span ctx carries, and returns the derived context alongside it.
//
// The caller owns the span and must End it; nothing here does. Use the
// returned context for the work being measured, or the child span will attach
// to the wrong parent.
//
// It works before Initialize and after Shutdown. With no tracer provider
// installed the global no-op provider returns a non-recording span, which is
// safe to use and to End, so callers never need to check for nil.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName).Start(ctx, name, trace.WithAttributes(attrs...))
}

// ToolHandlerMiddleware returns middleware that wraps every MCP tool call in a
// span named "mcp.tool.<tool>" and records two metrics against it:
// loki_mcp_tool_requests_total and loki_mcp_tool_duration_seconds, both
// labelled with the tool name and an error boolean.
//
// It is transparent. The wrapped handler's result and error are returned
// unchanged, the middleware neither retries nor swallows anything, and the
// handler receives the span-carrying context so anything it starts nests
// correctly.
//
// A call counts as an error if the handler returned a non-nil error *or* a
// result with IsError set; only the Go error is also recorded on the span with
// RecordError. The span is ended before the metrics are recorded, so the
// duration excludes the metric write itself.
//
// Safe before Initialize: with no instruments stored yet the metric writes are
// skipped and only the (no-op) span is produced. Safe for concurrent calls.
// The body is straight-line; the cognitive-complexity score comes entirely
// from the two closures the mcpserver.ToolHandlerMiddleware signature
// requires, which add a nesting penalty to every statement inside them.
// Splitting them out would move code without removing a single branch.
//
//nolint:gocognit // nesting penalty from the required two-closure signature, see above
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

// RecordSecretFiltering adds findings to loki_mcp_secret_findings_total and
// lines to loki_mcp_secret_lines_filtered_total. Each is skipped unless
// positive, so a call with two zeros records nothing and is not an error.
//
// The two counters carry no attributes — not the tool name, not the query — on
// purpose: this is the one metric path that sees secret-bearing data, and
// nothing derived from a matched line is allowed onto it. Correlation is via
// the ambient span in ctx, which is otherwise unused here.
//
// Safe before Initialize and after Shutdown: with no instruments stored the
// call returns immediately. Safe for concurrent calls.
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
