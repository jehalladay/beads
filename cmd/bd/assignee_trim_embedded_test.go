//go:build cgo

package main

import (
	"testing"
)

// TestAssigneeTrimRoundTrip is the end-to-end regression for beads-llzt: the
// create and update write paths stored the --assignee flag VERBATIM, while the
// read/filter side matches case-insensitively but never trims. So a padded
// `-a "  alice  "` produced an issue that `bd list --assignee alice` could not
// find — silently orphaning the work from the assignee meant to pull it (the
// exact `bd ready --assignee $GT_ROLE` never-idle path).
//
// This proves the ROUTING (create_input.go / update.go actually call
// normalizeAssignee), which the unit test on normalizeAssignee alone cannot: it
// fails on the pre-llzt code where those sites stored the flag verbatim.
func TestAssigneeTrimRoundTrip(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "at")

	// create -a with surrounding whitespace must store the trimmed form.
	created := bdCreate(t, bd, dir, "padded on create", "--type", "task", "--assignee", "  alice  ")
	if created.Assignee != "alice" {
		t.Fatalf("create stored assignee = %q, want %q", created.Assignee, "alice")
	}
	// It must be findable by the canonical (unpadded) name.
	got := bdListJSON(t, bd, dir, "--assignee", "alice")
	if len(got) != 1 || got[0].ID != created.ID {
		t.Fatalf("bd list --assignee alice = %d issues, want 1 (the created issue); create path did not trim", len(got))
	}

	// update --assignee with whitespace must likewise store trimmed.
	other := bdCreate(t, bd, dir, "assigned on update", "--type", "task")
	bdUpdate(t, bd, dir, other.ID, "--assignee", "\tbob\n")
	gotBob := bdListJSON(t, bd, dir, "--assignee", "bob")
	if len(gotBob) != 1 || gotBob[0].ID != other.ID {
		t.Fatalf("bd list --assignee bob = %d issues, want 1; update path did not trim", len(gotBob))
	}

	// A whitespace-only assignee folds to unassigned (empty), not a padded
	// literal that no assignee query could ever match.
	unassigned := bdCreate(t, bd, dir, "whitespace-only assignee", "--type", "task", "--assignee", "   ")
	if unassigned.Assignee != "" {
		t.Fatalf("create -a '   ' stored assignee = %q, want empty (unassigned)", unassigned.Assignee)
	}
}
