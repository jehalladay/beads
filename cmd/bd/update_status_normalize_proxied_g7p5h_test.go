//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedUpdateStatusNormalizesCaseAndPadding_g7p5h is the teeth for
// beads-g7p5h: the PROXIED `bd update --status` path must case-fold + trim
// built-in status variants (OPEN / " open " / In_Progress) the same way the
// direct twin does (update.go:87, beads-gqvu/7wrj), instead of exact-matching
// the raw flag against the canonical-lowercase status set.
//
// Before the fix, update_input.go stored in.fields["status"]=status RAW after
// validateUpdateStatus's exact case-sensitive match, so a hub-connected /
// proxied-server client rejected `--status OPEN` with "invalid status" while
// the same input succeeded (normalized to "open") on the direct path — a
// validation-parity divergence. The single-command builder already normalizes
// issue_type (:140) and assignee (:73); status was the odd one out.
func TestProxiedUpdateStatusNormalizesCaseAndPadding_g7p5h(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	p := bdProxiedInit(t, bd, "snorm")

	cases := []struct {
		name  string
		input string
		want  types.Status
	}{
		{"uppercase", "OPEN", types.StatusOpen},
		{"leading_trailing_space", " open ", types.StatusOpen},
		{"mixed_case_underscore", "In_Progress", types.StatusInProgress},
		{"uppercase_closed", "CLOSED", types.StatusClosed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := bdProxiedCreate(t, bd, p.dir, "Status normalize "+tc.name, "--type", "task")

			// RED before the fix: proxied path rejects the case/padding variant
			// with "invalid status %q".
			if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--status", tc.input); err != nil {
				t.Fatalf("proxied update --status %q rejected (direct path accepts it): %v", tc.input, err)
			}

			got := bdProxiedShow(t, bd, p.dir, a.ID)
			if got.Status != tc.want {
				t.Errorf("status = %q after --status %q, want %q (should normalize like direct path)", got.Status, tc.input, tc.want)
			}
		})
	}
}
