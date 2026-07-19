//go:build cgo

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-4yi7 (cmd-layer sibling of beads-ezng): `bd compact --apply` runs
// SnapshotIssue → CompactOverwrite [ATOMIC, durably commits the overwrite +
// compaction mark, beads-pj38] → AddComment [a COSMETIC post-commit event log].
// Before the fix, a failure of that post-commit AddComment called
// FatalErrorRespectJSON → os.Exit(1) — a FATAL abort for an issue that WAS
// successfully compacted (false failure; a retry then hits the
// eligibility/size skip). The fix warns and continues to the success output,
// mirroring promote.go's post-commit comment handling and the ezng library twin
// (TestCompactTier1_AddCommentError). This is the CLI path (runCompactApply),
// distinct from the internal/compact library path ezng fixed.
//
// runCompactApply exits the process via FatalError* on failure and has no exit
// seam, so the exit-code contract is exercised through a subprocess re-exec (the
// standard Go idiom for an os.Exit path): the child installs a fault-store that
// succeeds on every step EXCEPT AddComment, drives runCompactApply, and lets the
// natural os.Exit fire. RED before the fix: child exits 1. GREEN after: child
// exits 0 (compaction committed = success) and the CompactOverwrite ran.

// compactPostCommitFaultStore embeds DoltStorage (so it satisfies the full
// interface; any un-overridden method would panic if called, which none in the
// --apply happy path do) and overrides exactly the five methods runCompactApply
// touches: everything up to and including CompactOverwrite succeeds, and only
// AddComment fails — reproducing "compaction committed, then the cosmetic event
// comment errored".
type compactPostCommitFaultStore struct {
	storage.DoltStorage
	overwritten bool
}

func (s *compactPostCommitFaultStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	return &types.Issue{
		ID:                 id,
		Title:              "compact apply post-commit comment",
		Description:        strings.Repeat("original description body. ", 20),
		Design:             "ORIGINAL DESIGN",
		Notes:              "ORIGINAL NOTES",
		AcceptanceCriteria: "ORIGINAL ACCEPTANCE",
		Status:             types.StatusClosed,
	}, nil
}

func (s *compactPostCommitFaultStore) CheckEligibility(context.Context, string, int) (bool, string, error) {
	return true, "", nil
}

func (s *compactPostCommitFaultStore) SnapshotIssue(context.Context, string, int) error {
	return nil
}

func (s *compactPostCommitFaultStore) CompactOverwrite(context.Context, string, map[string]interface{}, int, int, string, string) error {
	s.overwritten = true
	return nil
}

func (s *compactPostCommitFaultStore) AddComment(context.Context, string, string, string) error {
	return errors.New("injected post-commit event-comment failure")
}

// TestCompactApplyPostCommitCommentIsNonFatal_4yi7 re-execs this test binary in a
// child that runs the fault-store drive helper below, and asserts the child
// exits 0 (success) rather than 1 (the pre-fix FATAL abort).
func TestCompactApplyPostCommitCommentIsNonFatal_4yi7(t *testing.T) {
	if os.Getenv("BEADS_4YI7_CHILD") == "1" {
		runCompactApplyPostCommitChild()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run", "TestCompactApplyPostCommitCommentIsNonFatal_4yi7")
	cmd.Env = append(os.Environ(), "BEADS_4YI7_CHILD=1")
	out, err := cmd.CombinedOutput()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("beads-4yi7: `bd compact --apply` FATAL-exited (code %d) on a post-commit "+
				"event-comment failure even though CompactOverwrite already committed the compaction — "+
				"a false failure for successfully-compacted data. Must warn + exit 0.\nchild output:\n%s",
				exitErr.ExitCode(), out)
		}
		t.Fatalf("beads-4yi7: child re-exec failed unexpectedly: %v\noutput:\n%s", err, out)
	}

	// GREEN sanity: the child must have actually reached & committed the overwrite
	// (proving we exercised the post-commit path, not an early bail-out).
	if !strings.Contains(string(out), "4yi7-child-overwrote") {
		t.Fatalf("beads-4yi7: child did not reach CompactOverwrite — the test did not exercise the "+
			"post-commit path.\noutput:\n%s", out)
	}
	if !strings.Contains(string(out), "4yi7-child-continued") {
		t.Fatalf("beads-4yi7: child did not continue past AddComment to the success output.\noutput:\n%s", out)
	}
}

// runCompactApplyPostCommitChild sets up the fault-store + compact globals and
// drives runCompactApply. It runs ONLY in the re-exec child (guarded by
// BEADS_4YI7_CHILD=1). With the fix, runCompactApply returns normally; without
// it, FatalErrorRespectJSON calls os.Exit(1) here.
func runCompactApplyPostCommitChild() {
	fault := &compactPostCommitFaultStore{}

	// summary shorter than the ~540-byte original so the size-reduction gate
	// passes without --force (compactForce stays false).
	summaryPath, err := os.CreateTemp("", "4yi7-summary-*.txt")
	if err != nil {
		os.Exit(2)
	}
	defer os.Remove(summaryPath.Name())
	if _, err := summaryPath.WriteString("compacted summary."); err != nil {
		os.Exit(2)
	}
	summaryPath.Close()

	// Install package globals runCompactApply reads.
	store = fault
	rootCtx = context.Background()
	compactID = "cp-4yi7"
	compactSummary = summaryPath.Name()
	compactTier = 1
	compactForce = false
	compactActor = "test"
	jsonOutput = false

	runCompactApply(rootCtx, fault)

	// Reached only if runCompactApply did NOT os.Exit (i.e. the fix is present).
	if fault.overwritten {
		os.Stdout.WriteString("4yi7-child-overwrote\n")
	}
	os.Stdout.WriteString("4yi7-child-continued\n")
}
