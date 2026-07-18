//go:build cgo

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/types"
)

// wispTestProto creates a proto epic with the given number of parent-child
// children in the store, returning the root proto's ID. Used to exercise
// wisp DAG fanout.
func wispTestProto(t *testing.T, ctx context.Context, s *dolt.DoltStore, numChildren int) string {
	t.Helper()
	root := &types.Issue{
		Title:     "Proto Root",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeEpic,
		Labels:    []string{MoleculeLabel},
	}
	if err := s.CreateIssue(ctx, root, "test"); err != nil {
		t.Fatalf("Failed to create proto root: %v", err)
	}
	for i := 1; i <= numChildren; i++ {
		child := &types.Issue{
			Title:     fmt.Sprintf("Step %d", i),
			Status:    types.StatusOpen,
			Priority:  2,
			IssueType: types.TypeTask,
		}
		if err := s.CreateIssue(ctx, child, "test"); err != nil {
			t.Fatalf("Failed to create step %d: %v", i, err)
		}
		if err := s.AddDependency(ctx, &types.Dependency{
			IssueID:     child.ID,
			DependsOnID: root.ID,
			Type:        types.DepParentChild,
		}, "test"); err != nil {
			t.Fatalf("Failed to add dependency for step %d: %v", i, err)
		}
	}
	return root.ID
}

// makeWispTestCmd builds a cobra.Command with the same flag schema as the
// real `bd mol wisp` command, with the given flag values set as defaults
// (so runWispCreate reads them back without needing arg parsing).
func makeWispTestCmd(rootOnly, dryRun bool) *cobra.Command {
	c := &cobra.Command{Use: "wisp"}
	c.Flags().StringArray("var", []string{}, "")
	c.Flags().Bool("dry-run", dryRun, "")
	c.Flags().Bool("root-only", rootOnly, "")
	return c
}

// countEphemeral returns the number of issues in the store with Ephemeral=true.
func countEphemeral(t *testing.T, ctx context.Context, s *dolt.DoltStore) int {
	t.Helper()
	tru := true
	results, err := s.SearchIssues(ctx, "", types.IssueFilter{Ephemeral: &tru})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	return len(results)
}

// withWispTestGlobals saves and restores the package-level store/rootCtx/actor
// globals around a test, since runWispCreate reads them directly.
func withWispTestGlobals(t *testing.T, s *dolt.DoltStore, ctx context.Context) {
	t.Helper()
	oldStore, oldCtx, oldActor := store, rootCtx, actor
	t.Cleanup(func() { store, rootCtx, actor = oldStore, oldCtx, oldActor })
	store, rootCtx, actor = s, ctx, "test"
}

// TestWispCreateMaterializesChildDAG is the regression test for GH#3872.
// Before the fix, wisps were silently forced to root-only unless the
// formula set pour=true, making --root-only a no-op flag and breaking
// ephemeral lifecycle testing of multi-step formulas. After the fix,
// `bd mol wisp <proto>` materializes the full child DAG by default,
// just marked Ephemeral=true so it doesn't sync via git.
func TestWispCreateMaterializesChildDAG(t *testing.T) {
	ctx := context.Background()
	s := newTestStoreWithPrefix(t, filepath.Join(t.TempDir(), "test.db"), "test")

	rootID := wispTestProto(t, ctx, s, 2)

	withWispTestGlobals(t, s, ctx)

	_ = captureStdout(t, func() error {
		runWispCreate(makeWispTestCmd(false, false), []string{rootID})
		return nil
	})

	// Proto root + 2 children = 3 source issues, all materialized as wisp
	// copies with Ephemeral=true. The original proto issues are persistent
	// (Ephemeral=false), so counting ephemeral issues gives us exactly the
	// wisp set.
	if got := countEphemeral(t, ctx, s); got != 3 {
		t.Errorf("expected 3 ephemeral wisp issues (root + 2 children), got %d", got)
	}
}

