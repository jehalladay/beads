//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedListBlockedSignalFamilies_h7u56_dqje3 is the PROXIED-twin teeth for
// beads-h7u56 (conditional-blocks) + beads-dqje3 (waits-for): the display
// blocked-indicator gap lived on BOTH the direct
// (issueops.queryBlockedByInfo) AND the proxied/hub
// (domain/db.DependencySQLRepository.GetBlockingInfo) paths — the recurring
// proxied-twin class. The embedded/direct teeth are in
// list_blocked_signal_families_embedded_test.go; this proves the hub-connected
// `bd list` path signals the same families identically, so a direct-only fix
// can't leave the proxied path under-signalling.
//
// Mutation-verified LOAD-BEARING against the domain/db twin: reverting the
// GetBlockingInfo type-filter widening or the waits-for leg reds the matching
// subtest here (○ open, no annotation) while the embedded teeth stay green.
func TestProxiedListBlockedSignalFamilies_h7u56_dqje3(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pbf")

	runList := func(t *testing.T, args ...string) string {
		t.Helper()
		full := append([]string{"list"}, args...)
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, full...)
		if err != nil {
			t.Fatalf("bd list %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
		}
		return stdout
	}
	lineFor := func(out, id string) string {
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(ln, " "+id+" ") {
				return ln
			}
		}
		return ""
	}

	// assertBlocked: the proxied display treats dep as blocked-by-blocker in the
	// pretty view AND `bd list --status blocked`, agreeing with bd blocked /
	// bd ready (the authority).
	assertBlocked := func(t *testing.T, dep, blocker string) {
		t.Helper()
		pretty := runList(t, "--pretty")
		ln := lineFor(pretty, dep)
		if ln == "" {
			t.Fatalf("could not find %s's line in proxied --pretty output:\n%s", dep, pretty)
		}
		if strings.Contains(ln, "○") {
			t.Errorf("proxied --pretty: blocked %s must not render ○ open, got line: %q\nfull:\n%s", dep, ln, pretty)
		}
		if !strings.Contains(ln, "(blocked by: "+blocker+")") {
			t.Errorf("proxied --pretty: expected '(blocked by: %s)' on %s, got line: %q", blocker, dep, ln)
		}
		statusBlocked := runList(t, "--status", "blocked")
		if sb := lineFor(statusBlocked, dep); sb == "" {
			t.Errorf("proxied bd list --status blocked should include blocked %s, got:\n%s", dep, statusBlocked)
		} else if strings.Contains(sb, "○") {
			t.Errorf("proxied bd list --status blocked must render %s ● (not ○ open), got line: %q", dep, sb)
		}
		blockedOut, _ := bdProxiedBlockedCapture(t, bd, p)
		if !strings.Contains(blockedOut, dep) {
			t.Errorf("proxied bd blocked should list %s, got:\n%s", dep, blockedOut)
		}
	}

	t.Run("conditional_blocks_open_target_signals_blocked", func(t *testing.T) {
		blk := bdProxiedCreate(t, bd, p.dir, "cond blocker open", "-p", "1")
		dep := bdProxiedCreate(t, bd, p.dir, "cond dependent", "-p", "1")
		bdProxiedDep(t, bd, p.dir, "add", dep.ID, blk.ID, "--type", "conditional-blocks")
		assertBlocked(t, dep.ID, blk.ID)
	})

	t.Run("conditional_blocks_success_close_stays_blocked", func(t *testing.T) {
		blk := bdProxiedCreate(t, bd, p.dir, "cond blocker success", "-p", "1")
		dep := bdProxiedCreate(t, bd, p.dir, "cond dependent success", "-p", "1")
		bdProxiedDep(t, bd, p.dir, "add", dep.ID, blk.ID, "--type", "conditional-blocks")
		bdProxiedClose(t, bd, p.dir, blk.ID, "--reason", "done") // success
		assertBlocked(t, dep.ID, blk.ID)
	})

	t.Run("waits_for_open_child_signals_blocked", func(t *testing.T) {
		sp := bdProxiedCreate(t, bd, p.dir, "spawner", "-p", "1")
		wt := bdProxiedCreate(t, bd, p.dir, "waiter", "-p", "1")
		ch := bdProxiedCreate(t, bd, p.dir, "child", "-p", "1")
		bdProxiedDep(t, bd, p.dir, "add", ch.ID, sp.ID, "--type", "parent-child")
		bdProxiedDep(t, bd, p.dir, "add", wt.ID, sp.ID, "--type", "waits-for")
		assertBlocked(t, wt.ID, sp.ID)
	})

	t.Run("hard_blocks_54lww_regression_still_signals", func(t *testing.T) {
		blk := bdProxiedCreate(t, bd, p.dir, "hard blocker", "-p", "1")
		dep := bdProxiedCreate(t, bd, p.dir, "hard dependent", "-p", "1")
		bdProxiedDep(t, bd, p.dir, "add", dep.ID, blk.ID, "--type", "blocks")
		assertBlocked(t, dep.ID, blk.ID)
	})
}
