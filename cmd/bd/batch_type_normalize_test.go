//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestBatch_CreateNormalizesTypeAliases verifies that `bd batch create <type> ...`
// expands documented type aliases the same way `bd create -t <type>` does
// (beads-dr70). Before the fix batch.go used the RAW flag value
// (types.IssueType(op.args[0])) with no .Normalize(), so a documented alias like
// "feat"/"mol" was stored raw, failed IsValid(), and the whole batch tx was
// REJECTED — an asymmetry with `bd create`, which normalizes feat->feature.
func TestBatch_CreateNormalizesTypeAliases(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbn")
	ctx := context.Background()

	// Each alias -> the canonical IssueType it must be stored as. These mirror
	// types.IssueTypeAliases, which is the same table bd create's Normalize uses.
	cases := []struct {
		alias string
		want  types.IssueType
	}{
		{"feat", types.TypeFeature},
		{"enhancement", types.TypeFeature},
		{"mol", types.TypeMolecule},
		{"dec", types.TypeDecision},
		{"investigation", types.TypeSpike},
		// mixed-case canonical name folds too
		{"BUG", types.TypeBug},
	}

	for _, c := range cases {
		t.Run(c.alias, func(t *testing.T) {
			// The create op's title is args[2:], so a single-word title works.
			script := "create " + c.alias + " 2 title-for-" + c.alias + "\n"
			if err := runBatchScriptInTx(t, ctx, st, script); err != nil {
				t.Fatalf("batch create %q should succeed (alias should normalize): %v", c.alias, err)
			}
			// Find the created issue by its title and check the stored type.
			issues, err := st.SearchIssues(ctx, "", types.IssueFilter{})
			if err != nil {
				t.Fatalf("SearchIssues: %v", err)
			}
			var got *types.Issue
			for i := range issues {
				if issues[i].Title == "title-for-"+c.alias {
					got = issues[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("created issue for alias %q not found", c.alias)
			}
			if got.IssueType != c.want {
				t.Errorf("batch create %q stored IssueType %q, want %q (should normalize like bd create)",
					c.alias, got.IssueType, c.want)
			}
		})
	}
}

// TestBatch_CreateStillRejectsInvalidType verifies the Normalize fix does NOT
// weaken validation: a genuinely unknown type (not an alias, not a canonical
// name, not a configured custom type) still hard-errors and the whole batch
// rolls back — Normalize maps it to itself, then the storage-layer IsValid
// check rejects it, exactly as `bd create` does.
//
// (The orchestrator pseudo-type "mr"->"merge-request" is intentionally NOT
// asserted here: the test store whitelists "merge-request" as a custom type
// (test_helpers_test.go types.custom), so it validates in this harness; its
// production rejection is covered by the pure-type unit tests in
// internal/types, not this storage-backed batch test.)
func TestBatch_CreateStillRejectsInvalidType(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbi")
	ctx := context.Background()

	script := "create frobnicate 2 should-not-persist\n"
	err := runBatchScriptInTx(t, ctx, st, script)
	if err == nil {
		t.Fatal("batch create \"frobnicate\" should be rejected (unknown type)")
	}
	// Nothing should have persisted (atomic rollback).
	issues, lerr := st.SearchIssues(ctx, "", types.IssueFilter{})
	if lerr != nil {
		t.Fatalf("SearchIssues: %v", lerr)
	}
	for _, is := range issues {
		if is.Title == "should-not-persist" {
			t.Error("invalid-type batch create persisted an issue despite error")
		}
	}
}
