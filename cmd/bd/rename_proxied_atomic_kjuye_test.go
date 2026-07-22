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

// beads-kjuye: the PROXIED `bd rename` path (runRenameProxiedServer) was
// NON-ATOMIC. It downgraded an updateReferencesInAllIssuesProxied failure to a
// stderr Warning (captured into refWarning) and then called uw.Commit ANYWAY —
// so the id rename committed while an arbitrary partial suffix of the
// cross-issue reference rewrite failed, leaving durable dangling old-id
// references reported to the operator only as RC=0 + a soft warning.
//
// That is the exact non-atomicity beads-uorhi fixed on the DIRECT path (rename +
// updateReferencesInAllIssuesTx in one store.RunInTransaction; a ref-rewrite
// error rolls the rename back, RC!=0). uorhi's own comment CLAIMED it "mirrors
// the atomic proxied UOW twin ... staged onto one uw + single Commit" — but a
// single Commit is only atomic if the ref failure ABORTS before commit; here it
// was swallowed. Multi-write-atomicity proxied twin (ary2n/zcq86/uorhi/4hmjr
// class).
//
// The fix RETURNS the error on the ref-rewrite failure, so uw.Commit is never
// reached and the deferred uw.Close discards the uncommitted rename
// (RollbackUnlessCommitted, uow.go) — the OLD id keeps resolving and no dangling
// old-id reference survives.
//
// A live rename-succeeds/ref-rewrite-fails scenario needs an injected fault in
// the UOW stack, and the proxied integration harness is a real-dolt subprocess
// (no fault seam), so — like the 4hmjr/a81t3/kdvfe teeth — this drives
// runRenameProxiedServer against a seam-level fake UOW: RenameIssueID SUCCEEDS,
// the reference-rewrite of a referencing issue FAILS (UpdateIssue), and Commit
// is instrumented. The contract the fix guarantees is observable directly: on a
// ref-rewrite failure the function returns an error and NEVER calls Commit (so
// the rename cannot land while a reference to the vanished old id survives).
//
// MUTATION-VERIFY: revert runRenameProxiedServer to swallow-into-refWarning +
// Commit-anyway → this test FAILS (Commit is called and/or the function returns
// nil despite the ref-rewrite write failing).

var errInjectedKjuyeRefFailure = errors.New("injected reference-rewrite failure (kjuye test)")

const (
	kjuyeOldID = "kj-abc"
	kjuyeNewID = "kj-xyz"
	kjuyeRefID = "kj-ref" // an issue whose description references kjuyeOldID
)

// fakeKjuyeProvider returns the fake UOW below from NewUOW.
type fakeKjuyeProvider struct {
	uw *fakeKjuyeUOW
}

func (p *fakeKjuyeProvider) NewUOW(context.Context) (uow.UnitOfWork, error) { return p.uw, nil }
func (p *fakeKjuyeProvider) Close(context.Context) error                    { return nil }

// fakeKjuyeUOW embeds uow.UnitOfWork (any method the rename path does NOT use
// panics if hit, which the test would surface) and overrides the handful the
// rename path exercises.
type fakeKjuyeUOW struct {
	uow.UnitOfWork
	issue      *fakeKjuyeIssueUC
	config     *fakeKjuyeConfigUC
	comment    *fakeKjuyeCommentUC
	committed  bool
	closeCalls int
}

func (u *fakeKjuyeUOW) IssueUseCase() domain.IssueUseCase     { return u.issue }
func (u *fakeKjuyeUOW) ConfigUseCase() domain.ConfigUseCase   { return u.config }
func (u *fakeKjuyeUOW) CommentUseCase() domain.CommentUseCase { return u.comment }
func (u *fakeKjuyeUOW) Commit(context.Context, string) error {
	u.committed = true
	return nil
}
func (u *fakeKjuyeUOW) Close(context.Context) { u.closeCalls++ }

// fakeKjuyeConfigUC: LoadCreateContext returns the "kj" prefix so the c3igh
// prefix-invariant guard accepts the new id (kj-xyz) and the rename proceeds to
// the reference rewrite (the step under test).
type fakeKjuyeConfigUC struct {
	domain.ConfigUseCase
}

