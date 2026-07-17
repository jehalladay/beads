//go:build cgo

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// closeFailingStore embeds a real DoltStorage and forces every CloseIssue to
// fail, leaving GetEpicsEligibleForClosure (and all other reads) intact. This
// isolates the "every eligible epic fails to close" path of
// `bd epic close-eligible` without touching the rest of the store behavior.
type closeFailingStore struct {
	storage.DoltStorage
}

func (s *closeFailingStore) CloseIssue(ctx context.Context, id, reason, actor, session string) error {
	return errors.New("injected close failure")
}

// TestEpicCloseEligible_AllFailed_NonZeroExit is the teeth for beads-b0df: when
// there ARE eligible epics but every CloseIssue attempt fails, the command must
// trip a non-zero exit (an *exitError from SilentExit/HandleErrorRespectJSON)
// rather than falsely reporting success with rc=0 ("✓ Closed 0 epic(s)" /
// {"closed":[],"count":0}). This mirrors the all-failed guard in close.go.
func TestEpicCloseEligible_AllFailed_NonZeroExit(t *testing.T) {
	h := newEpicTestHelper(t)

	// Seed an epic whose sole child is closed → eligible for closure.
	epic := &types.Issue{
		ID:        "test-epic-allfail",
		Title:     "Eligible epic whose close will fail",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeEpic,
		CreatedAt: time.Now(),
	}
	h.createIssue(t, epic)
	child := &types.Issue{
		Title:     "Closed child",
		Status:    types.StatusClosed,
		Priority:  2,
		IssueType: types.TypeTask,
		CreatedAt: time.Now(),
		ClosedAt:  ptrTime(time.Now()),
	}
	h.createIssue(t, child)
	h.addDependency(t, &types.Dependency{
		IssueID:     child.ID,
		DependsOnID: epic.ID,
		Type:        types.DepParentChild,
	})

	// Sanity: the epic really is eligible, so the len==0 early-out is NOT what
	// we hit — we exercise the all-failed guard specifically.
	if es := h.getEpicStatus(t, epic.ID); es == nil || !es.EligibleForClose {
		t.Fatalf("precondition: epic must be eligible for close, got %+v", es)
	}

	// Install a store whose CloseIssue always fails.
	prev := store
	store = &closeFailingStore{DoltStorage: h.s}
	t.Cleanup(func() { store = prev })

	// The command reads the package-global rootCtx (not cmd.Context()); it is
	// nil under `go test`, which panics inside the store's retry wrapper.
	prevCtx := rootCtx
	rootCtx = context.Background()
	t.Cleanup(func() { rootCtx = prevCtx })

	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonMode], func(t *testing.T) {
			prevJSON := jsonOutput
			jsonOutput = jsonMode
			t.Cleanup(func() { jsonOutput = prevJSON })

			err := closeEligibleEpicsCmd.RunE(closeEligibleEpicsCmd, nil)
			if err == nil {
				t.Fatalf("close-eligible with all-failing closes returned nil (false success); want non-zero exit")
			}
			var ee *exitError
			if !errors.As(err, &ee) {
				t.Fatalf("close-eligible returned %T (%v); want *exitError", err, err)
			}
			if ee.Code == 0 {
				t.Errorf("exit code = 0, want non-zero")
			}
		})
	}
}
