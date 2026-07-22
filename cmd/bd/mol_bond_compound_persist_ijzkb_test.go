//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-ijzkb: `mol bond` succeeds but the compound lineage (types.Issue.BondedFrom)
// was never persisted — there was no bonded_from storage column, so IsCompound()
// always returned false after a reload and `mol show` never rendered Compound.
// migration 0055 adds the column and both insert/scan sites persist it.
//
// These are ROUND-TRIP teeth (bond → reload from the store → assert), NOT
// pure-marshal tests: they exercise the real DoltStore insert + scan path so a
// regression in the SQL column list, the ScanIssueFrom dest ordering, or the
// mol+mol UpdateIssue populate is caught. Mutation-verify: revert the
// persistence (drop bonded_from from the INSERT/SELECT column lists, or restore
// the "not yet supported" TODO at mol_bond.go) and both subtests go RED.

func TestMolBondPersistsCompoundLineage_ijzkb(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	t.Run("proto_plus_proto", func(t *testing.T) {
		// Two protos (template-labelled) bond into a NEW compound root that
		// records both as its BondedFrom sources.
		protoA := &types.Issue{
			Title: "Proto A", Status: types.StatusOpen, Priority: 1,
			IssueType: types.TypeEpic, CreatedAt: time.Now(),
		}
		protoB := &types.Issue{
			Title: "Proto B", Status: types.StatusOpen, Priority: 1,
			IssueType: types.TypeEpic, CreatedAt: time.Now(),
		}
		if err := s.CreateIssue(ctx, protoA, "test"); err != nil {
			t.Fatalf("create protoA: %v", err)
		}
		if err := s.CreateIssue(ctx, protoB, "test"); err != nil {
			t.Fatalf("create protoB: %v", err)
		}
		// Mark them as protos (the template label) so the bond is proto+proto.
		if err := s.AddLabel(ctx, protoA.ID, MoleculeLabel, "test"); err != nil {
			t.Fatalf("label protoA: %v", err)
		}
		if err := s.AddLabel(ctx, protoB.ID, MoleculeLabel, "test"); err != nil {
			t.Fatalf("label protoB: %v", err)
		}

		// DB-resident protos (not formula-cooked): wrap each in a subgraph whose
		// Root is the stored proto and pass cookedA/cookedB=false, so
		// bondProtoProto skips materialization and bonds the existing rows.
		res, err := bondProtoProto(ctx, s,
			&TemplateSubgraph{Root: protoA}, &TemplateSubgraph{Root: protoB},
			false, false, types.BondTypeParallel, "", "test")
		if err != nil {
			t.Fatalf("bondProtoProto: %v", err)
		}

		// Reload the compound root FROM THE STORE — this is the assertion that
		// caught the gap: without persistence the reloaded BondedFrom is empty.
		compound, err := s.GetIssue(ctx, res.ResultID)
		if err != nil {
			t.Fatalf("reload compound %s: %v", res.ResultID, err)
		}
		if !compound.IsCompound() {
			t.Fatalf("reloaded compound %s: IsCompound()=false, want true (BondedFrom=%v)",
				compound.ID, compound.BondedFrom)
		}
		if len(compound.BondedFrom) != 2 {
			t.Fatalf("reloaded compound BondedFrom len=%d, want 2: %v",
				len(compound.BondedFrom), compound.BondedFrom)
		}
		gotSources := map[string]bool{}
		for _, ref := range compound.BondedFrom {
			gotSources[ref.SourceID] = true
			if ref.BondType != types.BondTypeParallel {
				t.Errorf("BondedFrom ref %s: BondType=%q, want %q", ref.SourceID, ref.BondType, types.BondTypeParallel)
			}
		}
		if !gotSources[protoA.ID] || !gotSources[protoB.ID] {
			t.Errorf("reloaded BondedFrom sources = %v, want both %s and %s", gotSources, protoA.ID, protoB.ID)
		}
	})

	t.Run("mol_plus_mol", func(t *testing.T) {
		// Two plain molecules (no template label): molA becomes the compound and
		// must record molB as its BondedFrom source (mol_bond.go:579, previously
		// a "not yet supported" TODO).
		molA := &types.Issue{
			Title: "Mol A", Status: types.StatusOpen, Priority: 1,
			IssueType: types.TypeTask, CreatedAt: time.Now(),
		}
		molB := &types.Issue{
			Title: "Mol B", Status: types.StatusOpen, Priority: 1,
			IssueType: types.TypeTask, CreatedAt: time.Now(),
		}
		if err := s.CreateIssue(ctx, molA, "test"); err != nil {
			t.Fatalf("create molA: %v", err)
		}
		if err := s.CreateIssue(ctx, molB, "test"); err != nil {
			t.Fatalf("create molB: %v", err)
		}

		res, err := bondMolMol(ctx, s, molA, molB, types.BondTypeSequential, "test")
		if err != nil {
			t.Fatalf("bondMolMol: %v", err)
		}
		if res.ResultID != molA.ID {
			t.Fatalf("bondMolMol ResultID=%s, want molA %s", res.ResultID, molA.ID)
		}

		// Reload molA FROM THE STORE and assert the lineage survived.
		compound, err := s.GetIssue(ctx, molA.ID)
		if err != nil {
			t.Fatalf("reload molA %s: %v", molA.ID, err)
		}
		if !compound.IsCompound() {
			t.Fatalf("reloaded molA %s: IsCompound()=false, want true (BondedFrom=%v)",
				compound.ID, compound.BondedFrom)
		}
		if len(compound.BondedFrom) != 1 {
			t.Fatalf("reloaded molA BondedFrom len=%d, want 1: %v",
				len(compound.BondedFrom), compound.BondedFrom)
		}
		ref := compound.BondedFrom[0]
		if ref.SourceID != molB.ID {
			t.Errorf("reloaded molA BondedFrom source = %q, want molB %q", ref.SourceID, molB.ID)
		}
		if ref.BondType != types.BondTypeSequential {
			t.Errorf("reloaded molA BondedFrom BondType = %q, want %q", ref.BondType, types.BondTypeSequential)
		}
	})
}
