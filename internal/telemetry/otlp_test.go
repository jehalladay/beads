package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/resource"
)

// These tests cover the OTLP metric export path (beads-dhia): the HTTP
// exporter constructor and the BD_OTEL_METRICS_URL branch of
// buildMetricProvider, plus buildTraceProvider directly. The OTLP HTTP
// exporter connects lazily (no dial at construction), so a localhost URL is
// hermetic — nothing is sent unless a reader flushes, which these tests never
// trigger.

func TestBuildOTLPMetricExporter(t *testing.T) {
	exp, err := buildOTLPMetricExporter(context.Background(), "http://127.0.0.1:8428/opentelemetry/api/v1/push")
	if err != nil {
		t.Fatalf("buildOTLPMetricExporter returned error: %v", err)
	}
	if exp == nil {
		t.Fatal("buildOTLPMetricExporter returned nil exporter")
	}
	// Shut the exporter down immediately so no background goroutine lingers;
	// this exercises the exporter's own Shutdown without ever exporting.
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Errorf("exporter Shutdown: %v", err)
	}
}

func TestBuildMetricProvider_OTLPURLBranch(t *testing.T) {
	// URL set, stdout off → the OTLP-exporter branch of buildMetricProvider.
	t.Setenv("BD_OTEL_STDOUT", "")
	t.Setenv("BD_OTEL_METRICS_URL", "http://127.0.0.1:8428/opentelemetry/api/v1/push")

	res, err := resource.New(context.Background())
	if err != nil {
		t.Fatalf("resource.New: %v", err)
	}

	mp, err := buildMetricProvider(context.Background(), res)
	if err != nil {
		t.Fatalf("buildMetricProvider (OTLP branch): %v", err)
	}
	if mp == nil {
		t.Fatal("buildMetricProvider returned nil provider")
	}
	// Shutdown flushes the periodic reader, which will fail to reach the
	// unreachable localhost endpoint; that's expected and not what we're
	// asserting — construction (the covered branch) already succeeded.
	_ = mp.Shutdown(context.Background())
}

func TestBuildMetricProvider_StdoutAndOTLPBothOn(t *testing.T) {
	// Both readers configured → covers both append branches in one provider.
	t.Setenv("BD_OTEL_STDOUT", "true")
	t.Setenv("BD_OTEL_METRICS_URL", "http://127.0.0.1:8428/opentelemetry/api/v1/push")

	res, err := resource.New(context.Background())
	if err != nil {
		t.Fatalf("resource.New: %v", err)
	}

	mp, err := buildMetricProvider(context.Background(), res)
	if err != nil {
		t.Fatalf("buildMetricProvider (both branches): %v", err)
	}
	if mp == nil {
		t.Fatal("buildMetricProvider returned nil provider")
	}
	// Flush hits the unreachable OTLP endpoint (expected); we only assert the
	// provider was constructed with both readers.
	_ = mp.Shutdown(context.Background())
}

func TestBuildMetricProvider_NoReaders(t *testing.T) {
	// Neither env var → provider with only the resource option (no readers).
	t.Setenv("BD_OTEL_STDOUT", "")
	t.Setenv("BD_OTEL_METRICS_URL", "")

	res, err := resource.New(context.Background())
	if err != nil {
		t.Fatalf("resource.New: %v", err)
	}

	mp, err := buildMetricProvider(context.Background(), res)
	if err != nil {
		t.Fatalf("buildMetricProvider (no readers): %v", err)
	}
	if mp == nil {
		t.Fatal("buildMetricProvider returned nil provider")
	}
	if err := mp.Shutdown(context.Background()); err != nil {
		t.Errorf("meter provider Shutdown: %v", err)
	}
}

func TestBuildTraceProvider(t *testing.T) {
	res, err := resource.New(context.Background())
	if err != nil {
		t.Fatalf("resource.New: %v", err)
	}

	tp, err := buildTraceProvider(context.Background(), res)
	if err != nil {
		t.Fatalf("buildTraceProvider: %v", err)
	}
	if tp == nil {
		t.Fatal("buildTraceProvider returned nil provider")
	}
	if err := tp.Shutdown(context.Background()); err != nil {
		t.Errorf("trace provider Shutdown: %v", err)
	}
}

// TestInit_MetricsURLPath drives Init through the metrics-URL activation path
// (Enabled() true via URL, stdout off): traces stay noop, a real metric
// provider is installed, and Shutdown resets the registry.
func TestInit_MetricsURLPath(t *testing.T) {
	t.Setenv("BD_OTEL_STDOUT", "")
	t.Setenv("BD_OTEL_METRICS_URL", "http://127.0.0.1:8428/opentelemetry/api/v1/push")
	shutdownFns = nil

	if err := Init(context.Background(), "beads-test", "v9.9.9"); err != nil {
		t.Fatalf("Init (metrics-url path): %v", err)
	}
	// Trace stays noop when BD_OTEL_STDOUT is not "true".
	if _, ok := otel.GetMeterProvider().(metricnoop.MeterProvider); ok {
		t.Error("meter provider is noop; want a real metric provider on the URL path")
	}
	// Only the metric provider registers a shutdown fn (no trace provider).
	if len(shutdownFns) != 1 {
		t.Errorf("shutdownFns len = %d, want 1 (metric only)", len(shutdownFns))
	}

	Shutdown(context.Background())
	if shutdownFns != nil {
		t.Errorf("shutdownFns = %v after Shutdown, want nil", shutdownFns)
	}
}
