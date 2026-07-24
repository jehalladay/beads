package db

import (
	"database/sql"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

func (s *testSuite) TestLabelSQLRepository() {
	s.Run("Insert", func() {
		s.Run("RoundTripWithList", s.labelInsertRoundTrip)
		s.Run("IdempotentDuplicate", s.labelInsertIdempotent)
		s.Run("DuplicateAddRecordsNoEvent", s.labelInsertDuplicateNoEvent)
		s.Run("RecordsLabelAddedEvent", s.labelInsertRecordsEvent)
		s.Run("RejectsEmptyIssueID", s.labelInsertEmptyIssueID)
		s.Run("RejectsEmptyLabel", s.labelInsertEmptyLabel)
		s.Run("MissingIssueIDFailsFK", s.labelInsertFKViolation)
	})
	s.Run("Delete", func() {
		s.Run("RemovesLabelRow", s.labelDeleteRemoves)
		s.Run("RecordsLabelRemovedEvent", s.labelDeleteRecordsEvent)
		s.Run("MissingLabelIsNoop", s.labelDeleteMissingNoop)
		s.Run("MissingLabelRecordsNoEvent", s.labelDeleteMissingNoEvent)
		s.Run("OnlyTargetLabelRemoved", s.labelDeleteSpecificLabel)
		s.Run("RejectsEmptyIssueID", s.labelDeleteEmptyIssueID)
		s.Run("RejectsEmptyLabel", s.labelDeleteEmptyLabel)
		s.Run("WispRoutesToWispLabels", s.labelDeleteWispRouting)
	})
	s.Run("List", func() {
		s.Run("OrdersByLabelAlpha", s.labelListAlphaOrder)
		s.Run("UnknownIssueReturnsEmpty", s.labelListUnknown)
	})
	s.Run("ListByIssueIDs", func() {
		s.Run("EmptySliceReturnsEmptyMap", s.labelBulkEmpty)
		s.Run("MultipleIssuesGroupedByID", s.labelBulkGrouped)
		s.Run("MissingIDsAreAbsent", s.labelBulkMissingAbsent)
	})
	s.Run("Wisp", func() {
		s.Run("InsertRoutesToWispLabels", s.labelWispInsertRouting)
		s.Run("InsertRecordsEventInWispEvents", s.labelWispInsertEvent)
		s.Run("ListReadsFromWispLabels", s.labelWispListIsolated)
		s.Run("ListByIssueIDsReadsFromWispLabels", s.labelWispBulkIsolated)
	})
	// beads-y3tm3: DOMAIN twin of the DIRECT-path case-fold coherence fix
	// (beads-9jjj8). Insert must fold to lower at write and Delete must match
	// on LOWER(label), or a hub-connected (proxied) crew re-introduces the
	// three-way add/query/remove case divergence 9jjj8 closed.
	s.Run("CaseFoldCoherence_y3tm3", func() {
		s.Run("InsertFoldsStoredLower", s.labelInsertFoldsLower)
		s.Run("NoCoexistingCaseVariants", s.labelInsertNoCoexistCase)
		s.Run("DeleteIsCaseInsensitive", s.labelDeleteCaseInsensitive)
	})
}

func (s *testSuite) labelRepo() domain.LabelSQLRepository {
	return NewLabelSQLRepository(s.Runner())
}

func (s *testSuite) labelInsertRoundTrip() {
	s.seedIssueRow("bd-lbl-1")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-1", "tech-debt", "tester", domain.LabelOpts{}))

	out, err := r.List(s.Ctx(), "bd-lbl-1", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Equal([]string{"tech-debt"}, out)
}

func (s *testSuite) labelInsertIdempotent() {
	s.seedIssueRow("bd-lbl-dup")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-dup", "needs-review", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-dup", "needs-review", "tester", domain.LabelOpts{}))

	out, err := r.List(s.Ctx(), "bd-lbl-dup", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Equal([]string{"needs-review"}, out, "duplicate label add should be a no-op on the labels table")

	// beads-5vpoh: the INSERT IGNORE second add affects zero rows, so it must
	// NOT record a second label_added event. This proxied (domain/db) leg
	// previously recorded unconditionally (the direct issueops.AddLabelInTx
	// already guarded on RowsAffected==0 for beads-usz1); the twin now matches.
	var count int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM events WHERE issue_id = ? AND event_type = ?",
		"bd-lbl-dup", string(types.EventLabelAdded),
	).Scan(&count))
	s.Equal(1, count, "duplicate label add must record exactly one label_added event, not one per call")
}

