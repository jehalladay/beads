package metrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMain intercepts the send-metrics re-exec that MaybeSpawnFlusher performs.
// MaybeSpawnFlusher spawns `<self> send-metrics`, and <self> is this test
// binary; without this guard the child would re-run the whole test suite (and
// each run would fork again — a fork bomb). Exiting immediately makes the
// detached child a harmless no-op so TestMaybeSpawnFlusherSpawnsChild can
// exercise the real cmd.Start()/Release() path hermetically.
func TestMain(m *testing.M) {
	if len(os.Args) >= 2 && os.Args[1] == SendMetricsSubcommand {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestEndpointReflectsInit covers Endpoint(): after Init resolves the endpoint
// (falling back to DefaultEndpoint on an empty argument), the accessor must
// return exactly what Init stored. The detached flusher relies on this value
// being the sanctioned one, so it is worth pinning directly.
func TestEndpointReflectsInit(t *testing.T) {
	tests := []struct {
		name     string
		argEndpt string
		want     string
	}{
		{name: "empty falls back to default", argEndpt: "", want: DefaultEndpoint},
		{name: "explicit endpoint preserved", argEndpt: "https://custom.example/collect", want: "https://custom.example/collect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			closeFn, err := Init("0.0.0-test", false, tt.argEndpt)
			if err != nil {
				t.Fatalf("Init: %v", err)
			}
			defer closeFn(context.Background())
			if got := Endpoint(); got != tt.want {
				t.Errorf("Endpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRunSendMetricsEnabledEmptyDirIsHermetic covers the enabled branch of
// RunSendMetrics through the flusher construction. With no queued .evtq files
// the flusher has nothing to upload, so it returns 0 without touching the
// network — exercising DataDir, ga4 transport construction, and the Flush call
// on an empty queue.
func TestRunSendMetricsEnabledEmptyDirIsHermetic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := Init("0.0.0-test", true, ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if code := RunSendMetrics(); code != 0 {
		t.Errorf("RunSendMetrics() = %d, want 0 for an empty queue", code)
	}
}

// TestFlushDisabledByEnv is the table-driven truth table for the env gate that
// keeps the detached flusher from spawning. Only unset and the explicit
// false/0 values leave flushing enabled; every other value disables it.
func TestFlushDisabledByEnv(t *testing.T) {
	tests := []struct {
		name    string
		set     bool
		val     string
		wantOff bool
	}{
		{name: "unset -> enabled", set: false, wantOff: false},
		{name: "empty -> enabled", set: true, val: "", wantOff: false},
		{name: "0 -> enabled", set: true, val: "0", wantOff: false},
		{name: "false -> enabled", set: true, val: "false", wantOff: false},
		{name: "FALSE mixed-case -> enabled", set: true, val: "False", wantOff: false},
		{name: "1 -> disabled", set: true, val: "1", wantOff: true},
		{name: "true -> disabled", set: true, val: "true", wantOff: true},
		{name: "yes -> disabled", set: true, val: "yes", wantOff: true},
		{name: "arbitrary -> disabled", set: true, val: "please", wantOff: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(EnvDisableEventFlush, tt.val)
			} else {
				os.Unsetenv(EnvDisableEventFlush)
			}
			if got := flushDisabledByEnv(); got != tt.wantOff {
				t.Errorf("flushDisabledByEnv() = %v, want %v", got, tt.wantOff)
			}
		})
	}
}

// TestMaybeSpawnFlusherEnabledButFlushDisabled covers the second early-return in
// MaybeSpawnFlusher: metrics are enabled (so the first guard passes) but
// BD_DISABLE_EVENT_FLUSH is set, so no child is spawned. Combined with the
// existing disabled and BD_IS_FLUSHER tests this reaches every guard branch
// without ever forking a real process.
func TestMaybeSpawnFlusherEnabledButFlushDisabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(EnvDisableEventFlush, "1")
	if _, err := Init("0.0.0-test", true, ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Enabled() is true; only flushDisabledByEnv() prevents the fork.
	MaybeSpawnFlusher()
}

// TestMaybeSpawnFlusherSpawnsChild covers the full spawn body of
// MaybeSpawnFlusher: with metrics enabled and no disabling env, it resolves the
// executable, builds the send-metrics command with the pinned child env, and
// starts+releases a detached process. TestMain turns the re-executed child into
// an immediate no-op, so the fork is harmless while every statement on the
// happy path is exercised. We block briefly on the process to avoid leaking a
// zombie into the rest of the suite.
func TestMaybeSpawnFlusherSpawnsChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Ensure no inherited disable/flusher markers leak in from the harness.
	os.Unsetenv(EnvDisableEventFlush)
	os.Unsetenv(EnvIsFlusher)
	if _, err := Init("0.0.0-test", true, ""); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Exercises the real os.Executable + exec.Command + Start + Release path.
	MaybeSpawnFlusher()
	// Reap any detached child the OS may reparent; give it a moment to exit so
	// the intercepted no-op does not linger.
	time.Sleep(50 * time.Millisecond)
}

// TestWriteUserConfigBootstrapExistRace covers the O_EXCL fs.ErrExist branch of
// writeUserConfigBootstrap: if the file appears between the ReadFile check and
// the exclusive create, the function must recover by delegating back to
// EnsureUserConfigDefaults rather than erroring. We simulate the race by
// pre-creating the file with a complete metrics block, then calling
// writeUserConfigBootstrap directly — it hits ErrExist, re-enters
// EnsureUserConfigDefaults, sees both leaves present, and returns nil without
// clobbering the file.
func TestWriteUserConfigBootstrapExistRace(t *testing.T) {
	home := setupUserConfigHome(t)
	path := userConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte("metrics:\n  disabled: true\n  endpoint: https://kept.example.com\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := writeUserConfigBootstrap(path); err != nil {
		t.Fatalf("writeUserConfigBootstrap on existing file: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("existing config clobbered by exist-race recovery.\nwant: %q\ngot:  %q", original, got)
	}
}

// TestWriteUserConfigBootstrapMkdirFails covers the mkdir error path of
// writeUserConfigBootstrap: when the parent directory cannot be created (here
// because an ancestor is a regular file), it must return a wrapped mkdir error.
func TestWriteUserConfigBootstrapMkdirFails(t *testing.T) {
	home := t.TempDir()
	blocker := filepath.Join(home, "blocker")
	if err := os.WriteFile(blocker, []byte("i am a file"), 0o600); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	// Parent dir is under a regular file -> MkdirAll fails.
	path := filepath.Join(blocker, "bd", "config.yaml")

	err := writeUserConfigBootstrap(path)
	if err == nil {
		t.Fatal("expected an error when the parent dir cannot be created, got nil")
	}
}
