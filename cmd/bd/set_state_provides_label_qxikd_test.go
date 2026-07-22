//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestSetStateRejectsProvidesLabel_qxikd is the end-to-end regression for
// beads-qxikd: `bd set-state <id> provides=cap` (which builds the label as
// <dimension>:<value> = "provides:cap") must reject the reserved single-provider
// capability label, at parity with single `bd create` / graph / cook / label add
// (beads-o70m1 / 1zq73) and the create-form/markdown/quick/update/tag seams
// (beads-4sfae). beads-rdzwu added reservedIdentityLabelError at this same
// set-state chokepoint but not providesLabelError — so set-state was the last
// identity-guarded seam that still let a hand-set provides: through, stamping the
// reserved capability label via tx.AddLabel (direct) / labelUC.AddLabel (proxied)
// outside `bd ship` (the only sanctioned applier). The guard runs at the RunE
// chokepoint before the usesProxiedServer() split, so ONE site covers direct +
// proxied. MUTATION-VERIFY: remove the providesLabelError call at state.go → this
// leg goes RED (set-state succeeds, RC=0, provides:cap lands).
func TestSetStateRejectsProvidesLabel_qxikd(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sp")

	created := bdCreate(t, bd, dir, "set-state provides-label target", "--type", "task")

	// Both separators the verb accepts ('=' and ':') build the same reserved
	// label, so exercise both to prove the guard sees the constructed
	// dimension:value form.
	for _, spec := range []string{"provides=cap", "provides:cap"} {
		out, err := runSetStateWithEnv(t, bd, dir, created.ID, spec, false)
		if err == nil {
			t.Errorf("bd set-state %s %q should reject the reserved provides: capability label, got success:\n%s", created.ID, spec, out)
			continue
		}
		if !strings.Contains(out, "provides:") || !strings.Contains(out, "bd ship") {
			t.Errorf("rejection for %q should name 'provides:' and hint 'bd ship'; output = %q", spec, out)
		}
	}
}

// TestSetStateProvidesNotGTInternalExempt_qxikd verifies providesLabelError has
// NO GT_INTERNAL bypass (unlike the identity guard): 'provides:' is applied by
// `bd ship` via storage, not any CLI seam, so even a gt-internal write must not
// hand-set it at set-state.
func TestSetStateProvidesNotGTInternalExempt_qxikd(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sg")

	created := bdCreate(t, bd, dir, "gt internal provides set-state target", "--type", "task")

	out, err := runSetStateWithEnv(t, bd, dir, created.ID, "provides=cap", true)
	if err == nil {
		t.Errorf("bd set-state provides=cap should be rejected even under GT_INTERNAL (ship applies it via storage, not this seam), got success:\n%s", out)
	}
}
