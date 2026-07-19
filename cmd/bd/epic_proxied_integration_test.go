//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerEpic proves `bd epic status` and `bd epic close-eligible` are
// proxied-server-aware (beads-92ld). Before the fix both subcommands called
// store.GetEpicsEligibleForClosure() (and close-eligible additionally
// store.CloseIssue()) with a nil global `store` in proxiedServerMode ('epic' is
// not in noDbCommands) → a nil-pointer panic on hub-connected crew. The
// eligibility read lived on the concrete stores + storage.Storage but NOT on
// the domain IssueUseCase, so the fix is an interface-extension leg (mh3e/t3wg
// class): widen GetEpicsEligibleForClosureInTx to the DBTX seam, add
// GetEpicsEligibleForClosure to IssueSQLRepository + IssueUseCase (delegating to
// that InTx), and route both subcommands through the proxied UOW stack.
func TestProxiedServerEpic(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// panicked reports whether the combined output shows the pre-fix failure
	// signature (nil global store dereference / "storage is nil").
	panicked := func(combined string) string {
		switch {
		case strings.Contains(combined, "storage is nil"):
			return "hit 'storage is nil'"
		case strings.Contains(combined, "nil pointer dereference"), strings.Contains(combined, "panic:"):
			return "PANICKED (nil store deref)"
		default:
			return ""
		}
	}

	t.Run("epic_status_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "eps")
		bdProxiedCreate(t, bd, p.dir, "Lone epic", "--type", "epic")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "epic", "status")
		if sig := panicked(stdout + stderr); sig != "" {
			t.Fatalf("bd epic status %s in proxied mode (not proxied-server-aware):\nerr:%v\nstdout:\n%s\nstderr:\n%s", sig, err, stdout, stderr)
		}
	})

	t.Run("epic_status_json_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "epj")
		bdProxiedCreate(t, bd, p.dir, "Json epic", "--type", "epic")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "epic", "status", "--json")
		if sig := panicked(stdout + stderr); sig != "" {
			t.Fatalf("bd epic status --json %s in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", sig, err, stdout, stderr)
		}
	})

	// Stronger teeth: an epic whose only child is closed must SURFACE as eligible
	// through the proxied read path, and close-eligible must actually close it
	// through the proxied write path.
	t.Run("close_eligible_surfaces_and_closes_via_proxy", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "epc")
		epic := bdProxiedCreate(t, bd, p.dir, "Eligible epic", "--type", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "Only child", "--type", "task", "--parent", epic.ID)

		// Close the sole child → the epic becomes eligible for closure.
		if _, err := bdProxiedRun(t, bd, p.dir, "close", child.ID); err != nil {
			t.Fatalf("closing child %s failed: %v", child.ID, err)
		}

		// Proxied read: --eligible-only must list the epic (proves the UOW read
		// resolved a store, not the nil global).
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "epic", "status", "--eligible-only")
		combined := stdout + stderr
		if sig := panicked(combined); sig != "" {
			t.Fatalf("bd epic status --eligible-only %s in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", sig, err, stdout, stderr)
		}
		if !strings.Contains(stdout, epic.ID) {
			t.Fatalf("eligible epic %s not surfaced by proxied `epic status --eligible-only`:\nstdout:\n%s\nstderr:\n%s", epic.ID, stdout, stderr)
		}

		// Proxied write: close-eligible must close the epic without panic.
		stdout, stderr, err = bdProxiedRunBuffers(t, bd, p.dir, "epic", "close-eligible")
		combined = stdout + stderr
		if sig := panicked(combined); sig != "" {
			t.Fatalf("bd epic close-eligible %s in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", sig, err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd epic close-eligible errored in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, epic.ID) {
			t.Fatalf("proxied `epic close-eligible` did not report closing %s:\nstdout:\n%s\nstderr:\n%s", epic.ID, stdout, stderr)
		}

		// After closing, the epic is no longer eligible → the read path reports
		// none (proves the write actually landed through the proxy).
		stdout, stderr, err = bdProxiedRunBuffers(t, bd, p.dir, "epic", "status", "--eligible-only")
		if sig := panicked(stdout + stderr); sig != "" {
			t.Fatalf("post-close bd epic status --eligible-only %s:\nerr:%v\nstdout:\n%s\nstderr:\n%s", sig, err, stdout, stderr)
		}
		if strings.Contains(stdout, epic.ID) {
			t.Fatalf("epic %s still eligible after proxied close-eligible (write did not land):\nstdout:\n%s", epic.ID, stdout)
		}
	})
}
