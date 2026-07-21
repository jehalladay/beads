//go:build cgo

package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-kdvfe: the DIRECT `bd promote` path was NON-ATOMIC. It ran
//   store.PromoteFromEphemeral(...)   // commit #1 — the wisp->bead move
//   store.AddIssueComment(...)        // commit #2 — the promotion record
// as two independent commits, and downgraded an AddIssueComment failure to a
// stderr "Warning: failed to add promotion comment..." while STILL returning a
// success (RC=0). So on a partial failure the bead landed promoted but the
// documented promotion comment (and the user's --reason) silently vanished,
// with a success exit code — a false "fully recorded" signal to any
// script/audit reading the promotion record. The PROXIED twin
// (runPromoteProxiedServer) was already atomic (one uw.Commit — a comment
// failure before commit discards the whole unit).
//
// The fix routes the direct path through store.PromoteFromEphemeralWithComment,
// which runs the promotion AND its recording comment in ONE transaction, and
// promote.go now RETURNS the error (rolls back) instead of warning-and-succeed.
//
// These teeth seed via the real `bd` subprocess (embedded dolt), then drive the
// real promoteCmd.RunE in-process against an EMBEDDED store — swapping the
// global store (the ado_test.go / pdzyv save/restore-globals pattern). The
// embedded store satisfies storage.DoltStorage and needs no Dolt container, so
// the fault leg is runnable here (unlike the containerized dolt-package tests).
//
// MUTATION-VERIFY: revert promote.go to the pre-fix two-commit sequence
// (store.PromoteFromEphemeral + a warned store.AddIssueComment) and
// TestPromoteIsAtomic_kdvfe FAILS — RunE returns nil (RC=0) and the wisp is
// left promoted despite the comment write failing.

var errInjectedCommentFailure = errors.New("injected promotion-comment failure (kdvfe test)")

// faultPromoteCommentStore wraps the real embedded store so the promotion-comment
// write fails on BOTH the atomic path (PromoteFromEphemeralWithComment) and the
// pre-fix path (AddIssueComment) — while PromoteFromEphemeral (the pre-fix
// promote step) delegates to the real store and SUCCEEDS. That symmetry makes
// the mutation-verify honest: a reverted promote.go still promotes the wisp via
// the real PromoteFromEphemeral and then hits the injected comment failure,
// exactly as production would on a partial failure.
type faultPromoteCommentStore struct {
	storage.DoltStorage
}

func (f *faultPromoteCommentStore) PromoteFromEphemeralWithComment(ctx context.Context, id, actor, comment string) (*types.Comment, error) {
	return nil, errInjectedCommentFailure
}

func (f *faultPromoteCommentStore) AddIssueComment(ctx context.Context, issueID, author, text string) (*types.Comment, error) {
	return nil, errInjectedCommentFailure
}

// runPromoteRunEWithStore swaps the global store/ctx, runs promoteCmd.RunE for
// the given wisp id, restores globals, and returns the RunE error.
func runPromoteRunEWithStore(t *testing.T, st storage.DoltStorage, wispID string) error {
	t.Helper()

	origStore := store
	origDBPath := dbPath
	origRootCtx := rootCtx
	origJSONOutput := jsonOutput
	origReadonly := readonlyMode
	origActor := actor
	origExplicit := commandDidExplicitDoltCommit
	origDidWrite := commandDidWrite.Load()
	t.Cleanup(func() {
		store = origStore
		dbPath = origDBPath
		rootCtx = origRootCtx
		jsonOutput = origJSONOutput
		readonlyMode = origReadonly
		actor = origActor
		commandDidExplicitDoltCommit = origExplicit
		commandDidWrite.Store(origDidWrite)
		_ = promoteCmd.Flags().Set("reason", "")
	})

	store = st
	rootCtx = context.Background()
	jsonOutput = false
	readonlyMode = false
	actor = "test-actor"
	_ = promoteCmd.Flags().Set("reason", "")

	var runErr error
	_ = captureStdout(t, func() error {
		runErr = promoteCmd.RunE(promoteCmd, []string{wispID})
		return nil
	})
	return runErr
}

