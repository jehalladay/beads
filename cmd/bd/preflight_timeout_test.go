package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreflightCheckTimeout_Default(t *testing.T) {
	t.Setenv("BD_PREFLIGHT_TIMEOUT", "")
	if got := preflightCheckTimeout(); got != defaultPreflightTimeout {
		t.Errorf("expected default %s, got %s", defaultPreflightTimeout, got)
	}
}

func TestPreflightCheckTimeout_EnvOverride(t *testing.T) {
	t.Setenv("BD_PREFLIGHT_TIMEOUT", "42s")
	if got := preflightCheckTimeout(); got != 42*time.Second {
		t.Errorf("expected 42s, got %s", got)
	}
}

func TestPreflightCheckTimeout_InvalidEnvFallsBack(t *testing.T) {
	t.Setenv("BD_PREFLIGHT_TIMEOUT", "not-a-duration")
	if got := preflightCheckTimeout(); got != defaultPreflightTimeout {
		t.Errorf("invalid override should fall back to default %s, got %s", defaultPreflightTimeout, got)
	}
	// Zero/negative must also fall back (a 0 timeout would fail every check instantly).
	t.Setenv("BD_PREFLIGHT_TIMEOUT", "0s")
	if got := preflightCheckTimeout(); got != defaultPreflightTimeout {
		t.Errorf("zero override should fall back to default %s, got %s", defaultPreflightTimeout, got)
	}
}

func TestRunBoundedCommand_Success(t *testing.T) {
	out, timedOut, err := runBoundedCommand("true")
	if timedOut {
		t.Error("expected no timeout for 'true'")
	}
	if err != nil {
		t.Errorf("expected 'true' to succeed, got %v (output: %s)", err, out)
	}
}

func TestRunBoundedCommand_Timeout(t *testing.T) {
	t.Setenv("BD_PREFLIGHT_TIMEOUT", "150ms")
	start := time.Now()
	_, timedOut, err := runBoundedCommand("sleep", "10")
	elapsed := time.Since(start)
	if !timedOut {
		t.Error("expected sleep 10 to time out under a 150ms bound")
	}
	if err == nil {
		t.Error("expected a non-nil error when the command is killed on timeout")
	}
	if elapsed > 5*time.Second {
		t.Errorf("timeout did not bound runtime; took %s", elapsed)
	}
}

// TestRunBoundedCommand_KillsProcessGroup is the crux of beads-z6zb: a
// timed-out check must reap the whole child process tree, not orphan
// grandchildren (go test spawns compile + test-binary children). We start a
// shell that backgrounds a child which writes a sentinel after a delay; if the
// process group is killed on timeout the sentinel must never appear.
func TestRunBoundedCommand_KillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "orphan-survived")
	// Parent shell spawns a backgrounded grandchild, then blocks. On timeout
	// the parent is killed; only a process-group kill also stops the grandchild.
	script := "( sleep 3; touch " + sentinel + " ) & sleep 30"

	t.Setenv("BD_PREFLIGHT_TIMEOUT", "200ms")
	_, timedOut, _ := runBoundedCommand("sh", "-c", script)
	if !timedOut {
		t.Fatal("expected the parent shell to time out")
	}

	// Wait past the grandchild's delay; if it was reaped with the group the
	// sentinel is never created.
	time.Sleep(4 * time.Second)
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("grandchild survived timeout (orphaned) — process group was not killed")
	}
}

func TestTimeoutMessage_MentionsOverride(t *testing.T) {
	msg := timeoutMessage(2*time.Minute, []byte("partial output"))
	if !strings.Contains(msg, "timed out") {
		t.Errorf("expected 'timed out' in message, got %q", msg)
	}
	if !strings.Contains(msg, "BD_PREFLIGHT_TIMEOUT") {
		t.Errorf("expected the override env var to be mentioned, got %q", msg)
	}
	if !strings.Contains(msg, "partial output") {
		t.Errorf("expected captured output to be preserved, got %q", msg)
	}
}