// labelInsertDuplicateNoEvent is the beads-5vpoh no-op-event tooth for the
// proxied Insert path: adding a label that is ALREADY present (INSERT IGNORE
// affects 0 rows) must record NO new label_added event, while the FIRST add of
// a genuinely-new label still records exactly one (the regression guard against
// under-recording). Mirrors the direct guard in issueops.AddLabelInTx (usz1).
func (s *testSuite) labelInsertDuplicateNoEvent() {
	s.seedIssueRow("bd-lbl-dup-evt")
	r := s.labelRepo()

	// New label → exactly one event.
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-dup-evt", "keep", "tester", domain.LabelOpts{}))
	s.Equal(1, s.labelEventCount("bd-lbl-dup-evt", types.EventLabelAdded, "labels"),
		"first add of a new label must record exactly one label_added event")

	// Duplicate add → still exactly one (no spurious no-op event).
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-dup-evt", "keep", "tester", domain.LabelOpts{}))
	s.Equal(1, s.labelEventCount("bd-lbl-dup-evt", types.EventLabelAdded, "labels"),
		"re-adding an already-present label (INSERT IGNORE 0 rows) must NOT record a second label_added event")
}

func (s *testSuite) labelInsertRecordsEvent() {
	s.seedIssueRow("bd-lbl-evt")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-evt", "perf", "alice", domain.LabelOpts{}))

	// beads-6p27f: the label event's human-readable value goes in the comment
	// column ("Added label: perf"), mirroring the direct path
	// (issueops.AddLabelInTx); old_value/new_value stay NULL/empty.
	var actor string
	var oldValue, newValue, comment sql.NullString
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT actor, old_value, new_value, comment FROM events WHERE issue_id = ? AND event_type = ?",
		"bd-lbl-evt", string(types.EventLabelAdded),
	).Scan(&actor, &oldValue, &newValue, &comment))
	s.Equal("alice", actor)
	s.True(comment.Valid, "label_added event should carry a comment")
	s.Equal("Added label: perf", comment.String, "comment must match the direct path (issueops.AddLabelInTx)")
	s.False(newValue.Valid && newValue.String != "", "label name must NOT be stored in new_value (direct parity, beads-6p27f); got %q", newValue.String)
	s.False(oldValue.Valid && oldValue.String != "", "old_value must be empty for label_added; got %q", oldValue.String)
}

func (s *testSuite) labelInsertEmptyIssueID() {
	err := s.labelRepo().Insert(s.Ctx(), "", "x", "tester", domain.LabelOpts{})
	s.Require().Error(err)
}

func (s *testSuite) labelInsertEmptyLabel() {
	err := s.labelRepo().Insert(s.Ctx(), "bd-lbl-x", "", "tester", domain.LabelOpts{})
	s.Require().Error(err)
}

func (s *testSuite) labelInsertFKViolation() {
	err := s.labelRepo().Insert(s.Ctx(), "bd-no-such-issue", "x", "tester", domain.LabelOpts{})
	s.Require().Error(err, "expected FK violation when issue_id does not exist")
}

