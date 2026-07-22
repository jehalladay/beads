//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestSetStateRejectsProvidesLabel_8o666 is the end-to-end regression for
// beads-8o666: `bd set-state <id> provides:cap` (which builds the label as
// <dimension>:<value> = "provides:cap") must reject the reserved capability
// label, matching the o70m1 guard already enforced at `bd create` +
// `bd create --graph`. set-state stamps the constructed label via AddLabel, so
// without this guard a hand-set set-state mints the single-provider capability
// label OUTSIDE `bd ship`, bypassing ship's CLOSED + single-provider
// invariants. The set-state seam is NOT in beads-4sfae's create-verb list
// (create_form/quick/markdown/update/tag), so it needed its own guard beside
// the existing reservedIdentityLabelError check (beads-rdzwu, state.go:155),
// before the usesProxiedServer() split so ONE site covers direct + proxied.
//
// MUTATION-VERIFY: remove the providesLabelError check at state.go and this
// reject case goes RED (set-state succeeds, stamping provides:cap).
func TestSetStateRejectsProvidesLabel_8o666(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sp")

	created := bdCreate(t, bd, dir, "set-state provides-label target", "--type", "task")

	// Both separators the verb accepts ('=' and ':') build the same reserved
	// "provides:<cap>" label, so exercise both. providesLabelError is UNGATED,
	// so GT_INTERNAL is irrelevant here (unlike the gt: identity family); use
	// the non-internal path.
	for _, spec := range []string{"provides=mycap", "provides:mycap"} {
		out, err := runSetStateWithEnv(t, bd, dir, created.ID, spec, false)
		if err == nil {
			t.Errorf("bd set-state %s %q should reject the reserved 'provides:' capability label (o70m1 bypass), got success:\n%s", created.ID, spec, out)
			continue
		}
		if !strings.Contains(out, "provides:") || !strings.Contains(out, "bd ship") {
			t.Errorf("rejection for %q should name 'provides:' and the 'bd ship' hint; output = %q", spec, out)
		}
	}
}

// TestSetStateProvidesGuardDoesNotOverReach_8o666 verifies the guard is exact:
// an ordinary dimension whose value merely contains the word "provides" (but
// does not form the reserved "provides:" prefix on the constructed label) still
// sets normally, and a normal state dimension is unaffected.
func TestSetStateProvidesGuardDoesNotOverReach_8o666(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "so")

	created := bdCreate(t, bd, dir, "ordinary set-state target", "--type", "task")

	// dimension "status", value "provides" → label "status:provides" — NOT a
	// reserved provides:<cap> label, so it must succeed (the guard keys on the
	// full constructed label's provides: prefix, not the substring anywhere).
	out, err := runSetStateWithEnv(t, bd, dir, created.ID, "status=provides", false)
	if err != nil {
		t.Fatalf("bd set-state status=provides (ordinary label 'status:provides') should succeed, got err: %v\n%s", err, out)
	}
}
