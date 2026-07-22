//go:build cgo

package main

import (
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

// TestPourDBProto_StripsTemplateLabel is the teeth for beads-hqa6b: mol pour /
// bond via a DB-persisted proto leaked the "template" (MoleculeLabel) onto the
// live clone-ROOT. cloneSubgraph copies Labels: oldIssue.Labels verbatim; on the
// FORMULA path the subgraph is freshly parsed (no "template" label) so the poured
// root is clean, but on the DB-PROTO path loadTemplateSubgraph reads a persisted
// proto bead that carries the "template" label → the label copies onto the live
// instance root → the root is a proto per isProto() → HIDDEN from default
// `bd list --all` (only under --include-templates). The FORMULA-path visibility
// diverged from the DB-proto path.
//
// The fix strips ONLY the MoleculeLabel from clones in cloneSubgraph, matching
// the formula path by construction. It must NOT regress legit user step-labels
// (commit f990671d0 deliberately carries user step labels — those must survive).
//
// MUTATION-VERIFY: revert the strip in cloneSubgraph (copy oldIssue.Labels
// verbatim) → the "clone-root drops template" subtest FAILS (the root re-carries
// "template" and isProto() reports it a proto → invisible in list --all).
func TestPourDBProto_StripsTemplateLabel(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	ctx := t.Context()
	s, err := embeddeddolt.Open(ctx, t.TempDir(), "beads", "main")
	if err != nil {
		t.Fatalf("embeddeddolt.Open failed: %v", err)
	}
	defer s.Close()
	if err := s.SetConfig(ctx, "issue_prefix", "test"); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}

	// Build a DB-persisted proto: a root epic carrying the "template"
	// (MoleculeLabel) — how a `cook --persist` proto lives in the DB — plus a
	// legit user step-label on the root, and one child likewise carrying both
	// the template label and a user step-label. This is the shape
	// loadTemplateSubgraph reads back (unlike the formula path which parses a
	// fresh subgraph with no template label).
	const userRootLabel = "phase:build"
	const userChildLabel = "worker"

	root := &types.Issue{
		ID:        "test-proto",
		Title:     "Proto Root",
		IssueType: types.TypeEpic,
		Status:    types.StatusOpen,
	}
	if err := s.CreateIssue(ctx, root, "test"); err != nil {
		t.Fatalf("CreateIssue(root) failed: %v", err)
	}
	if err := s.AddLabel(ctx, root.ID, MoleculeLabel, "test"); err != nil {
		t.Fatalf("AddLabel(root, MoleculeLabel) failed: %v", err)
	}
	if err := s.AddLabel(ctx, root.ID, userRootLabel, "test"); err != nil {
		t.Fatalf("AddLabel(root, user) failed: %v", err)
	}

	child := &types.Issue{
		ID:        "test-proto.1",
		Title:     "Proto Child",
		IssueType: types.TypeTask,
		Status:    types.StatusOpen,
	}
	if err := s.CreateIssue(ctx, child, "test"); err != nil {
		t.Fatalf("CreateIssue(child) failed: %v", err)
	}
	if err := s.AddDependency(ctx, &types.Dependency{
		IssueID:     child.ID,
		DependsOnID: root.ID,
		Type:        types.DepParentChild,
	}, "test"); err != nil {
		t.Fatalf("AddDependency(child->root) failed: %v", err)
	}
	if err := s.AddLabel(ctx, child.ID, MoleculeLabel, "test"); err != nil {
		t.Fatalf("AddLabel(child, MoleculeLabel) failed: %v", err)
	}
	if err := s.AddLabel(ctx, child.ID, userChildLabel, "test"); err != nil {
		t.Fatalf("AddLabel(child, user) failed: %v", err)
	}

	// Load the proto FROM THE DB (the DB-proto path) and pour it (ephemeral=false).
	subgraph, err := loadTemplateSubgraph(ctx, s, root.ID)
	if err != nil {
		t.Fatalf("loadTemplateSubgraph failed: %v", err)
	}
	// Sanity: the loaded proto root really does carry the template label (this is
	// the leak source; if this ever changes the test is moot).
	if !isProto(subgraph.Root) {
		t.Fatalf("precondition: DB proto root must carry the template label")
	}

	result, err := spawnMolecule(ctx, s, subgraph, nil, "", "test", false, types.IDPrefixMol)
	if err != nil {
		t.Fatalf("spawnMolecule failed: %v", err)
	}

	newRootID, ok := result.IDMapping[root.ID]
	if !ok {
		t.Fatalf("result.IDMapping missing entry for root %s; got %v", root.ID, result.IDMapping)
	}
	newChildID, ok := result.IDMapping[child.ID]
	if !ok {
		t.Fatalf("result.IDMapping missing entry for child %s; got %v", child.ID, result.IDMapping)
	}

	labelSet := func(id string) map[string]bool {
		labels, gerr := s.GetLabels(ctx, id)
		if gerr != nil {
			t.Fatalf("GetLabels(%s) failed: %v", id, gerr)
		}
		set := make(map[string]bool, len(labels))
		for _, l := range labels {
			set[l] = true
		}
		return set
	}

	t.Run("poured clone-root drops the template label (visible in list --all)", func(t *testing.T) {
		got := labelSet(newRootID)
		if got[MoleculeLabel] {
			t.Errorf("poured clone-root %s must NOT carry %q — it leaks the DB proto's template label, hiding the live instance from `bd list --all`", newRootID, MoleculeLabel)
		}
		// Re-read the persisted issue and confirm isProto() reports it a real
		// instance, not a proto (the exact predicate `bd list --all` uses to hide).
		got2, gerr := s.GetIssue(ctx, newRootID)
		if gerr != nil {
			t.Fatalf("GetIssue(%s) failed: %v", newRootID, gerr)
		}
		got2.Labels = labelsSlice(got)
		if isProto(got2) {
			t.Errorf("poured clone-root %s is still classified as a proto → hidden from default list", newRootID)
		}
	})

	t.Run("poured clone-root preserves the legit user step-label", func(t *testing.T) {
		got := labelSet(newRootID)
		if !got[userRootLabel] {
			t.Errorf("poured clone-root %s dropped the legit user step-label %q; labels=%v — must not regress the f990671d0 step-label carry", newRootID, userRootLabel, got)
		}
	})

	t.Run("poured clone-child drops template but keeps its user step-label", func(t *testing.T) {
		got := labelSet(newChildID)
		if got[MoleculeLabel] {
			t.Errorf("poured clone-child %s must NOT carry %q", newChildID, MoleculeLabel)
		}
		if !got[userChildLabel] {
			t.Errorf("poured clone-child %s dropped the legit user step-label %q; labels=%v", newChildID, userChildLabel, got)
		}
	})
}

// labelsSlice converts a label set to a slice for isProto() re-evaluation.
func labelsSlice(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	return out
}