func (s *testSuite) labelListAlphaOrder() {
	s.seedIssueRow("bd-lbl-ord")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-ord", "zeta", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-ord", "alpha", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-ord", "mu", "tester", domain.LabelOpts{}))

	out, err := r.List(s.Ctx(), "bd-lbl-ord", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Equal([]string{"alpha", "mu", "zeta"}, out)
}

func (s *testSuite) labelListUnknown() {
	out, err := s.labelRepo().List(s.Ctx(), "bd-no-labels-here", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Empty(out)
}

func (s *testSuite) labelBulkEmpty() {
	out, err := s.labelRepo().ListByIssueIDs(s.Ctx(), nil, domain.LabelOpts{})
	s.Require().NoError(err)
	s.NotNil(out, "ListByIssueIDs should return a non-nil empty map")
	s.Empty(out)
}

func (s *testSuite) labelBulkGrouped() {
	s.seedIssueRow("bd-lbl-bulk-1")
	s.seedIssueRow("bd-lbl-bulk-2")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-bulk-1", "a", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-bulk-1", "b", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-bulk-2", "c", "tester", domain.LabelOpts{}))

	out, err := r.ListByIssueIDs(s.Ctx(), []string{"bd-lbl-bulk-1", "bd-lbl-bulk-2"}, domain.LabelOpts{})
	s.Require().NoError(err)
	s.Equal([]string{"a", "b"}, out["bd-lbl-bulk-1"])
	s.Equal([]string{"c"}, out["bd-lbl-bulk-2"])
}

func (s *testSuite) labelBulkMissingAbsent() {
	s.seedIssueRow("bd-lbl-present")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-present", "x", "tester", domain.LabelOpts{}))

	out, err := r.ListByIssueIDs(s.Ctx(), []string{"bd-lbl-present", "bd-lbl-missing"}, domain.LabelOpts{})
	s.Require().NoError(err)
	s.Equal([]string{"x"}, out["bd-lbl-present"])
	_, present := out["bd-lbl-missing"]
	s.False(present, "missing issue IDs should not appear in the result map")
}

func (s *testSuite) labelWispInsertRouting() {
	s.seedWispRow("bd-lbl-wisp-1")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-wisp-1", "wisp-only", "tester", domain.LabelOpts{UseWispsTable: true}))

	var wispCount, permCount int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM wisp_labels WHERE issue_id = ?", "bd-lbl-wisp-1").Scan(&wispCount))
	s.Equal(1, wispCount)
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM labels WHERE issue_id = ?", "bd-lbl-wisp-1").Scan(&permCount))
	s.Equal(0, permCount, "wisp-routed insert must not write to labels")
}

func (s *testSuite) labelWispInsertEvent() {
	s.seedWispRow("bd-lbl-wisp-evt")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-wisp-evt", "audit", "alice", domain.LabelOpts{UseWispsTable: true}))

	var wispEvtCount, permEvtCount int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM wisp_events WHERE issue_id = ? AND event_type = ?",
		"bd-lbl-wisp-evt", string(types.EventLabelAdded),
	).Scan(&wispEvtCount))
	s.Equal(1, wispEvtCount)
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM events WHERE issue_id = ? AND event_type = ?",
		"bd-lbl-wisp-evt", string(types.EventLabelAdded),
	).Scan(&permEvtCount))
	s.Equal(0, permEvtCount, "wisp-routed label event must not write to events")
}

