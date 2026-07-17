package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestEnabled(t *testing.T) {
	tests := []struct {
		name       string
		metricsURL string
		stdout     string
		want       bool
	}{
		{name: "neither set", metricsURL: "", stdout: "", want: false},
		{name: "metrics url set", metricsURL: "http://localhost:8428/x", stdout: "", want: true},
		{name: "stdout true", metricsURL: "", stdout: "true", want: true},
		{name: "stdout non-true is off", metricsURL: "", stdout: "1", want: false},
		{name: "stdout yes is off", metricsURL: "", stdout: "yes", want: false},
		{name: "both set", metricsURL: "http://localhost:8428/x", stdout: "true", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BD_OTEL_METRICS_URL", tt.metricsURL)
			t.Setenv("BD_OTEL_STDOUT", tt.stdout)
			if got := Enabled(); got != tt.want {
				t.Fatalf("Enabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestInitDisabledInstallsNoop verifies the zero-overhead path: when telemetry
// is disabled, Init installs no-op providers, returns nil, and registers no
// shutdown functions.
func TestInitDisabledInstallsNoop(t *testing.T) {
	t.Setenv("BD_OTEL_METRICS_URL", "")
	t.Setenv("BD_OTEL_STDOUT", "")
	shutdownFns = nil

	if err := Init(context.Background(), "beads-test", "v0.0.0"); err != nil {
		t.Fatalf("Init() disabled returned error: %v", err)
	}
	if _, ok := otel.GetTracerProvider().(tracenoop.TracerProvider); !ok {
		t.Errorf("tracer provider = %T, want noop.TracerProvider", otel.GetTracerProvider())
	}
	if _, ok := otel.GetMeterProvider().(metricnoop.MeterProvider); !ok {
		t.Errorf("meter provider = %T, want noop.MeterProvider", otel.GetMeterProvider())
	}
	if len(shutdownFns) != 0 {
		t.Errorf("shutdownFns len = %d, want 0 on the disabled path", len(shutdownFns))
	}
}

// TestInitStdoutInstallsRealProviders verifies the enabled path via
// BD_OTEL_STDOUT (no network): Init returns nil, installs non-noop providers,
// and registers shutdown functions for both trace and metric providers.
func TestInitStdoutInstallsRealProviders(t *testing.T) {
	t.Setenv("BD_OTEL_METRICS_URL", "")
	t.Setenv("BD_OTEL_STDOUT", "true")
	shutdownFns = nil

	if err := Init(context.Background(), "beads-test", "v1.2.3"); err != nil {
		t.Fatalf("Init() stdout returned error: %v", err)
	}
	if _, ok := otel.GetTracerProvider().(tracenoop.TracerProvider); ok {
		t.Error("tracer provider is noop; want a real stdout trace provider")
	}
	if _, ok := otel.GetMeterProvider().(metricnoop.MeterProvider); ok {
		t.Error("meter provider is noop; want a real stdout metric provider")
	}
	// One shutdown fn for the trace provider + one for the metric provider.
	if len(shutdownFns) != 2 {
		t.Errorf("shutdownFns len = %d, want 2 (trace + metric)", len(shutdownFns))
	}

	// Shutdown must flush and reset the registry so a later Init starts clean.
	Shutdown(context.Background())
	if shutdownFns != nil {
		t.Errorf("shutdownFns = %v after Shutdown, want nil", shutdownFns)
	}
}

// TestShutdownEmptyIsSafe verifies Shutdown is a no-op when nothing is registered.
func TestShutdownEmptyIsSafe(t *testing.T) {
	shutdownFns = nil
	Shutdown(context.Background()) // must not panic
	if shutdownFns != nil {
		t.Errorf("shutdownFns = %v, want nil", shutdownFns)
	}
}

func TestTracerNameDefaulting(t *testing.T) {
	// Empty name must fall back to the instrumentation scope; both calls must
	// return a usable (non-nil) tracer without panicking.
	if got := Tracer(""); got == nil {
		t.Error("Tracer(\"\") returned nil")
	}
	if got := Tracer("custom/scope"); got == nil {
		t.Error("Tracer(\"custom/scope\") returned nil")
	}
}

func TestMeterNameDefaulting(t *testing.T) {
	if got := Meter(""); got == nil {
		t.Error("Meter(\"\") returned nil")
	}
	if got := Meter("custom/scope"); got == nil {
		t.Error("Meter(\"custom/scope\") returned nil")
	}
}
