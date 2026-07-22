//go:build cgo

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/formula"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

// TestWispCreateNextStepsHint_ap2jc is the teeth for beads-ap2jc: `bd mol wisp`
// printed a Next-steps hint `bd close <root>.<step>`, but a plain wisp create
// (no --child-ref) does NOT set opts.ParentID, so generateBondedID is skipped and
// the children get RANDOM-SUFFIX IDs (root h-wisp-w38 → child h-wisp-6cg), NOT
// dotted <root>.<step> IDs. The hinted `bd close h-wisp-w38.alpha` always failed
// RC=1 ("no issue found"). The fix prints the REAL minted child IDs (already in
// result.IDMapping at print time) so the suggested close command resolves.
//
// MUTATION-VERIFY: revert printWispCreateNextSteps to emit the dotted
// "%s.<step>" template → the "hint references real, resolvable child IDs" assert
// FAILS (the output no longer contains any minted child ID and re-introduces the
// broken dotted reference).
func TestWispCreateNextStepsHint_ap2jc(t *testing.T) {
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

	f := &formula.Formula{
		Formula: "wf",
		Version: 1,
		Type:    formula.TypeWorkflow,
		Steps: []*formula.Step{
			{ID: "alpha", Title: "Alpha step"},
			{ID: "beta", Title: "Beta step"},
		},
	}
	subgraph, err := cookFormulaToSubgraph(f, "wf")
	if err != nil {
		t.Fatalf("cookFormulaToSubgraph failed: %v", err)
	}

	// Plain wisp create: Ephemeral, wisp prefix, NO ParentID → random-suffix
	// child IDs (the exact path that broke the dotted hint).
	result, err := spawnMoleculeWithOptions(ctx, s, subgraph, CloneOptions{
		Actor:     "test",
		Ephemeral: true,
		Prefix:    types.IDPrefixWisp,
	})
	if err != nil {
		t.Fatalf("spawnMoleculeWithOptions failed: %v", err)
	}

	// Collect the real minted child IDs (everything in the mapping except root).
	rootOld := subgraph.Root.ID
	var childIDs []string
	for oldID, newID := range result.IDMapping {
		if oldID == rootOld {
			continue
		}
		childIDs = append(childIDs, newID)
	}
	if len(childIDs) == 0 {
		t.Fatalf("expected at least one minted child ID; got mapping %v", result.IDMapping)
	}

	var buf bytes.Buffer
	printWispCreateNextSteps(&buf, result, subgraph, false)
	out := buf.String()

	t.Run("hint references real, resolvable child IDs (not the broken dotted template)", func(t *testing.T) {
		for _, id := range childIDs {
			if !strings.Contains(out, id) {
				t.Errorf("Next-steps hint must list the real minted child ID %q; got:\n%s", id, out)
			}
			// The listed ID must actually resolve (the whole point — the old
			// dotted hint pointed at an ID that never existed).
			issue, gerr := s.GetIssue(ctx, id)
			if gerr != nil || issue == nil {
				t.Errorf("hinted child ID %q does not resolve in the store: %v", id, gerr)
			}
		}
		// The broken dotted-template reference must be gone.
		if strings.Contains(out, result.NewEpicID+".<step>") || strings.Contains(out, "<root>.<step>") {
			t.Errorf("Next-steps hint still emits the broken dotted close template; got:\n%s", out)
		}
	})

	t.Run("root-based squash/burn hints stay correct", func(t *testing.T) {
		if !strings.Contains(out, "bd mol squash "+result.NewEpicID) {
			t.Errorf("squash hint should reference the wisp root %s; got:\n%s", result.NewEpicID, out)
		}
		if !strings.Contains(out, "bd mol burn "+result.NewEpicID) {
			t.Errorf("burn hint should reference the wisp root %s; got:\n%s", result.NewEpicID, out)
		}
	})

	t.Run("root-only wisp (no children) hints closing the root itself", func(t *testing.T) {
		rootOnlyResult := &InstantiateResult{
			NewEpicID: result.NewEpicID,
			IDMapping: map[string]string{rootOld: result.NewEpicID},
			Created:   1,
		}
		var b2 bytes.Buffer
		printWispCreateNextSteps(&b2, rootOnlyResult, subgraph, true)
		o2 := b2.String()
		if !strings.Contains(o2, "bd close "+result.NewEpicID) {
			t.Errorf("root-only wisp should hint closing the root %s; got:\n%s", result.NewEpicID, o2)
		}
		if strings.Contains(o2, ".<step>") {
			t.Errorf("root-only wisp hint must not emit the dotted template; got:\n%s", o2)
		}
	})
}
