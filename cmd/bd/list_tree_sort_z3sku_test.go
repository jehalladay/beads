package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-z3sku: `bd list --sort <field>` was SILENTLY IGNORED in the DEFAULT
// tree view — it always rendered priority-DESC regardless of --sort, while
// --flat/--json/--format all honored the flag (a documented-behavior
// contradiction: the flag help promises sort-by field with no note that the
// default view ignores it). ROOT: sortIssues() pre-sorts the slice, but the
// tree renderer (buildIssueTreeWithDeps) re-sorts roots unconditionally by
// compareIssuesByPriority, discarding the requested order. Fix: re-apply the
// requested --sort to the tree ROOTS in displayPrettyListWithBlocked, falling
// back to the priority default only when sortBy=="".
//
// This exercises the real render path (displayPrettyListWithBlocked ->
// buildIssueTreeWithDeps -> printPrettyTree) end-to-end and asserts the ROOT
// line order, which a pure sortIssues() unit test could not (the bug lived in
// the renderer's re-sort, not in sortIssues).
//
// Shape: 3 flat roots (no deps) whose priority order and title order DIFFER, so
// a rendered order can only match one of them:
//
//	ID      Priority   Title
//	z-hi    0          "Charlie"
//	z-mid   1          "Alpha"
//	z-lo    2          "Bravo"
//
// priority order: z-hi, z-mid, z-lo   (P0, P1, P2)
// title order:    z-mid(Alpha), z-lo(Bravo), z-hi(Charlie)
//
// Mutation proof: delete the `sortIssues(roots, sortBy, reverse)` line in
// displayPrettyListWithBlocked and the --sort-title assertion fails (roots stay
// in priority order) — RED.
func TestDisplayPrettyList_HonorsSortInTreeView_z3sku(t *testing.T) {
	hi := &types.Issue{ID: "z-hi", Title: "Charlie", Status: types.StatusOpen, Priority: 0}
	mid := &types.Issue{ID: "z-mid", Title: "Alpha", Status: types.StatusOpen, Priority: 1}
	lo := &types.Issue{ID: "z-lo", Title: "Bravo", Status: types.StatusOpen, Priority: 2}
	// Pass in priority order to make the bug (renderer forcing priority) invisible
	// unless the requested sort is genuinely re-applied.
	issues := []*types.Issue{hi, mid, lo}

	rootOrder := func(out string) []string {
		var order []string
		for _, ln := range strings.Split(out, "\n") {
			for _, id := range []string{"z-hi", "z-mid", "z-lo"} {
				// Root lines render "<icon> <id> ...": match the id as its own
				// token so a title/annotation mention can't false-match.
				if regexp.MustCompile(`(^|\s)` + regexp.QuoteMeta(id) + `\s`).MatchString(ln) {
					order = append(order, id)
				}
			}
		}
		return order
	}

	// --sort title: roots must render in title order (Alpha, Bravo, Charlie).
	outTitle := capturePrettyFooter(t, func() {
		displayPrettyListWithBlocked(issues, false, nil, false, "", nil, "title", false)
	})
	gotTitle := rootOrder(outTitle)
	wantTitle := []string{"z-mid", "z-lo", "z-hi"}
	if strings.Join(gotTitle, ",") != strings.Join(wantTitle, ",") {
		t.Errorf("--sort title root order = %v, want %v (title order) — the tree view must honor --sort, not force priority (beads-z3sku)\nrendered:\n%s", gotTitle, wantTitle, outTitle)
	}

	// --sort id (reverse): roots must render in reverse-id order.
	outID := capturePrettyFooter(t, func() {
		displayPrettyListWithBlocked(issues, false, nil, false, "", nil, "id", true)
	})
	gotID := rootOrder(outID)
	wantID := []string{"z-mid", "z-lo", "z-hi"} // reverse natural id: z-mid > z-lo > z-hi
	if strings.Join(gotID, ",") != strings.Join(wantID, ",") {
		t.Errorf("--sort id --reverse root order = %v, want %v (beads-z3sku)\nrendered:\n%s", gotID, wantID, outID)
	}

	// Control: sortBy=="" preserves the stable priority default (P0,P1,P2).
	outDefault := capturePrettyFooter(t, func() {
		displayPrettyListWithBlocked(issues, false, nil, false, "", nil, "", false)
	})
	gotDefault := rootOrder(outDefault)
	wantDefault := []string{"z-hi", "z-mid", "z-lo"}
	if strings.Join(gotDefault, ",") != strings.Join(wantDefault, ",") {
		t.Errorf("default (sortBy=\"\") root order = %v, want %v (priority default must be preserved) (beads-z3sku)\nrendered:\n%s", gotDefault, wantDefault, outDefault)
	}
}
