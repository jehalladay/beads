package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace/noop"

	"github.com/steveyegge/beads/internal/types"
)

// writeHook creates an executable hook script named hookName in dir.
func writeHook(t *testing.T, dir, hookName, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	path := filepath.Join(dir, hookName)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
}

func TestTruncateOutput(t *testing.T) {
	short := "small output"
	if got := truncateOutput(short); got != short {
		t.Fatalf("truncateOutput(short) = %q, want unchanged", got)
	}

	long := strings.Repeat("x", maxOutputBytes+50)
	got := truncateOutput(long)
	if !strings.HasSuffix(got, "... (truncated)") {
		t.Fatalf("truncateOutput(long) missing truncation marker: %q", got[len(got)-20:])
	}
	if len(got) != maxOutputBytes+len("... (truncated)") {
		t.Fatalf("truncated length = %d, want %d", len(got), maxOutputBytes+len("... (truncated)"))
	}
	if !strings.HasPrefix(got, strings.Repeat("x", maxOutputBytes)) {
		t.Fatal("truncated output did not preserve the first maxOutputBytes bytes")
	}

	// Exactly maxOutputBytes is not truncated (boundary).
	exact := strings.Repeat("y", maxOutputBytes)
	if got := truncateOutput(exact); got != exact {
		t.Fatal("truncateOutput at exactly maxOutputBytes should be unchanged")
	}
}

func TestAddHookOutputEventsBothBuffers(t *testing.T) {
	// A noop span accepts AddEvent without panicking; this exercises both the
	// stdout and stderr non-empty branches (and truncation via a long buffer).
	span := noop.Span{}
	stdout := bytes.NewBufferString(strings.Repeat("o", maxOutputBytes+10))
	stderr := bytes.NewBufferString("an error line")
	addHookOutputEvents(span, stdout, stderr)
}

func TestAddHookOutputEventsEmptyBuffers(t *testing.T) {
	// Both empty → neither AddEvent branch runs; must not panic.
	span := noop.Span{}
	addHookOutputEvents(span, &bytes.Buffer{}, &bytes.Buffer{})
}

func TestRunInvalidEventReturnsEarly(t *testing.T) {
	r := NewRunner(t.TempDir())
	// Invalid event → eventToHook returns "" → early return, no panic.
	r.Run("bogus", &types.Issue{ID: "x"})
}

func TestRunSyncInvalidEventReturnsNil(t *testing.T) {
	r := NewRunner(t.TempDir())
	if err := r.RunSync("bogus", &types.Issue{ID: "x"}); err != nil {
		t.Fatalf("RunSync(invalid) = %v, want nil", err)
	}
}

func TestHookExistsInvalidEvent(t *testing.T) {
	r := NewRunner(t.TempDir())
	if r.HookExists("bogus") {
		t.Fatal("HookExists(invalid event) = true, want false")
	}
}

func TestRunSkipsMissingHook(t *testing.T) {
	// hooksDir has no on_update file → os.Stat error → skip silently.
	r := NewRunner(t.TempDir())
	r.Run(EventUpdate, &types.Issue{ID: "x"})
}

func TestRunSkipsDirectoryHook(t *testing.T) {
	dir := t.TempDir()
	// Create on_update as a directory → info.IsDir() → skip.
	if err := os.MkdirAll(filepath.Join(dir, HookOnUpdate), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	NewRunner(dir).Run(EventUpdate, &types.Issue{ID: "x"})
}

func TestRunSkipsNonExecutableHook(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Non-executable regular file → mode&0111==0 → skip.
	if err := os.WriteFile(filepath.Join(dir, HookOnUpdate), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	NewRunner(dir).Run(EventUpdate, &types.Issue{ID: "x"})
}

func TestRunExecutesExistingExecutableHook(t *testing.T) {
	// Covers Run's happy path: stat ok, executable, goroutine dispatch.
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	writeHook(t, dir, HookOnCreate, "#!/bin/sh\ntouch "+marker+"\n")

	r := NewRunner(dir)
	// Prove the async goroutine actually runs by polling for the marker via
	// a synchronous run first (deterministic) then the async Run for coverage.
	if err := r.RunSync(EventCreate, &types.Issue{ID: "x"}); err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
	// Async path for Run() coverage (fire-and-forget; no assertion).
	r.Run(EventCreate, &types.Issue{ID: "y"})
}