func (u *fakeKjuyeConfigUC) LoadCreateContext(context.Context) (domain.CreateContext, error) {
	return domain.CreateContext{IssuePrefix: "kj", AllowedPrefixes: "kj"}, nil
}

// fakeKjuyeIssueUC: GetIssue resolves the old id (exists) and reports the new id
// as free; RenameIssueID succeeds (records that it ran); SearchIssues returns a
// single referencing issue; and UpdateIssue on that referencing issue FAILS —
// so the ONLY thing that fails the unit is the reference rewrite, mirroring the
// uorhi direct-path fault point.
type fakeKjuyeIssueUC struct {
	domain.IssueUseCase
	renamed bool
}

func (u *fakeKjuyeIssueUC) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	switch id {
	case kjuyeOldID:
		return &types.Issue{ID: kjuyeOldID, Title: "target", Status: types.StatusOpen, IssueType: types.TypeTask}, nil
	case kjuyeNewID:
		// New id is free: return (nil, err) so the "already exists" guard passes.
		return nil, errors.New("not found")
	}
	return nil, errors.New("not found")
}

func (u *fakeKjuyeIssueUC) RenameIssueID(_ context.Context, _, _ string, _ *types.Issue, _ string) error {
	u.renamed = true
	return nil
}

func (u *fakeKjuyeIssueUC) SearchIssues(_ context.Context, _ string, _ types.IssueFilter) (domain.SearchPage, error) {
	return domain.SearchPage{
		Items: []*types.Issue{
			{ID: kjuyeRefID, Title: "refs target", Description: "depends on " + kjuyeOldID + " for context", Status: types.StatusOpen, IssueType: types.TypeTask},
		},
	}, nil
}

func (u *fakeKjuyeIssueUC) UpdateIssue(_ context.Context, id string, _ map[string]any, _ string) error {
	if id == kjuyeRefID {
		return errInjectedKjuyeRefFailure
	}
	return nil
}

// fakeKjuyeCommentUC is present so the rename path's comment sweep does not
// panic if it is reached — but with the fix, the UpdateIssue failure aborts
// BEFORE the comment sweep for that issue, so GetCommentsForIssue is not hit for
// the referencing issue. Return empty defensively.
type fakeKjuyeCommentUC struct {
	domain.CommentUseCase
}

func (u *fakeKjuyeCommentUC) GetCommentsForIssue(context.Context, string) ([]*types.Comment, error) {
	return nil, nil
}

func TestRenameProxiedIsAtomic_kjuye(t *testing.T) {
	uw := &fakeKjuyeUOW{
		issue:   &fakeKjuyeIssueUC{},
		config:  &fakeKjuyeConfigUC{},
		comment: &fakeKjuyeCommentUC{},
	}

	origProvider := uowProvider
	origJSON := jsonOutput
	origActor := actor
	t.Cleanup(func() {
		uowProvider = origProvider
		jsonOutput = origJSON
		actor = origActor
	})
	uowProvider = &fakeKjuyeProvider{uw: uw}
	jsonOutput = false
	actor = "test-actor"

	err := runRenameProxiedServer(context.Background(), kjuyeOldID, kjuyeNewID, false)

	// The rename step must have run (so the ONLY failure is the reference
	// rewrite) — this makes the atomicity assertion honest, mirroring the uorhi
	// fault symmetry (rename succeeds, ref-rewrite fails).
	if !uw.issue.renamed {
		t.Fatalf("precondition: RenameIssueID was not called; the fake did not exercise the rename step")
	}

	// RC contract: the ref-rewrite failure must surface as an error, not a
	// warn-and-succeed (the pre-fix RC=0 false "renamed" signal).
	if err == nil {
		t.Error("REGRESSION (kjuye): proxied `bd rename` returned success despite the cross-issue reference rewrite failing — a dangling old-id reference is silently left under a success exit")
	}

	// ATOMICITY: Commit must NOT have run. A committed rename with a failed
	// reference rewrite is exactly the dangling-ref inconsistency the fix
	// prevents (the deferred uw.Close then discards the uncommitted rename).
	if uw.committed {
		t.Error("REGRESSION (kjuye): the proxied rename committed the unit of work even though the reference rewrite failed — the rename must roll back with the ref sweep (atomic parity with the direct path, beads-uorhi)")
	}
}