func (s *testSuite) labelWispListIsolated() {
	// Same issue ID in both tables (won't happen in practice, but proves the routing
	// is strict — List with UseWispsTable=true sees only wisp_labels rows).
	s.seedIssueRow("bd-lbl-iso-perm")
	s.seedWispRow("bd-lbl-iso-wisp")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-iso-perm", "perm", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-iso-wisp", "wisp", "tester", domain.LabelOpts{UseWispsTable: true}))

	permLabels, err := r.List(s.Ctx(), "bd-lbl-iso-perm", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Equal([]string{"perm"}, permLabels)

	wispLabels, err := r.List(s.Ctx(), "bd-lbl-iso-wisp", domain.LabelOpts{UseWispsTable: true})
	s.Require().NoError(err)
	s.Equal([]string{"wisp"}, wispLabels)

	// Cross-routed lookups should be empty.
	empty, err := r.List(s.Ctx(), "bd-lbl-iso-wisp", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Empty(empty)
	empty, err = r.List(s.Ctx(), "bd-lbl-iso-perm", domain.LabelOpts{UseWispsTable: true})
	s.Require().NoError(err)
	s.Empty(empty)
}

func (s *testSuite) labelWispBulkIsolated() {
	s.seedWispRow("bd-lbl-wbulk-1")
	s.seedWispRow("bd-lbl-wbulk-2")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-wbulk-1", "a", "tester", domain.LabelOpts{UseWispsTable: true}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-wbulk-1", "b", "tester", domain.LabelOpts{UseWispsTable: true}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-wbulk-2", "c", "tester", domain.LabelOpts{UseWispsTable: true}))

	out, err := r.ListByIssueIDs(s.Ctx(), []string{"bd-lbl-wbulk-1", "bd-lbl-wbulk-2"}, domain.LabelOpts{UseWispsTable: true})
	s.Require().NoError(err)
	s.Equal([]string{"a", "b"}, out["bd-lbl-wbulk-1"])
	s.Equal([]string{"c"}, out["bd-lbl-wbulk-2"])
}

func (s *testSuite) labelDeleteRemoves() {
	s.seedIssueRow("bd-lbl-del-1")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-del-1", "tech-debt", "tester", domain.LabelOpts{}))

	s.Require().NoError(r.Delete(s.Ctx(), "bd-lbl-del-1", "tech-debt", "tester", domain.LabelOpts{}))

	out, err := r.List(s.Ctx(), "bd-lbl-del-1", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Empty(out, "deleted label should no longer appear in List")
}

func (s *testSuite) labelDeleteRecordsEvent() {
	s.seedIssueRow("bd-lbl-del-evt")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-del-evt", "perf", "alice", domain.LabelOpts{}))
	s.Require().NoError(r.Delete(s.Ctx(), "bd-lbl-del-evt", "perf", "bob", domain.LabelOpts{}))

	// beads-6p27f: the removed-label value goes in the comment column
	// ("Removed label: perf"), mirroring issueops.RemoveLabelInTx; old/new empty.
	var actor string
	var oldValue, newValue, comment sql.NullString
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT actor, old_value, new_value, comment FROM events WHERE issue_id = ? AND event_type = ?",
		"bd-lbl-del-evt", string(types.EventLabelRemoved),
	).Scan(&actor, &oldValue, &newValue, &comment))
	s.Equal("bob", actor)
	s.True(comment.Valid, "label_removed event should carry a comment")
	s.Equal("Removed label: perf", comment.String, "comment must match the direct path (issueops.RemoveLabelInTx)")
	s.False(oldValue.Valid && oldValue.String != "", "label name must NOT be stored in old_value (direct parity, beads-6p27f); got %q", oldValue.String)
	s.False(newValue.Valid && newValue.String != "", "new_value must be empty for label_removed; got %q", newValue.String)
}

