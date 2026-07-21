//go:build cgo

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

// beads-4hmjr: the PROXIED `bd promote` path (runPromoteProxiedServer) was
// NON-ATOMIC. It downgraded an AddComment failure to a stderr Warning and then
// called uw.Commit ANYWAY — so the promotion committed while the documented
// recording comment (and the user's --reason) silently vanished under RC=0.
// That is the exact non-atomicity beads-kdvfe fixed on the DIRECT path
// (PromoteFromEphemeralWithComment runs both writes in one tx; a comment failure
// returns an error → the whole tx rolls back, RC!=0). kdvfe's comment claimed
// "the proxied path was already atomic via a single uw.Commit" — but a single
// Commit is only atomic if the comment failure ABORTS before commit; here it was
// swallowed. Multi-write-atomicity proxied twin (ary2n/zcq86/uorhi class).
//
// The fix RETURNS the error on AddComment failure, so uw.Commit is never
// reached and the deferred uw.Close discards the uncommitted promotion.
//
// A live merge-succeeds/comment-fails scenario needs an injected comment fault
// in the UOW stack, and the proxied integration harness is a real-dolt
// subprocess (no fault seam), so — like the a81t3 fault-UOW and the kdvfe
// fault-store teeth — this drives runPromoteProxiedServer against a seam-level
// fake UOW: PromoteFromEphemeral SUCCEEDS, AddComment FAILS, and Commit is
// instrumented. The contract the fix guarantees is observable directly: on a
// comment failure the function returns an error and NEVER calls Commit (so the
// promotion cannot land without its comment).
//
// MUTATION-VERIFY: revert runPromoteProxiedServer to warn-and-continue on the
// AddComment error → this test FAILS (Commit is called and/or the function
// returns nil despite the comment write failing).

var errInjected4hmjrCommentFailure = errors.New("injected promotion-comment failure (4hmjr test)")

// fake4hmjrProvider returns the fake UOW below from NewUOW.
type fake4hmjrProvider struct {
	uw *fake4hmjrUOW
}

func (p *fake4hmjrProvider) NewUOW(context.Context) (uow.UnitOfWork, error) { return p.uw, nil }
func (p *fake4hmjrProvider) Close(context.Context) error                    { return nil }

// fake4hmjrUOW embeds uow.UnitOfWork (all unused methods panic if hit, which the
// test would surface) and overrides the four the promote path uses.
type fake4hmjrUOW struct {
	uow.UnitOfWork
	issue      *fake4hmjrIssueUC
	comment    *fake4hmjrCommentUC
	committed  bool
	closeCalls int
}

func (u *fake4hmjrUOW) IssueUseCase() domain.IssueUseCase     { return u.issue }
func (u *fake4hmjrUOW) CommentUseCase() domain.CommentUseCase { return u.comment }
func (u *fake4hmjrUOW) Commit(context.Context, string) error {
	u.committed = true
	return nil
}
func (u *fake4hmjrUOW) Close(context.Context) { u.closeCalls++ }

// fake4hmjrIssueUC: GetWisp/GetIssue resolve a single ephemeral wisp, and
// PromoteFromEphemeral succeeds (records that it ran) — so the ONLY thing that
// can fail the unit is the comment write.
type fake4hmjrIssueUC struct {
	domain.IssueUseCase
	wisp     *types.Issue
	promoted bool
}

func (u *fake4hmjrIssueUC) GetWisp(_ context.Context, id string) (*types.Issue, error) {
	if u.wisp != nil && u.wisp.ID == id {
		return u.wisp, nil
	}
	return nil, nil
}

func (u *fake4hmjrIssueUC) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	if u.wisp != nil && u.wisp.ID == id {
		return u.wisp, nil
	}
	return nil, nil
}

func (u *fake4hmjrIssueUC) PromoteFromEphemeral(_ context.Context, _, _ string) error {
	u.promoted = true
	return nil
}

// fake4hmjrCommentUC: AddComment fails, simulating a comment-write failure after
// the promotion step succeeded.
type fake4hmjrCommentUC struct {
	domain.CommentUseCase
}

func (u *fake4hmjrCommentUC) AddComment(context.Context, string, string, string) (*types.Comment, error) {
	return nil, errInjected4hmjrCommentFailure
}

func TestPromoteProxiedIsAtomic_4hmjr(t *testing.T) {
	uw := &fake4hmjrUOW{
		issue: &fake4hmjrIssueUC{
			wisp: &types.Issue{ID: "pp-1", Ephemeral: true, IssueType: types.TypeTask},
		},
		comment: &fake4hmjrCommentUC{},
	}

	origProvider := uowProvider
	origJSON := jsonOutput
	origActor := actor
	t.Cleanup(func() {
		uowProvider = origProvider
		jsonOutput = origJSON
		actor = origActor
	})
	uowProvider = &fake4hmjrProvider{uw: uw}
	jsonOutput = false
	actor = "test-actor"

	err := runPromoteProxiedServer(context.Background(), "pp-1", "worth keeping")

	// The promotion step must have run (so the ONLY failure is the comment) —
	// this makes the atomicity assertion honest, mirroring the kdvfe fault-store
	// symmetry (promote succeeds, comment fails).
	if !uw.issue.promoted {
		t.Fatalf("precondition: PromoteFromEphemeral was not called; the fake did not exercise the promote step")
	}

	// RC contract: the comment failure must surface as an error, not a
	// warn-and-succeed (the pre-fix RC=0 false "fully recorded" signal).
	if err == nil {
		t.Error("REGRESSION (4hmjr): proxied `bd promote` returned success despite the promotion-comment write failing — the recording comment (and --reason) is silently dropped under a success exit")
	}

	// ATOMICITY: Commit must NOT have run. A committed promotion with a failed
	// comment is exactly the inconsistency the fix prevents (the deferred
	// uw.Close then discards the uncommitted promotion).
	if uw.committed {
		t.Error("REGRESSION (4hmjr): the proxied promote committed the unit of work even though the promotion-comment write failed — the promotion must roll back with the comment (atomic parity with the direct path, beads-kdvfe)")
	}
}
