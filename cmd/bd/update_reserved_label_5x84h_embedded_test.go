//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedUpdateRejectsReservedIdentityLabel is the beads-5x84h teeth.
//
// reservedIdentityLabelError (reserved_labels.go, beads-3c4g) reserves the gt
// identity family (gt:agent/gt:role/gt:rig) at write time — a hand-set one hides
// the bead from `bd ready` (the beads-wqs discriminator). The guard is enforced
// on the CREATE seams (create.go:200 + the family kmw78/f8fvh/kvq0v/1077e) and
// on `bd label add` (label.go:277), but the UPDATE mutation path adds/sets
// labels via applyLabelUpdates with NO guard, so `bd update <bead> --add-label
// gt:role` (or --set-labels gt:agent) silently stamped a reserved identity label
// onto an existing bead — the create-family's mutation-path twin.
//
// The fix loops --add-label/--set-labels values through reservedIdentityLabelError
// at the pre-dispatch chokepoint in update.go RunE (beside the a0nmp guard),
// covering BOTH the direct and proxied update paths with one site.
//
// Mutation: delete the guard loop → the reserved cases go from rc!=0 back to
// rc=0 and the label lands (RED). The non-identity control (--add-label
// priority:p0) and --remove-label control stay GREEN, proving the guard is
// precise and only fires on the reserved family.
func TestEmbeddedUpdateRejectsReservedIdentityLabel(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "url")

	hasLabel := func(iss *types.Issue, want string) bool {
		for _, l := range iss.Labels {
			if l == want {
				return true
			}
		}
		return false
	}

	// (1) --add-label gt:role / gt:agent / gt:rig must be refused on an ordinary
	//     (non-GT_INTERNAL) write, and the label must NOT land.
	for _, label := range []string{"gt:agent", "gt:role", "gt:rig"} {
		t.Run("update_add_label_"+strings.ReplaceAll(label, ":", "_")+"_refuses", func(t *testing.T) {
			iss := bdCreate(t, bd, dir, "add-label spoof target")
			out := bdUpdateFail(t, bd, dir, iss.ID, "--add-label", label)
			if !strings.Contains(out, label) {
				t.Errorf("rejection should name the reserved label %q, got:\n%s", label, out)
			}
			if got := bdShow(t, bd, dir, iss.ID); hasLabel(got, label) {
				t.Errorf("reserved label %q must NOT land via update --add-label; labels=%v", label, got.Labels)
			}
		})
	}

	// (2) --set-labels gt:agent must be refused too (the whole-replace leg).
	t.Run("update_set_labels_reserved_refuses", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "set-labels spoof target")
		out := bdUpdateFail(t, bd, dir, iss.ID, "--set-labels", "gt:agent")
		if !strings.Contains(out, "gt:agent") {
			t.Errorf("rejection should name the reserved label, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, iss.ID); hasLabel(got, "gt:agent") {
			t.Errorf("reserved label must NOT land via update --set-labels; labels=%v", got.Labels)
		}
	})

	// (3) A reserved label mixed with an ordinary one on --set-labels must reject
	//     the WHOLE set (pre-dispatch, before any write) — the ordinary label
	//     must not land either.
	t.Run("update_set_labels_mixed_reserved_rejects_whole_set", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "mixed set-labels target")
		out := bdUpdateFail(t, bd, dir, iss.ID, "--set-labels", "priority:p0", "--set-labels", "gt:rig")
		if !strings.Contains(out, "gt:rig") {
			t.Errorf("rejection should name the reserved label, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, iss.ID); hasLabel(got, "priority:p0") || hasLabel(got, "gt:rig") {
			t.Errorf("a mixed set with a reserved label must land NOTHING (pre-write reject); labels=%v", got.Labels)
		}
	})

	// (4) PRECISION control: an ordinary label via --add-label must still land
	//     (rc=0) — the guard only fires on the reserved family.
	t.Run("update_add_ordinary_label_succeeds", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "ordinary add-label target")
		bdUpdate(t, bd, dir, iss.ID, "--add-label", "priority:p0")
		if got := bdShow(t, bd, dir, iss.ID); !hasLabel(got, "priority:p0") {
			t.Errorf("ordinary label priority:p0 must land via update --add-label; labels=%v", got.Labels)
		}
	})

	// (5) PRECISION control: --remove-label gt:role is NOT a spoof and must be
	//     allowed (rc=0) — removing a reserved label is unguarded by design,
	//     matching `bd label remove`. (No such label present ⇒ idempotent no-op,
	//     still rc=0; what matters is the guard does not reject the removal.)
	t.Run("update_remove_reserved_label_allowed", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "remove reserved target")
		bdUpdate(t, bd, dir, iss.ID, "--remove-label", "gt:role")
		if got := bdShow(t, bd, dir, iss.ID); hasLabel(got, "gt:role") {
			t.Errorf("gt:role should not be present after remove; labels=%v", got.Labels)
		}
	})

	// (6) GT_INTERNAL privileged write: gt's own registration stamp must still
	//     succeed (the guard is GT_INTERNAL-gated) — proving the fix does not
	//     break town-wide agent/rig/role registration.
	t.Run("update_add_reserved_label_allowed_with_gt_internal", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "gt-internal stamp target")
		cmd := exec.Command(bd, "update", iss.ID, "--add-label", "gt:agent")
		cmd.Dir = dir
		cmd.Env = append(bdEnv(dir), gtInternalEnv+"="+gtInternalValue)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("GT_INTERNAL update --add-label gt:agent should succeed, got err=%v\n%s", err, out)
		}
		if got := bdShow(t, bd, dir, iss.ID); !hasLabel(got, "gt:agent") {
			t.Errorf("gt:agent must land on a GT_INTERNAL privileged write; labels=%v", got.Labels)
		}
	})
}