// TestPromoteIsAtomic_kdvfe: force the promotion-comment write to fail, then
// assert (a) `bd promote` reports the failure (non-nil RunE error / non-zero
// exit) instead of the old RC=0 warning, and (b) the wisp is NOT promoted — it
// stays ephemeral, so the promotion did not land without its recording comment.
func TestPromoteIsAtomic_kdvfe(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "kd")

	// Seed an ephemeral wisp via the real CLI (routes to the wisps table).
	wisp := bdCreate(t, bd, dir, "promote me", "--ephemeral")
	real := openStore(t, beadsDir, "kd")

	got, err := real.GetIssue(context.Background(), wisp.ID)
	if err != nil {
		t.Fatalf("GetIssue before promote: %v", err)
	}
	if !got.Ephemeral {
		t.Skip("seeded issue is not ephemeral; cannot exercise promote")
	}

	fault := &faultPromoteCommentStore{DoltStorage: real}
	runErr := runPromoteRunEWithStore(t, fault, wisp.ID)

	// RC contract: the comment failure must surface as an error, not a
	// warn-and-succeed (the pre-fix RC=0 false "fully recorded" signal).
	if runErr == nil {
		t.Error("REGRESSION (kdvfe): `bd promote` returned success (RC=0) despite the promotion-comment write failing — the promotion comment (and --reason) is silently dropped under a success exit")
	}

	// ATOMICITY: the wisp must NOT have been promoted. A promoted-but-
	// comment-less bead is exactly the inconsistency the fix prevents. Reopen a
	// fresh store so we read committed state, not the fault wrapper's view.
	verify := openStore(t, beadsDir, "kd")
	after, err := verify.GetIssue(context.Background(), wisp.ID)
	if err != nil {
		t.Fatalf("GetIssue after failed promote: %v", err)
	}
	if !after.Ephemeral {
		t.Errorf("REGRESSION (kdvfe): wisp %s was promoted to a permanent bead even though the promotion-comment write failed — the promotion must roll back with the comment (atomic parity with the proxied path)", wisp.ID)
	}
}

// TestPromoteWithCommentPersistsAtomically_kdvfe is the positive leg: a
// successful promote lands the bead permanent AND its promotion comment in the
// comments table, in one shot via the real embedded store method.
// MUTATION-VERIFY: a PromoteFromEphemeralWithComment that drops the comment
// write leaves the comment absent → this test FAILS.
func TestPromoteWithCommentPersistsAtomically_kdvfe(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "kp")

	wisp := bdCreate(t, bd, dir, "promote me too", "--ephemeral")
	real := openStore(t, beadsDir, "kp")
	ctx := context.Background()

	if got, err := real.GetIssue(ctx, wisp.ID); err != nil {
		t.Fatalf("GetIssue before promote: %v", err)
	} else if !got.Ephemeral {
		t.Skip("seeded issue is not ephemeral; cannot exercise promote")
	}

	const promoText = "Promoted from wisp to permanent bead: worth keeping"
	comment, err := real.PromoteFromEphemeralWithComment(ctx, wisp.ID, "tester", promoText)
	if err != nil {
		t.Fatalf("PromoteFromEphemeralWithComment: %v", err)
	}
	if comment == nil || comment.Text != promoText {
		t.Fatalf("expected returned promotion comment %q, got %+v", promoText, comment)
	}

	// The bead is now permanent. Reopen to read committed state.
	verify := openStore(t, beadsDir, "kp")
	after, err := verify.GetIssue(ctx, wisp.ID)
	if err != nil {
		t.Fatalf("GetIssue after promote: %v", err)
	}
	if after.Ephemeral {
		t.Errorf("expected %s to be permanent after promote, still ephemeral", wisp.ID)
	}

	// The promotion comment is readable in the permanent comments table (9l1it):
	// bd show / bd comments read GetIssueComments, so a promotion recorded only
	// as an event would be invisible.
	comments, err := verify.GetIssueComments(ctx, wisp.ID)
	if err != nil {
		t.Fatalf("GetIssueComments: %v", err)
	}
	found := false
	for _, c := range comments {
		if c.Text == promoText {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("REGRESSION (kdvfe/9l1it): promotion comment not found in the comments table after promote; got %d comment(s) %v", len(comments), comments)
	}
}
