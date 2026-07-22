//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateIssueFromFormValues_DepsClosedParentGuard covers beads-o8h79: the
// create-FORM Dependencies-FIELD straggler of the closed-parent close-guard
// family. beads-3jdex guarded the form's --parent leg (ParentID), and p1p9n
// guarded `bd create --deps parent-child:<id>` + markdown, but the form's
// Dependencies field (fv.Dependencies, e.g. a comma-separated
// "parent-child:<id>" typed into the interactive Dependencies form field) fed a
// SEPARATE loop that appended the parent-child edge into pendingEdges and
// applied it in the tx with NO closed-parent guard. So leaving --parent empty
// and instead typing parent-child:<closed-epic-or-molecule> into Dependencies
// recreated the forbidden "closed parent with an open child" state at rc=0.
//
// The fix mirrors 3jdex/p1p9n: in the Dependencies loop, when
// depType==DepParentChild && !fv.Force, look up the target and refuse if it's a
// closed auto-closing parent (epic|molecule|ephemeral). --force overrides; the
// read fails OPEN (err==nil gate) so a lookup miss never newly rejects a create
// the loop would otherwise have accepted.
//
// Mutation-verify: remove the guard block in create_form.go and the
// ClosedEpic/ClosedMolecule refuse subtests go RED (the form creates the open
// child under the closed parent and returns nil).
func TestCreateIssueFromFormValues_DepsClosedParentGuard(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	// makeParent creates a parent of the given form type and optionally closes it.
	makeParent := func(t *testing.T, title, issueType string, closed bool) string {
		t.Helper()
		parent, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title:     title,
			Priority:  1,
			IssueType: issueType,
		}, "test")
		if err != nil {
			t.Fatalf("failed to create %s parent: %v", issueType, err)
		}
		if closed {
			if err := s.CloseIssue(ctx, parent.ID, "done", "test", ""); err != nil {
				t.Fatalf("failed to close parent %s: %v", parent.ID, err)
			}
		}
		return parent.ID
	}

	// REFUSE cases: a parent-child:<closed-auto-closing-parent> spec supplied via
	// the Dependencies FIELD (not --parent) must fire the same guard.
	refuseCases := []struct {
		name       string
		parentType string
	}{
		{"ClosedEpicRefused", "epic"},
		{"ClosedMoleculeRefused", "molecule"},
	}
	for _, tc := range refuseCases {
		t.Run(tc.name, func(t *testing.T) {
			pid := makeParent(t, tc.name+" parent", tc.parentType, true)
			_, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
				Title:        "deps-field child under closed " + tc.parentType,
				Priority:     2,
				IssueType:    "task",
				Dependencies: []string{"parent-child:" + pid},
			}, "test")
			if err == nil {
				t.Fatalf("beads-o8h79: expected refusal creating a child under closed %s "+
					"via the Dependencies field, got nil", tc.parentType)
			}
			if !strings.Contains(err.Error(), "closed parent") {
				t.Errorf("beads-o8h79: expected 'closed parent' guard error, got: %v", err)
			}
		})
	}

	// --force overrides the guard on the Dependencies-field axis too.
	t.Run("ForceOverridesClosedParent", func(t *testing.T) {
		pid := makeParent(t, "force deps parent", "epic", true)
		if _, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title:        "forced deps-field child",
			Priority:     2,
			IssueType:    "task",
			Dependencies: []string{"parent-child:" + pid},
			Force:        true,
		}, "test"); err != nil {
			t.Fatalf("beads-o8h79: --force should override the closed-parent guard on the "+
				"Dependencies field, got: %v", err)
		}
	})

	// CONTROL cases (guard must NOT over-fire): open auto-closing parent, and a
	// closed NON-auto-closing (task) parent, both allow the parent-child dep.
	t.Run("OpenEpicParentAllowed", func(t *testing.T) {
		pid := makeParent(t, "open deps epic", "epic", false)
		if _, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title:        "deps-field child under open epic",
			Priority:     2,
			IssueType:    "task",
			Dependencies: []string{"parent-child:" + pid},
		}, "test"); err != nil {
			t.Fatalf("beads-o8h79: child under OPEN epic via Dependencies field must succeed, got: %v", err)
		}
	})
	// CONTROL: a closed NON-auto-closing (task) parent still allows a
	// parent-child edge — proves the guard is scoped to auto-closing parent
	// types (epic|molecule|ephemeral), not "any closed parent".
	t.Run("ClosedTaskParentAllowed", func(t *testing.T) {
		pid := makeParent(t, "closed deps task", "task", true)
		if _, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title:        "deps-field child under closed task",
			Priority:     2,
			IssueType:    "task",
			Dependencies: []string{"parent-child:" + pid},
		}, "test"); err != nil {
			t.Fatalf("beads-o8h79: child under closed NON-auto-closing (task) parent via "+
				"Dependencies field must succeed, got: %v", err)
		}
	})
}
