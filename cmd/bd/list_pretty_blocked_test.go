package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-54lww: the DEFAULT bd list view is the pretty/tree renderer
// (displayPrettyListWithDeps* -> printPrettyTree -> formatPrettyIssue). GH#2858
// (show ● blocked when an open issue has active open blockers, and append a
// "(blocked by: X)" annotation) was wired into the compact/agent seam
// (formatIssueCompact) but NOT into the pretty/tree seam, so the default view
// UNDER-SIGNALS blocked state: an open issue with an active blocker rendered as
// "○ ... open" with no annotation, while --flat/compact, bd ready, and bd
// blocked all agree it is blocked.
//
// Fix: thread a per-issue blockedBy set into formatPrettyIssue (via
// formatPrettyIssueBlocked and the display/tree chain) so it (a) overrides the
// icon to StatusBlocked for open issues with active blockers and (b) appends
// "(blocked by: ...)" like formatIssueCompact.
//
// Mutation proof: drop the override/annotation in formatPrettyIssueBlocked ->
// these assertions fail (icon stays ○, no annotation) -> RED.

func TestFormatPrettyIssueBlocked_54lww(t *testing.T) {
	openBlocked := &types.Issue{ID: "lw-b", Title: "Blocked task", Status: types.StatusOpen, Priority: 1}

	t.Run("open issue with active blockers renders ● blocked + annotation", func(t *testing.T) {
		got := formatPrettyIssueBlocked(openBlocked, []string{"lw-a"})
		if !strings.Contains(got, "●") {
			t.Errorf("expected blocked icon ● for open issue with active blocker, got: %q", got)
		}
		if strings.HasPrefix(strings.TrimSpace(got), "○") {
			t.Errorf("open-blocked issue must not render the ○ open icon, got: %q", got)
		}
		if !strings.Contains(got, "(blocked by: lw-a)") {
			t.Errorf("expected '(blocked by: lw-a)' annotation, got: %q", got)
		}
	})

	t.Run("open issue with NO blockers renders ○ open, no annotation (unchanged)", func(t *testing.T) {
		got := formatPrettyIssueBlocked(openBlocked, nil)
		if !strings.Contains(got, "○") {
			t.Errorf("expected open icon ○ for unblocked open issue, got: %q", got)
		}
		if strings.Contains(got, "blocked by") {
			t.Errorf("unblocked issue must not show a blocked-by annotation, got: %q", got)
		}
	})

	t.Run("in_progress issue with blockers keeps its own icon (GH#2858 override is open-only)", func(t *testing.T) {
		inProg := &types.Issue{ID: "lw-p", Title: "Started", Status: types.StatusInProgress, Priority: 1}
		got := formatPrettyIssueBlocked(inProg, []string{"lw-a"})
		// The compact seam only overrides the icon for StatusOpen; a started
		// issue keeps ◐. But the annotation still surfaces the relationship.
		if strings.Contains(got, "○") {
			t.Errorf("in_progress issue must not render ○, got: %q", got)
		}
		if !strings.Contains(got, "(blocked by: lw-a)") {
			t.Errorf("expected '(blocked by: lw-a)' annotation on in_progress issue, got: %q", got)
		}
	})

	t.Run("formatPrettyIssue (no blocker data) is unchanged — open stays ○", func(t *testing.T) {
		got := formatPrettyIssue(openBlocked)
		if !strings.Contains(got, "○") {
			t.Errorf("formatPrettyIssue with no deps must keep ○ open, got: %q", got)
		}
		if strings.Contains(got, "blocked by") {
			t.Errorf("formatPrettyIssue must not annotate blockers, got: %q", got)
		}
	})
}

// TestDisplayPrettyListWithBlocked_54lww exercises the full tree path: an open
// issue with an active blocker, both in the displayed set, must render with the
// ● blocked icon and the "(blocked by: A)" annotation in the default pretty
// output — matching bd list --flat.
func TestDisplayPrettyListWithBlocked_54lww(t *testing.T) {
	a := &types.Issue{ID: "lw-a", Title: "Blocker", Status: types.StatusOpen, Priority: 1}
	b := &types.Issue{ID: "lw-b", Title: "Blocked", Status: types.StatusOpen, Priority: 1}
	issues := []*types.Issue{a, b}
	// b is blocked by a (active: a is open).
	blockedBy := map[string][]string{"lw-b": {"lw-a"}}

	out := capturePrettyFooter(t, func() {
		displayPrettyListWithBlocked(issues, false, nil, false, "", blockedBy)
	})

	// The blocked child line must carry ● + annotation.
	if !strings.Contains(out, "●") {
		t.Errorf("pretty output should render ● blocked for lw-b, got:\n%s", out)
	}
	if !strings.Contains(out, "(blocked by: lw-a)") {
		t.Errorf("pretty output should annotate '(blocked by: lw-a)', got:\n%s", out)
	}
	// The unblocked blocker itself stays open (○) and unannotated. Find its
	// OWN line (the one whose ID field is lw-a, i.e. begins "<icon> lw-a "),
	// not the blocked line that mentions lw-a in its annotation.
	lines := strings.Split(out, "\n")
	for _, ln := range lines {
		if strings.Contains(ln, " lw-a ") && !strings.Contains(ln, "lw-b") {
			if strings.Contains(ln, "blocked by") {
				t.Errorf("blocker lw-a should not be annotated as blocked, got line: %q", ln)
			}
			if !strings.Contains(ln, "○") {
				t.Errorf("unblocked blocker lw-a should keep ○ open, got line: %q", ln)
			}
		}
	}
}