// TestWispCreateRootOnly verifies that --root-only opts out of child
// materialization while still creating the root as ephemeral. Before the
// GH#3872 fix this was the silent default for all vapor formulas; after
// the fix it must be an explicit opt-in via the flag.
func TestWispCreateRootOnly(t *testing.T) {
	ctx := context.Background()
	s := newTestStoreWithPrefix(t, filepath.Join(t.TempDir(), "test.db"), "test")

	rootID := wispTestProto(t, ctx, s, 2)

	withWispTestGlobals(t, s, ctx)

	_ = captureStdout(t, func() error {
		runWispCreate(makeWispTestCmd(true, false), []string{rootID})
		return nil
	})

	if got := countEphemeral(t, ctx, s); got != 1 {
		t.Errorf("expected 1 ephemeral wisp issue (root only), got %d", got)
	}
}

// TestWispCreateDryRunFanoutMessage verifies the dry-run printout reflects
// full DAG materialization by default and switches to "root only" wording
// only under --root-only. Catches regressions in user-facing messaging.
func TestWispCreateDryRunFanoutMessage(t *testing.T) {
	ctx := context.Background()
	s := newTestStoreWithPrefix(t, filepath.Join(t.TempDir(), "test.db"), "test")

	rootID := wispTestProto(t, ctx, s, 2)

	withWispTestGlobals(t, s, ctx)

	t.Run("default fans out", func(t *testing.T) {
		output := captureStdout(t, func() error {
			runWispCreate(makeWispTestCmd(false, true), []string{rootID})
			return nil
		})
		if !strings.Contains(output, "would create wisp with 3 issues") {
			t.Errorf("dry-run should mention 3 issues (root + 2 children), got:\n%s", output)
		}
		if strings.Contains(output, "root only") {
			t.Errorf("dry-run without --root-only should NOT say 'root only', got:\n%s", output)
		}
	})

	t.Run("root-only shows opt-out wording", func(t *testing.T) {
		output := captureStdout(t, func() error {
			runWispCreate(makeWispTestCmd(true, true), []string{rootID})
			return nil
		})
		if !strings.Contains(output, "1 issue (root only)") {
			t.Errorf("dry-run with --root-only should mention 1 root issue, got:\n%s", output)
		}
		if !strings.Contains(output, "--root-only") {
			t.Errorf("skip message should reference --root-only flag, got:\n%s", output)
		}
	})
}

// makeWispGCTestCmd builds a cobra.Command with the same flag schema as the
// real `bd mol wisp gc` command, with the given flag values set as defaults so
// runWispGC reads them back without arg parsing.
func makeWispGCTestCmd(closed, force bool, excludeType []string) *cobra.Command {
	c := &cobra.Command{Use: "gc"}
	c.Flags().Bool("dry-run", false, "")
	c.Flags().String("age", "1h", "")
	c.Flags().Bool("all", false, "")
	c.Flags().Bool("closed", closed, "")
	c.Flags().BoolP("force", "f", force, "")
	c.Flags().StringSlice("exclude-type", excludeType, "")
	return c
}

// countEphemeralOfType returns the number of ephemeral issues of the given
// canonical type in the store.
func countEphemeralOfType(t *testing.T, ctx context.Context, s *dolt.DoltStore, it types.IssueType) int {
	t.Helper()
	tru := true
	results, err := s.SearchIssues(ctx, "", types.IssueFilter{Ephemeral: &tru})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	n := 0
	for _, r := range results {
		if r.IssueType == it {
			n++
		}
	}
	return n
}