func (s *testSuite) labelDeleteMissingNoop() {
	s.seedIssueRow("bd-lbl-del-miss")
	r := s.labelRepo()
	s.Require().NoError(r.Delete(s.Ctx(), "bd-lbl-del-miss", "never-there", "tester", domain.LabelOpts{}))

	out, err := r.List(s.Ctx(), "bd-lbl-del-miss", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Empty(out)
}

// labelDeleteMissingNoEvent is the beads-5vpoh no-op-event tooth for the proxied
// Delete path: removing a label the issue never carried (DELETE affects 0 rows)
// must record NO label_removed event, while removing a PRESENT label still
// records exactly one (the regression guard against under-recording). Mirrors
// the direct guard in issueops.RemoveLabelInTx (usz1).
func (s *testSuite) labelDeleteMissingNoEvent() {
	s.seedIssueRow("bd-lbl-del-miss-evt")
	r := s.labelRepo()

	// Remove a never-present label → no event.
	s.Require().NoError(r.Delete(s.Ctx(), "bd-lbl-del-miss-evt", "ghost", "tester", domain.LabelOpts{}))
	s.Equal(0, s.labelEventCount("bd-lbl-del-miss-evt", types.EventLabelRemoved, "labels"),
		"removing a never-present label (DELETE 0 rows) must NOT record a label_removed event")

	// Add then remove a present label → exactly one label_removed event.
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-del-miss-evt", "real", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Delete(s.Ctx(), "bd-lbl-del-miss-evt", "real", "tester", domain.LabelOpts{}))
	s.Equal(1, s.labelEventCount("bd-lbl-del-miss-evt", types.EventLabelRemoved, "labels"),
		"removing a present label must record exactly one label_removed event")
}

// labelEventCount returns how many events of eventType exist for issueID in the
// given event table ("events" or "wisp_events"). beads-5vpoh helper.
func (s *testSuite) labelEventCount(issueID string, eventType types.EventType, labelTable string) int {
	eventTable := "events"
	if labelTable == "wisp_labels" {
		eventTable = "wisp_events"
	}
	var count int
	//nolint:gosec // G201: eventTable is one of two hardcoded constants
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM "+eventTable+" WHERE issue_id = ? AND event_type = ?",
		issueID, string(eventType),
	).Scan(&count))
	return count
}

func (s *testSuite) labelDeleteSpecificLabel() {
	s.seedIssueRow("bd-lbl-del-specific")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-del-specific", "keep", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-del-specific", "drop", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-del-specific", "stay", "tester", domain.LabelOpts{}))

	s.Require().NoError(r.Delete(s.Ctx(), "bd-lbl-del-specific", "drop", "tester", domain.LabelOpts{}))

	out, err := r.List(s.Ctx(), "bd-lbl-del-specific", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Equal([]string{"keep", "stay"}, out, "Delete must target only the named label, not siblings on the same issue")
}

func (s *testSuite) labelDeleteEmptyIssueID() {
	err := s.labelRepo().Delete(s.Ctx(), "", "x", "tester", domain.LabelOpts{})
	s.Require().Error(err)
}

func (s *testSuite) labelDeleteEmptyLabel() {
	err := s.labelRepo().Delete(s.Ctx(), "bd-lbl-del-x", "", "tester", domain.LabelOpts{})
	s.Require().Error(err)
}

func (s *testSuite) labelDeleteWispRouting() {
	s.seedIssueRow("bd-lbl-del-cross-perm")
	s.seedWispRow("bd-lbl-del-cross-wisp")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-del-cross-perm", "shared", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-lbl-del-cross-wisp", "shared", "tester", domain.LabelOpts{UseWispsTable: true}))

	s.Require().NoError(r.Delete(s.Ctx(), "bd-lbl-del-cross-wisp", "shared", "tester", domain.LabelOpts{UseWispsTable: true}))

	var wispCount, permCount int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM wisp_labels WHERE issue_id = ?", "bd-lbl-del-cross-wisp").Scan(&wispCount))
	s.Equal(0, wispCount, "wisp-routed Delete must remove the wisp_labels row")
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM labels WHERE issue_id = ?", "bd-lbl-del-cross-perm").Scan(&permCount))
	s.Equal(1, permCount, "wisp-routed Delete must not touch the labels table")

	var wispEvt, permEvt int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM wisp_events WHERE issue_id = ? AND event_type = ?",
		"bd-lbl-del-cross-wisp", string(types.EventLabelRemoved),
	).Scan(&wispEvt))
	s.Equal(1, wispEvt)
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM events WHERE issue_id = ? AND event_type = ?",
		"bd-lbl-del-cross-wisp", string(types.EventLabelRemoved),
	).Scan(&permEvt))
	s.Equal(0, permEvt, "wisp-routed Delete must record the event in wisp_events, not events")
}
