package hooks

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/steveyegge/beads/internal/types"
)

func TestTruncateOutput(t *testing.T) {
	t.Run("short output unchanged", func(t *testing.T) {
		in := "small"
		if got := truncateOutput(in); got != in {
			t.Errorf("truncateOutput(%q) = %q, want unchanged", in, got)
		}
	})

	t.Run("exactly at limit unchanged", func(t *testing.T) {
		in := strings.Repeat("a", maxOutputBytes)
		if got := truncateOutput(in); got != in {
			t.Errorf("output of length %d should be unchanged", maxOutputBytes)
		}
	})

	t.Run("over limit is truncated with note", func(t *testing.T) {
		in := strings.Repeat("a", maxOutputBytes+50)
		got := truncateOutput(in)
		if !strings.HasSuffix(got, "... (truncated)") {
			t.Errorf("expected truncation note suffix, got tail %q", got[len(got)-20:])
		}
		if !strings.HasPrefix(got, strings.Repeat("a", maxOutputBytes)) {
			t.Error("expected the first maxOutputBytes to be preserved")
		}
		if len(got) != maxOutputBytes+len("... (truncated)") {
			t.Errorf("truncated length = %d, want %d", len(got), maxOutputBytes+len("... (truncated)"))
		}
	})
}

// addHookOutputEvents evaluates truncateOutput on each non-empty buffer before
// adding a span event. A noop tracer's span still evaluates the call arguments,
// so this exercises both the stdout and stderr branches (and the empty-buffer
// skips) without an OTel exporter.
func TestAddHookOutputEvents_Branches(t *testing.T) {
	_, span := otel.Tracer("test").Start(context.Background(), "test")
	defer span.End()

	t.Run("both empty is a no-op", func(t *testing.T) {
		addHookOutputEvents(span, &bytes.Buffer{}, &bytes.Buffer{})
	})

	t.Run("stdout only", func(t *testing.T) {
		var out, errb bytes.Buffer
		out.WriteString("hello stdout")
		addHookOutputEvents(span, &out, &errb)
	})

	t.Run("stderr only", func(t *testing.T) {
		var out, errb bytes.Buffer
		errb.WriteString("hello stderr")
		addHookOutputEvents(span, &out, &errb)
	})

	t.Run("both present and over-limit", func(t *testing.T) {
		var out, errb bytes.Buffer
		out.WriteString(strings.Repeat("o", maxOutputBytes+10))
		errb.WriteString(strings.Repeat("e", maxOutputBytes+10))
		addHookOutputEvents(span, &out, &errb)
	})
}

// Run returns immediately for an unknown event without spawning a goroutine.
func TestRun_UnknownEvent(t *testing.T) {
	runner := NewRunner(t.TempDir())
	runner.Run("bogus-event", &types.Issue{ID: "bd-x"})
}

// Run silently skips when no hook file exists.
func TestRun_NoHook(t *testing.T) {
	runner := NewRunner(t.TempDir())
	runner.Run(EventCreate, &types.Issue{ID: "bd-x"})
}

// Run silently skips a present-but-non-executable hook.
func TestRun_NotExecutable(t *testing.T) {
	tmpDir := t.TempDir()
	hookPath := filepath.Join(tmpDir, HookOnCreate)
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\ntrue\n"), 0o644); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	runner := NewRunner(tmpDir)
	runner.Run(EventCreate, &types.Issue{ID: "bd-x"})
}

// Run silently skips when the hook path is a directory.
func TestRun_Directory(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, HookOnCreate), 0o755); err != nil {
		t.Fatalf("mkdir hook: %v", err)
	}
	runner := NewRunner(tmpDir)
	runner.Run(EventCreate, &types.Issue{ID: "bd-x"})
}

// A real async Run whose hook emits both stdout and stderr drives runHook's
// success path through addHookOutputEvents. We observe completion via a
// side-effect file the hook writes, then poll for it.
func TestRun_Async_EmitsOutput(t *testing.T) {
	tmpDir := t.TempDir()
	hookPath := filepath.Join(tmpDir, HookOnClose)
	doneFile := filepath.Join(tmpDir, "done.txt")
	hookScript := "#!/bin/sh\n" +
		"echo stdout-line\n" +
		"echo stderr-line 1>&2\n" +
		"echo ok > " + doneFile + "\n"
	if err := os.WriteFile(hookPath, []byte(hookScript), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	runner := NewRunner(tmpDir)
	runner.Run(EventClose, &types.Issue{ID: "bd-async", Title: "T"})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(doneFile); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("async hook did not complete within deadline")
}
