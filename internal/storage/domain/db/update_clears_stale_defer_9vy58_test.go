package db

import (
	"time"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// TestUpdateClearsStaleFutureDefer_9vy58 pins the beads-9vy58 fix: the domain
// db-layer Update (the proxied/hub update path via IssueSQLRepository, wired
// through uow.go) must clear a stale FUTURE defer_until when the caller flips
// --status to open|in_progress without touching defer_until — the DOMAIN-VS-
// DIRECT twin of the direct-path beads-l2lb7 clear (cmd/bd/update.go).
//
// The direct path's l2lb7 clear lives in the regularUpdates block, which the
// proxied path never reaches (usesProxiedServer() dispatches at update.go:113,
// BEFORE that block). Its sibling status-transition side effects WERE all
// mirrored into this statusChanging block — closed_at (h3iv), close_reason
// (6qo8t), started_at (hfb4) — but the defer-clear was not, so a hub-connected
// crew's `bd update --status open` on a deferred issue left the stale future
// defer_until in place, leaving a self-contradictory status=open-but-invisible-
// to-`bd ready` row (ready.go predicate: defer_until IS NULL OR
// defer_until <= UTC_TIMESTAMP()).
//
// Mutation check: remove the defer_until=NULL append in the statusChanging block
// of issue.go and the open/in_progress subtests go GREEN->RED (the stale future
// defer_until survives the status flip).
func (s *testSuite) TestUpdateClearsStaleFutureDefer_9vy58() {
	future := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)

	// insertDeferred seeds a deferred issue with a future defer_until.
	insertDeferred := func(id string) {
		r := s.issueRepo()
		iss := newTestIssue(id, "deferred")
		iss.Status = types.StatusDeferred
		d := future
		iss.DeferUntil = &d
		s.Require().NoError(r.Insert(s.Ctx(), iss, "tester", domain.InsertIssueOpts{}))
		// Sanity: the future defer_until round-trips on read.
		got, err := r.Get(s.Ctx(), id, domain.IssueTableOpts{})
		s.Require().NoError(err)
		s.Require().NotNil(got.DeferUntil, "seed defer_until must persist")
	}

	s.Run("StatusOpenClearsStaleFutureDefer", func() {
		r := s.issueRepo()
		insertDeferred("bd-9vy58-open")
		s.Require().NoError(r.Update(s.Ctx(), "bd-9vy58-open",
			map[string]any{"status": types.StatusOpen}, "tester", domain.IssueTableOpts{}))
		out, err := r.Get(s.Ctx(), "bd-9vy58-open", domain.IssueTableOpts{})
		s.Require().NoError(err)
		s.Equal(types.StatusOpen, out.Status)
		s.Nil(out.DeferUntil, "status->open must clear the stale future defer_until (beads-9vy58)")
	})

	s.Run("StatusInProgressClearsStaleFutureDefer", func() {
		r := s.issueRepo()
		insertDeferred("bd-9vy58-inprog")
		s.Require().NoError(r.Update(s.Ctx(), "bd-9vy58-inprog",
			map[string]any{"status": types.StatusInProgress}, "tester", domain.IssueTableOpts{}))
		out, err := r.Get(s.Ctx(), "bd-9vy58-inprog", domain.IssueTableOpts{})
		s.Require().NoError(err)
		s.Equal(types.StatusInProgress, out.Status)
		s.Nil(out.DeferUntil, "status->in_progress must clear the stale future defer_until (beads-9vy58)")
	})

	s.Run("CallerSetDeferUntilWins", func() {
		r := s.issueRepo()
		insertDeferred("bd-9vy58-explicit")
		newDefer := time.Now().UTC().Add(240 * time.Hour).Truncate(time.Second)
		s.Require().NoError(r.Update(s.Ctx(), "bd-9vy58-explicit",
			map[string]any{"status": types.StatusOpen, "defer_until": newDefer}, "tester", domain.IssueTableOpts{}))
		out, err := r.Get(s.Ctx(), "bd-9vy58-explicit", domain.IssueTableOpts{})
		s.Require().NoError(err)
		s.Require().NotNil(out.DeferUntil, "a caller-supplied defer_until must not be clobbered by the auto-clear")
		s.Equal(newDefer.Unix(), out.DeferUntil.Unix(), "caller-set defer_until wins over the auto-clear")
	})

	s.Run("PastDeferUnaffectedNoClauseNeeded", func() {
		r := s.issueRepo()
		// A PAST defer_until is already ready-visible; the guard is scoped to
		// FUTURE defer_until, so this leg proves the auto-clear does not fire on
		// a past value (it would be a harmless no-op either way, but the guard
		// mirrors l2lb7's DeferUntil.After(now) condition).
		id := "bd-9vy58-past"
		iss := newTestIssue(id, "past defer")
		iss.Status = types.StatusDeferred
		past := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Second)
		iss.DeferUntil = &past
		s.Require().NoError(r.Insert(s.Ctx(), iss, "tester", domain.InsertIssueOpts{}))
		s.Require().NoError(r.Update(s.Ctx(), id,
			map[string]any{"status": types.StatusOpen}, "tester", domain.IssueTableOpts{}))
		out, err := r.Get(s.Ctx(), id, domain.IssueTableOpts{})
		s.Require().NoError(err)
		s.Equal(types.StatusOpen, out.Status)
	})

	s.Run("StatusBlockedDoesNotClearDefer", func() {
		r := s.issueRepo()
		insertDeferred("bd-9vy58-blocked")
		s.Require().NoError(r.Update(s.Ctx(), "bd-9vy58-blocked",
			map[string]any{"status": types.StatusBlocked}, "tester", domain.IssueTableOpts{}))
		out, err := r.Get(s.Ctx(), "bd-9vy58-blocked", domain.IssueTableOpts{})
		s.Require().NoError(err)
		s.Require().NotNil(out.DeferUntil, "a non-ready-visible status (blocked) must NOT clear defer_until (mirrors l2lb7 scope)")
	})
}
