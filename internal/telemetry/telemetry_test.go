package telemetry

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
)

// unsetenv removes key for the duration of the test, restoring its prior
// state afterwards. Unlike t.Setenv it can express "variable absent".
func unsetenv(t *testing.T, key string) {
	t.Helper()
	if v, ok := os.LookupEnv(key); ok {
		t.Cleanup(func() { _ = os.Setenv(key, v) })
	} else {
		t.Cleanup(func() { _ = os.Unsetenv(key) })
	}
	require.NoError(t, os.Unsetenv(key))
}

// otlpEnvKeys returns every environment variable shouldUseOTLP inspects for
// the given exporter env var and signal.
func otlpEnvKeys(exporterEnv, signal string) []string {
	return []string{
		exporterEnv,
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_" + signal + "_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_" + signal + "_PROTOCOL",
		"OTEL_EXPORTER_OTLP_HEADERS",
		"OTEL_EXPORTER_OTLP_" + signal + "_HEADERS",
	}
}

func TestOtelDisabled(t *testing.T) {
	tests := []struct {
		name  string
		value *string
		want  bool
	}{
		{"unset", nil, false},
		{"empty", ptr(""), false},
		{"true", ptr("true"), true},
		{"1", ptr("1"), true},
		{"yes", ptr("yes"), true},
		{"false", ptr("false"), false},
		{"TRUE upper", ptr("TRUE"), true},
		{"padded true", ptr("  true  "), true},
		{"0", ptr("0"), false},
		{"no", ptr("no"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value == nil {
				unsetenv(t, "OTEL_SDK_DISABLED")
			} else {
				t.Setenv("OTEL_SDK_DISABLED", *tt.value)
			}
			assert.Equal(t, tt.want, otelDisabled())
		})
	}
}

func ptr(s string) *string { return &s }

func TestShouldUseOTLP(t *testing.T) {
	const (
		exporterEnv = "OTEL_TRACES_EXPORTER"
		signal      = "TRACES"
	)

	t.Run("nothing set", func(t *testing.T) {
		for _, k := range otlpEnvKeys(exporterEnv, signal) {
			unsetenv(t, k)
		}
		assert.False(t, shouldUseOTLP(exporterEnv, signal))
	})

	// Each single variable, set on its own, enables OTLP.
	for _, key := range otlpEnvKeys(exporterEnv, signal) {
		t.Run("set "+key, func(t *testing.T) {
			for _, k := range otlpEnvKeys(exporterEnv, signal) {
				unsetenv(t, k)
			}
			t.Setenv(key, "x")
			assert.True(t, shouldUseOTLP(exporterEnv, signal))
		})
	}

	t.Run("exporter env is trimmed", func(t *testing.T) {
		for _, k := range otlpEnvKeys(exporterEnv, signal) {
			unsetenv(t, k)
		}
		t.Setenv(exporterEnv, "   ")
		assert.False(t, shouldUseOTLP(exporterEnv, signal))
	})

	t.Run("endpoint vars are not trimmed", func(t *testing.T) {
		for _, k := range otlpEnvKeys(exporterEnv, signal) {
			unsetenv(t, k)
		}
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", " ")
		assert.True(t, shouldUseOTLP(exporterEnv, signal))
	})
}

func TestShutdownEmptyRegistry(t *testing.T) {
	// Drain anything a previous test registered, then shutting down an empty
	// registry must be a no-op returning nil.
	require.NoError(t, Shutdown(context.Background()))
	require.NoError(t, Shutdown(context.Background()))
}

func TestShutdownReverseOrder(t *testing.T) {
	require.NoError(t, Shutdown(context.Background())) // drain leftovers

	var order []string
	errBoom := errors.New("boom")
	mk := func(name string, err error) func(context.Context) error {
		return func(context.Context) error {
			order = append(order, name)
			return err
		}
	}

	shutdownMu.Lock()
	shutdowns = append(shutdowns,
		mk("first", nil),
		mk("second", errBoom),
		mk("third", nil),
	)
	shutdownMu.Unlock()

	err := Shutdown(context.Background())
	require.ErrorIs(t, err, errBoom)
	assert.Equal(t, []string{"third", "second", "first"}, order)
}

func TestStartSpanNoopProvider(t *testing.T) {
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(noop.NewTracerProvider())
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	ctx, span := StartSpan(context.Background(), "test-span", attribute.String("k", "v"))
	require.NotNil(t, ctx)
	require.NotNil(t, span)
	assert.False(t, span.IsRecording())
	assert.False(t, span.SpanContext().IsValid())
	span.End() // must not panic
}

// findAttr returns the value of the first attribute with the given key.
func findAttr(attrs []attribute.KeyValue, key string) (attribute.Value, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestToolHandlerMiddlewareSpanError(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})

	errNext := errors.New("next failed")
	tests := []struct {
		name        string
		next        func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
		wantErr     bool
		wantAttr    bool
		wantErrEvts int
	}{
		{
			name: "nil error",
			next: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("ok"), nil
			},
			wantAttr: false,
		},
		{
			name: "go error",
			next: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, errNext
			},
			wantErr:     true,
			wantAttr:    true,
			wantErrEvts: 1,
		},
		{
			name: "result IsError",
			next: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultError("bad"), nil
			},
			wantAttr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mw := ToolHandlerMiddleware()
			req := mcp.CallToolRequest{}
			req.Params.Name = "loki_query"

			before := len(sr.Ended())
			result, err := mw(tt.next)(context.Background(), req)
			if tt.wantErr {
				require.ErrorIs(t, err, errNext)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
			}

			ended := sr.Ended()
			require.Len(t, ended, before+1)
			span := ended[len(ended)-1]
			assert.Equal(t, "mcp.tool.loki_query", span.Name())

			v, ok := findAttr(span.Attributes(), "mcp.tool.name")
			require.True(t, ok)
			assert.Equal(t, "loki_query", v.AsString())

			v, ok = findAttr(span.Attributes(), "error")
			require.True(t, ok)
			assert.Equal(t, tt.wantAttr, v.AsBool())

			errEvts := 0
			for _, e := range span.Events() {
				if e.Name == "exception" {
					errEvts++
				}
			}
			assert.Equal(t, tt.wantErrEvts, errEvts)
		})
	}
}

