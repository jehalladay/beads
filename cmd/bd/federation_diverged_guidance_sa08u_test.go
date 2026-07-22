//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestFederationSyncDivergedGuidance_sa08u is the teeth for beads-sa08u:
// `bd federation sync` printed a bare "✗ merge failed: ... no common ancestor"
// with NO recovery guidance when two towns were independently `bd init`'d
// (diverged histories, no common merge base). `bd dolt push/pull` classify the
// SAME Dolt error via isDivergedHistoryErr and print actionable guidance — but
// federation had no such branch, so the operator was stranded.
//
// The fix routes the diverged-history case through
// printFederationDivergedGuidance (federation-specific export/re-seed/import
// recovery, NOT dolt push/pull's bd-bootstrap / --force guidance, which is
// wrong for a peer↔peer divergence where neither town is the other's origin).
//
// This drives the REAL runFederationSync path end-to-end against a genuinely
// diverged peer (not a mocked error) so the routing itself is load-bearing:
// removing `if isDivergedHistoryErr(err) { printFederationDivergedGuidance }`
// makes the guidance vanish and this test goes RED (a pure output-only test of
// the helper would not — the veneer trap).
//
// Repro: seed a file:// remote from town B (independent history), then have
// town A (its own independent `bd init`) add that same remote as a peer and
// sync. A's main and B's main share no ancestor → DOLT_MERGE fails with
// "no common ancestor".
func TestFederationSyncDivergedGuidance_sa08u(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt federation tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A shared file:// remote path both towns will reference. Town B seeds it;
	// town A then tries to merge it and diverges.
	remotePath := t.TempDir() + "/hub"
	remoteURL := "file://" + remotePath

	// ── Town B: independent init, one issue, publish (bootstrap) to the hub. ──
	dirB, _, _ := bdInit(t, bd, "--prefix", "townb")
	bdCreate := func(dir, title string) {
		t.Helper()
		cmd := exec.Command(bd, "create", title, "-p", "1")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd create in %s: %v\n%s", dir, err, out)
		}
	}
	bdCreate(dirB, "town b issue")
	bdFederation(t, bd, dirB, "add-peer", "hub", remoteURL)
	// First sync bootstraps the empty hub with B's history (beads-aapwu).
	bdFederation(t, bd, dirB, "sync", "--peer", "hub")

	// ── Town A: a SEPARATE independent init — no shared ancestor with B. ──
	dirA, _, _ := bdInit(t, bd, "--prefix", "towna")
	bdCreate(dirA, "town a issue")
	bdFederation(t, bd, dirA, "add-peer", "hub", remoteURL)

	// A syncs against the hub (now carrying B's independent history). The merge
	// has no common ancestor → diverged-history failure.
	runSync := func(extra ...string) (string, int) {
		t.Helper()
		args := append([]string{"federation", "sync", "--peer", "hub"}, extra...)
		cmd := exec.Command(bd, args...)
		cmd.Dir = dirA
		cmd.Env = bdEnv(dirA)
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatalf("federation sync exec error (not an exit error): %v\n%s", err, out)
			}
		}
		return string(out), code
	}

	out, code := runSync()

	// Precondition: the merge must actually have diverged (the scenario is
	// valid). If Dolt merged cleanly, the test's premise is wrong — fail loud
	// rather than silently pass on a non-diverged path.
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "common ancestor") {
		t.Fatalf("sa08u precondition: expected a diverged-history (no common ancestor) merge failure, got (code=%d):\n%s", code, out)
	}

	// The fix: federation-specific recovery guidance must be printed.
	wantPhrases := []string{
		"diverged",  // names the condition
		"Recovery",  // has an actionable recovery section
		"bd export", // save local-only issues (peer↔peer, not bd bootstrap)
		"bd import", // re-seed from the canonical town
	}
	for _, p := range wantPhrases {
		if !strings.Contains(out, p) {
			t.Errorf("sa08u: diverged federation sync must print recovery guidance containing %q; got:\n%s", p, out)
		}
	}

	// It must NOT hand the operator the dolt push/pull guidance, which is wrong
	// for a peer↔peer federation divergence (no single origin to re-clone /
	// force-push to).
	for _, wrong := range []string{"bd bootstrap", "--force"} {
		if strings.Contains(out, wrong) {
			t.Errorf("sa08u: federation diverged guidance must NOT reuse dolt push/pull's %q (peer↔peer has no origin); got:\n%s", wrong, out)
		}
	}
}
