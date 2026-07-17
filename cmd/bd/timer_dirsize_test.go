package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-drj3: hermetic tests for checkTimer (gate.go, pure timer logic) and
// getDirSize (compact.go, filesystem walk over a temp dir). Both verified 0% +
// no test references.

func TestCheckTimer(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("no timeout configured is an error", func(t *testing.T) {
		g := &types.Issue{CreatedAt: base} // Timeout == 0
		resolved, escalated, reason, err := checkTimer(g, base)
		if err == nil {
			t.Fatal("expected error when no timeout set")
		}
		if resolved || escalated {
			t.Errorf("no-timeout should not resolve/escalate, got resolved=%v escalated=%v", resolved, escalated)
		}
		if reason == "" {
			t.Error("expected a reason string")
		}
	})

	t.Run("expired timer resolves", func(t *testing.T) {
		g := &types.Issue{CreatedAt: base, Timeout: time.Hour}
		now := base.Add(90 * time.Minute) // 30m past expiry
		resolved, escalated, reason, err := checkTimer(g, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !resolved {
			t.Error("timer past expiry should resolve")
		}
		if escalated {
			t.Error("checkTimer never escalates")
		}
		if reason == "" || !contains(reason, "expired") {
			t.Errorf("reason should mention expiry, got %q", reason)
		}
	})

	t.Run("not-yet-expired timer reports remaining", func(t *testing.T) {
		g := &types.Issue{CreatedAt: base, Timeout: time.Hour}
		now := base.Add(20 * time.Minute) // 40m remaining
		resolved, _, reason, err := checkTimer(g, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved {
			t.Error("timer before expiry should not resolve")
		}
		if !contains(reason, "expires in") {
			t.Errorf("reason should report remaining time, got %q", reason)
		}
	})
}

func TestGetDirSize(t *testing.T) {
	t.Run("sums file sizes recursively, ignores dirs", func(t *testing.T) {
		dir := t.TempDir()
		writeSizedFile(t, filepath.Join(dir, "a.txt"), 10)
		sub := filepath.Join(dir, "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeSizedFile(t, filepath.Join(sub, "b.txt"), 25)

		got, err := getDirSize(dir)
		if err != nil {
			t.Fatalf("getDirSize: %v", err)
		}
		if got != 35 {
			t.Errorf("size = %d, want 35", got)
		}
	})

	t.Run("empty dir is zero", func(t *testing.T) {
		got, err := getDirSize(t.TempDir())
		if err != nil {
			t.Fatalf("getDirSize: %v", err)
		}
		if got != 0 {
			t.Errorf("empty dir size = %d, want 0", got)
		}
	})

	t.Run("nonexistent path errors", func(t *testing.T) {
		if _, err := getDirSize(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
			t.Error("expected an error walking a nonexistent path")
		}
	})
}

func writeSizedFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