// findMetric locates a metric by name in collected output.
func findMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for i := range rm.ScopeMetrics {
		for j := range rm.ScopeMetrics[i].Metrics {
			if rm.ScopeMetrics[i].Metrics[j].Name == name {
				return &rm.ScopeMetrics[i].Metrics[j]
			}
		}
	}
	return nil
}

func TestToolHandlerMiddlewareAndSecretMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		_ = mp.Shutdown(context.Background())
		otel.SetMeterProvider(prev)
	})

	// Instruments are created against whatever the global meter provider is;
	// rebinding here is the same path Initialize takes after installing a
	// real provider.
	initInstruments()

	mw := ToolHandlerMiddleware()
	req := mcp.CallToolRequest{}
	req.Params.Name = "loki_query"
	_, err := mw(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})(context.Background(), req)
	require.NoError(t, err)

	RecordSecretFiltering(context.Background(), 2, 3)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	requests := findMetric(rm, "loki_mcp_tool_requests_total")
	require.NotNil(t, requests)
	assert.Equal(t, "Total MCP tool requests", requests.Description)
	assert.Equal(t, "{request}", requests.Unit)
	reqSum, ok := requests.Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, reqSum.DataPoints, 1)
	assert.Equal(t, int64(1), reqSum.DataPoints[0].Value)
	nameAttr, ok := reqSum.DataPoints[0].Attributes.Value("mcp.tool.name")
	require.True(t, ok)
	assert.Equal(t, "loki_query", nameAttr.AsString())
	errAttr, ok := reqSum.DataPoints[0].Attributes.Value("error")
	require.True(t, ok)
	assert.False(t, errAttr.AsBool())

	duration := findMetric(rm, "loki_mcp_tool_duration_seconds")
	require.NotNil(t, duration)
	assert.Equal(t, "MCP tool request duration", duration.Description)
	assert.Equal(t, "s", duration.Unit)
	durHist, ok := duration.Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, durHist.DataPoints, 1)
	assert.Equal(t, uint64(1), durHist.DataPoints[0].Count)

	findings := findMetric(rm, "loki_mcp_secret_findings_total")
	require.NotNil(t, findings)
	assert.Equal(t, "Total secret findings detected in returned data", findings.Description)
	assert.Equal(t, "{finding}", findings.Unit)
	findSum, ok := findings.Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, findSum.DataPoints, 1)
	assert.Equal(t, int64(2), findSum.DataPoints[0].Value)

	lines := findMetric(rm, "loki_mcp_secret_lines_filtered_total")
	require.NotNil(t, lines)
	assert.Equal(t, "Total log lines omitted due to detected secrets", lines.Description)
	assert.Equal(t, "{line}", lines.Unit)
	lineSum, ok := lines.Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, lineSum.DataPoints, 1)
	assert.Equal(t, int64(3), lineSum.DataPoints[0].Value)
}

func TestInitializeDisabledIsNoop(t *testing.T) {
	require.NoError(t, Shutdown(context.Background())) // drain leftovers
	t.Setenv("OTEL_SDK_DISABLED", "true")

	require.NoError(t, Initialize(context.Background(), Config{ServiceVersion: "test", Stderr: true}))

	shutdownMu.Lock()
	n := len(shutdowns)
	shutdownMu.Unlock()
	assert.Zero(t, n, "disabled SDK must not register any shutdown functions")
}

func TestInitializeStderr(t *testing.T) {
	require.NoError(t, Shutdown(context.Background())) // drain leftovers
	unsetenv(t, "OTEL_SDK_DISABLED")
	for _, signal := range []string{"TRACES", "METRICS", "LOGS"} {
		for _, k := range otlpEnvKeys("OTEL_"+signal+"_EXPORTER", signal) {
			unsetenv(t, k)
		}
	}

	prevTP := otel.GetTracerProvider()
	prevMP := otel.GetMeterProvider()
	t.Cleanup(func() {
		_ = Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetMeterProvider(prevMP)
	})

	require.NoError(t, Initialize(context.Background(), Config{ServiceVersion: "test", Stderr: true}))

	// One provider per signal (traces, metrics, logs) => three shutdown fns.
	shutdownMu.Lock()
	n := len(shutdowns)
	shutdownMu.Unlock()
	assert.Equal(t, 3, n)
}

func TestInitializeNoExportersNoProviders(t *testing.T) {
	require.NoError(t, Shutdown(context.Background())) // drain leftovers
	unsetenv(t, "OTEL_SDK_DISABLED")
	for _, signal := range []string{"TRACES", "METRICS", "LOGS"} {
		for _, k := range otlpEnvKeys("OTEL_"+signal+"_EXPORTER", signal) {
			unsetenv(t, k)
		}
	}

	require.NoError(t, Initialize(context.Background(), Config{ServiceVersion: "test"}))

	// No exporter configured anywhere => no provider installed, no shutdowns.
	shutdownMu.Lock()
	n := len(shutdowns)
	shutdownMu.Unlock()
	assert.Zero(t, n)
}