// TestWispGCExcludeTypeAliasProtectsMolecules is the end-to-end beads-asls
// regression test. The documented protect-command `bd mol wisp gc --closed
// --force --exclude-type mol` must actually protect molecule wisps from
// deletion. Before the fix, the exclude filter was built from the raw flag
// (`IssueType("mol")`), which never matched stored canonical "molecule" rows,
// so the protection failed OPEN and deleted the very molecules the user asked
// to keep — a silent destructive bug (molecule is NOT infra-protected, so
// --exclude-type is its ONLY protection).
func TestWispGCExcludeTypeAliasProtectsMolecules(t *testing.T) {
	ctx := context.Background()
	s := newTestStoreWithPrefix(t, filepath.Join(t.TempDir(), "test.db"), "test")
	withWispTestGlobals(t, s, ctx)

	// A closed ephemeral molecule (the thing --exclude-type mol must protect)
	// and a closed ephemeral task (an unprotected wisp that SHOULD be deleted,
	// proving GC still runs and the exclude is narrow).
	mol := &types.Issue{Title: "keepme molecule", Status: types.StatusClosed, Priority: 2, IssueType: types.TypeMolecule, Ephemeral: true}
	task := &types.Issue{Title: "purge task", Status: types.StatusClosed, Priority: 2, IssueType: types.TypeTask, Ephemeral: true}
	for _, iss := range []*types.Issue{mol, task} {
		if err := s.CreateIssue(ctx, iss, "test"); err != nil {
			t.Fatalf("CreateIssue(%s): %v", iss.Title, err)
		}
	}

	if got := countEphemeralOfType(t, ctx, s, types.TypeMolecule); got != 1 {
		t.Fatalf("setup: expected 1 ephemeral molecule, got %d", got)
	}

	// The documented protect-command: delete closed wisps EXCEPT the "mol" alias.
	_ = captureStdout(t, func() error {
		return runWispGC(makeWispGCTestCmd(true, true, []string{"mol"}), nil)
	})

	// The molecule must survive (alias resolved to canonical "molecule").
	if got := countEphemeralOfType(t, ctx, s, types.TypeMolecule); got != 1 {
		t.Errorf("molecule was deleted despite --exclude-type mol (protection failed open): %d molecules remain, want 1", got)
	}
	// The unprotected task must be gone (GC still ran; exclude is narrow).
	if got := countEphemeralOfType(t, ctx, s, types.TypeTask); got != 0 {
		t.Errorf("unprotected closed task survived GC: %d tasks remain, want 0", got)
	}
}

// makeWispGCAgeTestCmd builds a cobra.Command with the wisp gc flag schema and
// the given --age / --force values, for driving runWispGC in a test.
func makeWispGCAgeTestCmd(age string, force bool) *cobra.Command {
	c := &cobra.Command{Use: "gc"}
	c.Flags().Bool("dry-run", false, "")
	c.Flags().String("age", age, "")
	c.Flags().Bool("all", false, "")
	c.Flags().Bool("closed", false, "")
	c.Flags().BoolP("force", "f", force, "")
	c.Flags().StringSlice("exclude-type", nil, "")
	return c
}

// TestWispGCNegativeAgeRejected is the beads-v7lm regression test. `wisp gc
// --age -5h` must fail loud, not delete everything. time.ParseDuration accepts
// a negative duration, and the abandoned check (now.Sub(UpdatedAt) >
// ageThreshold) is TRUE for every wisp when ageThreshold < 0 — so before the
// guard, a negative --age silently turned a scoped GC into delete-all,
// destroying even wisps updated seconds ago. The guard rejects age<0 up front.
func TestWispGCNegativeAgeRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStoreWithPrefix(t, filepath.Join(t.TempDir(), "test.db"), "test")
	withWispTestGlobals(t, s, ctx)

	// A fresh ephemeral wisp (just created — must NOT be GC'd by any sane age).
	fresh := &types.Issue{Title: "fresh wisp", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true}
	if err := s.CreateIssue(ctx, fresh, "test"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Negative --age must be rejected with an error (not proceed to delete).
	out, err := captureStdoutExpectErr(t, func() error {
		return runWispGC(makeWispGCAgeTestCmd("-5h", true), nil)
	})
	if err == nil {
		t.Errorf("wisp gc --age -5h should return an error, got nil (output: %q)", out)
	}

	// The fresh wisp must still exist — the negative age must NOT have deleted it.
	tru := true
	remaining, serr := s.SearchIssues(ctx, "", types.IssueFilter{Ephemeral: &tru})
	if serr != nil {
		t.Fatalf("SearchIssues: %v", serr)
	}
	if len(remaining) != 1 {
		t.Errorf("negative --age deleted wisps despite the guard: %d ephemeral remain, want 1", len(remaining))
	}
}
