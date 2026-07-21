//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestListBlockedSignalFamilies_h7u56_dqje3 is the end-to-end teeth for
// beads-h7u56 (conditional-blocks) + beads-dqje3 (waits-for): the DEFAULT bd
// list pretty view (and --status blocked) must show the ● blocked glyph +
// "(blocked by: X)" annotation for an issue that bd ready withholds and bd
// blocked lists — for ALL blocking-edge families, not just the hard
// 'blocks'/'parent-child' pair beads-54lww covered.
//
// The display seam (GetBlockingInfoForIssues → blockedByMap) used a narrower
// type filter (type IN ('blocks','parent-child')) than the authority
// (is_blocked / bd ready / bd blocked, which count {blocks, conditional-blocks,
// waits-for} + parent-child inheritance), so a genuinely-blocked
// conditional-blocks dependent or waits-for waiter rendered as ○ open (looked
// runnable) in the primary browse view — the exact under-signalling 54lww set
// out to kill, still live for these two families.
//
// Mutation-verified LOAD-BEARING: reverting the dependency_queries.go
// conditional-blocks/waits-for legs (or the proxied domain/db/dependency.go
// twin) makes the corresponding subtest RED (○ open, no annotation). The
// close-semantics subtests additionally guard against OVER-signalling (a
// failure-closed conditional target or a satisfied waits-for gate must flip
// back to ○ open, matching bd ready).
func TestListBlockedSignalFamilies_h7u56_dqje3(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bf")

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	// lineFor returns the output row whose ID field is id (begins "<icon> <id> "),
	// i.e. the issue's OWN row — not a row that merely mentions it inside another
	// issue's "(blocked by: ...)" annotation.
	lineFor := func(out, id string) string {
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(ln, " "+id+" ") {
				return ln
			}
		}
		return ""
	}

	// assertBlocked asserts the display treats dep as blocked-by-blocker across
	// the DEFAULT pretty view AND `bd list --status blocked`, and that it agrees
	// with the authority (present in `bd blocked`, absent from `bd ready`).
	assertBlocked := func(t *testing.T, dep, blocker string) {
		t.Helper()
		pretty := run(t, "list", "--pretty")
		ln := lineFor(pretty, dep)
		if ln == "" {
			t.Fatalf("could not find %s's line in --pretty output:\n%s", dep, pretty)
		}
		if !strings.Contains(ln, "●") {
			t.Errorf("--pretty: blocked %s should render ● blocked, got line: %q\nfull:\n%s", dep, ln, pretty)
		}
		if strings.Contains(ln, "○") {
			t.Errorf("--pretty: blocked %s must not render ○ open, got line: %q", dep, ln)
		}
		if !strings.Contains(ln, "(blocked by: "+blocker+")") {
			t.Errorf("--pretty: expected '(blocked by: %s)' on %s, got line: %q", blocker, dep, ln)
		}

		// `bd list --status blocked` must not self-contradict: it filters the
		// row IN via is_blocked=1, so it must also RENDER it ● (not ○ open).
		statusBlocked := run(t, "list", "--status", "blocked")
		sbLine := lineFor(statusBlocked, dep)
		if sbLine == "" {
			t.Errorf("bd list --status blocked should include the blocked %s (is_blocked=1), got:\n%s", dep, statusBlocked)
		} else if !strings.Contains(sbLine, "●") || strings.Contains(sbLine, "○") {
			t.Errorf("bd list --status blocked must render %s ● blocked (not ○ open), got line: %q", dep, sbLine)
		}

		// Authority parity: blocked ⇒ in `bd blocked`, NOT in `bd ready`.
		blocked := run(t, "blocked")
		if !strings.Contains(blocked, dep) {
			t.Errorf("bd blocked should list the blocked %s, got:\n%s", dep, blocked)
		}
		ready := run(t, "ready")
		if rl := lineFor(ready, dep); rl != "" {
			t.Errorf("bd ready must withhold the blocked %s, but it appeared: %q", dep, rl)
		}
	}

	// assertUnblocked asserts the display + authority BOTH treat dep as runnable
	// (the guard against over-signalling once the blocker/gate is resolved).
	assertUnblocked := func(t *testing.T, dep string) {
		t.Helper()
		pretty := run(t, "list", "--pretty")
		ln := lineFor(pretty, dep)
		if ln == "" {
			t.Fatalf("could not find %s's line in --pretty output:\n%s", dep, pretty)
		}
		// The col-1 STATUS glyph must be ○ open. NB: the line ALSO carries the
		// priority icon "● P%d", so we cannot test for the absence of ● — the
		// robust discriminator (matching the 54lww test) is the PRESENCE of the
		// ○ status glyph, which the blocked-override replaces with ● when blocked.
		if !strings.Contains(ln, "○") {
			t.Errorf("--pretty: unblocked %s should render the ○ open status glyph, got line: %q", dep, ln)
		}
		if strings.Contains(ln, "blocked by") {
			t.Errorf("--pretty: unblocked %s must not carry a blocked annotation, got line: %q", dep, ln)
		}
		ready := run(t, "ready")
		if lineFor(ready, dep) == "" {
			t.Errorf("bd ready should list the now-unblocked %s, got:\n%s", dep, ready)
		}
	}

	t.Run("conditional_blocks_open_target_signals_blocked", func(t *testing.T) {
		// h7u56: DEP conditional-blocks BLK; BLK open ⇒ DEP blocked.
		blk := bdCreate(t, bd, dir, "cond blocker open", "-p", "1")
		dep := bdCreate(t, bd, dir, "cond dependent", "-p", "1")
		bdDep(t, bd, dir, "add", dep.ID, blk.ID, "--type", "conditional-blocks")
		assertBlocked(t, dep.ID, blk.ID)
	})

	t.Run("conditional_blocks_success_close_stays_blocked", func(t *testing.T) {
		// h7u56 + a3hm: "B runs only if A FAILS" — a SUCCESS close of A means B's
		// condition can never be met, so B STAYS blocked (reason-aware). The
		// display must agree with the authority, not drop the success-closed
		// conditional blocker.
		blk := bdCreate(t, bd, dir, "cond blocker success", "-p", "1")
		dep := bdCreate(t, bd, dir, "cond dependent success", "-p", "1")
		bdDep(t, bd, dir, "add", dep.ID, blk.ID, "--type", "conditional-blocks")
		bdClose(t, bd, dir, blk.ID, "--reason", "done") // success (no failure keyword)
		assertBlocked(t, dep.ID, blk.ID)
	})

	t.Run("conditional_blocks_failure_close_unblocks", func(t *testing.T) {
		// h7u56 + a3hm: a FAILURE close of A satisfies "B runs only if A fails",
		// so B becomes runnable — display AND authority both flip to ○ open. This
		// is the over-signalling guard: the widened display query must still drop
		// a failure-closed conditional blocker.
		blk := bdCreate(t, bd, dir, "cond blocker fail", "-p", "1")
		dep := bdCreate(t, bd, dir, "cond dependent fail", "-p", "1")
		bdDep(t, bd, dir, "add", dep.ID, blk.ID, "--type", "conditional-blocks")
		bdClose(t, bd, dir, blk.ID, "--reason", "failed") // failure keyword
		assertUnblocked(t, dep.ID)
	})

	t.Run("waits_for_open_child_signals_blocked", func(t *testing.T) {
		// dqje3: WT waits-for SP (fanout gate). SP has an OPEN parent-child child
		// ⇒ the all-children gate is unsatisfied ⇒ WT blocked. The named blocker
		// is the spawner SP.
		sp := bdCreate(t, bd, dir, "spawner", "-p", "1")
		wt := bdCreate(t, bd, dir, "waiter", "-p", "1")
		ch := bdCreate(t, bd, dir, "child", "-p", "1")
		bdDep(t, bd, dir, "add", ch.ID, sp.ID, "--type", "parent-child")
		bdDep(t, bd, dir, "add", wt.ID, sp.ID, "--type", "waits-for")
		assertBlocked(t, wt.ID, sp.ID)
	})

	t.Run("waits_for_gate_satisfied_unblocks", func(t *testing.T) {
		// dqje3: once the spawner's only child closes, the all-children gate is
		// satisfied ⇒ the waiter flips to ○ open in the display AND appears in bd
		// ready (over-signalling guard: the gated display query must release it).
		sp := bdCreate(t, bd, dir, "spawner sat", "-p", "1")
		wt := bdCreate(t, bd, dir, "waiter sat", "-p", "1")
		ch := bdCreate(t, bd, dir, "child sat", "-p", "1")
		bdDep(t, bd, dir, "add", ch.ID, sp.ID, "--type", "parent-child")
		bdDep(t, bd, dir, "add", wt.ID, sp.ID, "--type", "waits-for")
		bdClose(t, bd, dir, ch.ID, "--reason", "done")
		assertUnblocked(t, wt.ID)
	})

	t.Run("hard_blocks_54lww_regression_still_signals", func(t *testing.T) {
		// Regression guard: the original 54lww hard-blocks glyph must stay green
		// after widening the query to the other families.
		blk := bdCreate(t, bd, dir, "hard blocker", "-p", "1")
		dep := bdCreate(t, bd, dir, "hard dependent", "-p", "1")
		bdDep(t, bd, dir, "add", dep.ID, blk.ID, "--type", "blocks")
		assertBlocked(t, dep.ID, blk.ID)
	})
}
