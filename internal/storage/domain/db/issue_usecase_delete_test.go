package db

import (
	"strings"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

func (s *testSuite) TestIssueUseCase_Delete() {
	s.Run("DeleteIssue", func() {
		s.Run("EmptyIDReturnsError", s.iucDeleteEmptyID)
		s.Run("RemovesRowAndDeps", s.iucDeleteRemovesRowAndDeps)
		s.Run("CascadesAcrossDepTypes", s.iucDeleteCascades)
		s.Run("RewritesTextReferencesInNeighbors", s.iucDeleteRewritesRefs)
		s.Run("RewritesTitleReferenceInNeighbor", s.iucDeleteRewritesTitleRef)
		s.Run("RewritesTitleAndDescriptionTogether", s.iucDeleteRewritesTitleAndDesc)
		s.Run("RewritesTitleForMultipleDeletedIDs", s.iucDeleteRewritesTitleMultiID)
		s.Run("LeavesUnconnectedTitleRefAlone", s.iucDeleteLeavesUnconnectedTitle)
		s.Run("RecomputesIsBlockedOnAffected", s.iucDeleteRecomputesBlocked)
	})
	s.Run("DeleteIssues", func() {
		s.Run("EmptyIDsIsNoop", s.iucDeleteIssuesEmpty)
		s.Run("DryRunCountsButDoesNotDelete", s.iucDeleteIssuesDryRun)
		s.Run("CleansLabelsAndEvents", s.iucDeleteCleansAuxiliaryTables)
		s.Run("UpdateTextReferencesFalseLeavesRefs", s.iucDeleteSkipsRefsWhenFlagOff)
		s.Run("CascadeFalseLeavesDependents", s.iucDeleteIssuesNoCascadeKeepsDependents)
		s.Run("CascadeTrueRemovesDependents", s.iucDeleteIssuesCascadeRemovesDependents)
	})
	s.Run("DeleteWisp", func() {
		s.Run("DispatchesToWispsTable", s.iucDeleteWispDispatches)
	})
	s.Run("PreviewDelete", func() {
		s.Run("EmptyInputReturnsEmpty", s.iucPreviewEmpty)
		s.Run("PopulatesIssuesNotFoundAndConnected", s.iucPreviewPopulates)
		s.Run("DoesNotMutate", s.iucPreviewIsReadOnly)
	})
	s.Run("PreviewDeleteWisp", func() {
		s.Run("PopulatesFromWispsTable", s.iucPreviewWisp)
	})
}

func (s *testSuite) iucDeleteEmptyID() {
	_, err := s.issueUseCase().DeleteIssue(s.Ctx(), "", "tester")
	s.Require().Error(err)
}

func (s *testSuite) iucDeleteRemovesRowAndDeps() {
	s.seedOpenIssue("bd-iuc-del-a")
	s.seedOpenIssue("bd-iuc-del-b")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-del-a", "bd-iuc-del-b", types.DepBlocks), "tester", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssue(s.Ctx(), "bd-iuc-del-a", "tester")
	s.Require().NoError(err)
	s.Equal(1, res.DeletedCount)
	s.Equal(1, res.DependenciesCount, "the A->B edge must be counted")

	var rows int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM issues WHERE id = ?", "bd-iuc-del-a").Scan(&rows))
	s.Equal(0, rows)
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? OR depends_on_issue_id = ?",
		"bd-iuc-del-a", "bd-iuc-del-a").Scan(&rows))
	s.Equal(0, rows)
}

func (s *testSuite) iucDeleteCascades() {
	s.seedOpenIssue("bd-iuc-cas-root")
	s.seedOpenIssue("bd-iuc-cas-mid")
	s.seedOpenIssue("bd-iuc-cas-leaf")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-cas-mid", "bd-iuc-cas-root", types.DepBlocks), "tester", domain.DepInsertOpts{}))
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-cas-leaf", "bd-iuc-cas-mid", types.DepBlocks), "tester", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssue(s.Ctx(), "bd-iuc-cas-root", "tester")
	s.Require().NoError(err)
	s.Equal(3, res.DeletedCount, "root + mid + leaf")

	for _, id := range []string{"bd-iuc-cas-root", "bd-iuc-cas-mid", "bd-iuc-cas-leaf"} {
		var rows int
		s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
			"SELECT COUNT(*) FROM issues WHERE id = ?", id).Scan(&rows))
		s.Equal(0, rows, "%s should be deleted", id)
	}
}

func (s *testSuite) iucDeleteRewritesRefs() {
	s.seedOpenIssue("bd-iuc-ref-target")
	s.seedOpenIssue("bd-iuc-ref-neighbor")
	s.Require().NoError(s.issueRepo().Update(s.Ctx(), "bd-iuc-ref-neighbor",
		map[string]any{"description": "see bd-iuc-ref-target for context"},
		"seeder", domain.IssueTableOpts{}))
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-ref-target", "bd-iuc-ref-neighbor", types.DepRelated), "tester", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssue(s.Ctx(), "bd-iuc-ref-target", "tester")
	s.Require().NoError(err)
	s.GreaterOrEqual(res.ReferencesUpdated, 1)

	updated, err := s.issueRepo().Get(s.Ctx(), "bd-iuc-ref-neighbor", domain.IssueTableOpts{})
	s.Require().NoError(err)
	s.True(strings.Contains(updated.Description, "[deleted:bd-iuc-ref-target]"),
		"neighbor description should be rewritten; got %q", updated.Description)
}

// iucDeleteRewritesTitleRef is the beads-989m0 regression guard: a connected
// neighbor that references the deleted id ONLY in its title must have that
// title tombstoned, matching the rename.go / rename_prefix.go twins. Before the
// fix rewriteTextReferences skipped the title field entirely, leaving a dangling
// live reference in the field shown in every list/ready/blocked/show view.
func (s *testSuite) iucDeleteRewritesTitleRef() {
	s.seedOpenIssue("bd-iuc-tref-target")
	s.seedOpenIssue("bd-iuc-tref-neighbor")
	s.Require().NoError(s.issueRepo().Update(s.Ctx(), "bd-iuc-tref-neighbor",
		map[string]any{"title": "Fix regression from bd-iuc-tref-target"},
		"seeder", domain.IssueTableOpts{}))
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-tref-target", "bd-iuc-tref-neighbor", types.DepRelated),
		"tester", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssue(s.Ctx(), "bd-iuc-tref-target", "tester")
	s.Require().NoError(err)
	s.GreaterOrEqual(res.ReferencesUpdated, 1, "the title-only reference must be counted")

	updated, err := s.issueRepo().Get(s.Ctx(), "bd-iuc-tref-neighbor", domain.IssueTableOpts{})
	s.Require().NoError(err)
	s.True(strings.Contains(updated.Title, "[deleted:bd-iuc-tref-target]"),
		"neighbor title should be rewritten; got %q", updated.Title)
}

// iucDeleteRewritesTitleAndDesc proves both fields are tombstoned in one delete
// when the neighbor references the deleted id in title AND description — no
// field is left dangling.
func (s *testSuite) iucDeleteRewritesTitleAndDesc() {
	s.seedOpenIssue("bd-iuc-tboth-target")
	s.seedOpenIssue("bd-iuc-tboth-neighbor")
	s.Require().NoError(s.issueRepo().Update(s.Ctx(), "bd-iuc-tboth-neighbor",
		map[string]any{
			"title":       "Fix regression from bd-iuc-tboth-target",
			"description": "Root cause traced to bd-iuc-tboth-target.",
		},
		"seeder", domain.IssueTableOpts{}))
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-tboth-target", "bd-iuc-tboth-neighbor", types.DepRelated),
		"tester", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssue(s.Ctx(), "bd-iuc-tboth-target", "tester")
	s.Require().NoError(err)
	s.GreaterOrEqual(res.ReferencesUpdated, 1)

	updated, err := s.issueRepo().Get(s.Ctx(), "bd-iuc-tboth-neighbor", domain.IssueTableOpts{})
	s.Require().NoError(err)
	s.True(strings.Contains(updated.Title, "[deleted:bd-iuc-tboth-target]"),
		"title should be rewritten; got %q", updated.Title)
	s.True(strings.Contains(updated.Description, "[deleted:bd-iuc-tboth-target]"),
		"description should be rewritten; got %q", updated.Description)
}

// iucDeleteRewritesTitleMultiID proves the in-memory conn.Title mirror keeps
// multi-deleted-ID correctness: a neighbor whose title references two deleted
// ids gets BOTH tombstoned in the single delete pass (a later deletedID pass
// must see the already-rewritten title, same as the desc/notes/design/ac
// mirrors).
func (s *testSuite) iucDeleteRewritesTitleMultiID() {
	s.seedOpenIssue("bd-iuc-tmul-x")
	s.seedOpenIssue("bd-iuc-tmul-y")
	s.seedOpenIssue("bd-iuc-tmul-neighbor")
	s.Require().NoError(s.issueRepo().Update(s.Ctx(), "bd-iuc-tmul-neighbor",
		map[string]any{"title": "Supersedes bd-iuc-tmul-x and bd-iuc-tmul-y"},
		"seeder", domain.IssueTableOpts{}))
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-tmul-x", "bd-iuc-tmul-neighbor", types.DepRelated),
		"tester", domain.DepInsertOpts{}))
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-tmul-y", "bd-iuc-tmul-neighbor", types.DepRelated),
		"tester", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssues(s.Ctx(), domain.DeleteIssuesParams{
		IDs:                  []string{"bd-iuc-tmul-x", "bd-iuc-tmul-y"},
		UpdateTextReferences: true,
		Cascade:              false,
	}, "tester")
	s.Require().NoError(err)
	s.GreaterOrEqual(res.ReferencesUpdated, 1)

	updated, err := s.issueRepo().Get(s.Ctx(), "bd-iuc-tmul-neighbor", domain.IssueTableOpts{})
	s.Require().NoError(err)
	s.True(strings.Contains(updated.Title, "[deleted:bd-iuc-tmul-x]"),
		"first deleted id must be tombstoned in title; got %q", updated.Title)
	s.True(strings.Contains(updated.Title, "[deleted:bd-iuc-tmul-y]"),
		"second deleted id must be tombstoned in title; got %q", updated.Title)
}

// iucDeleteLeavesUnconnectedTitle preserves the deliberate dep-connected
// scoping (issue_delete.go:63-65 / beads-rir3): a bead that references the
// deleted id in its title but is NOT dependency-connected must be left alone.
func (s *testSuite) iucDeleteLeavesUnconnectedTitle() {
	s.seedOpenIssue("bd-iuc-tunc-target")
	s.seedOpenIssue("bd-iuc-tunc-stranger")
	original := "Related to bd-iuc-tunc-target somehow"
	s.Require().NoError(s.issueRepo().Update(s.Ctx(), "bd-iuc-tunc-stranger",
		map[string]any{"title": original},
		"seeder", domain.IssueTableOpts{}))
	// No dependency edge → not a connected neighbor.

	_, err := s.issueUseCase().DeleteIssue(s.Ctx(), "bd-iuc-tunc-target", "tester")
	s.Require().NoError(err)

	survived, err := s.issueRepo().Get(s.Ctx(), "bd-iuc-tunc-stranger", domain.IssueTableOpts{})
	s.Require().NoError(err)
	s.Equal(original, survived.Title,
		"an unconnected bead's title must not be rewritten (dep-connected scoping)")
}

func (s *testSuite) iucDeleteRecomputesBlocked() {
	s.seedOpenIssue("bd-iuc-rib-blocker")
	s.seedOpenIssue("bd-iuc-rib-depender")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-rib-depender", "bd-iuc-rib-blocker", types.DepBlocks),
		"seeder", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssue(s.Ctx(), "bd-iuc-rib-blocker", "tester")
	s.Require().NoError(err)
	s.Equal(2, res.DeletedCount, "blocker + depender (cascade)")
}

func (s *testSuite) iucDeleteIssuesEmpty() {
	res, err := s.issueUseCase().DeleteIssues(s.Ctx(),
		domain.DeleteIssuesParams{}, "tester")
	s.Require().NoError(err)
	s.Equal(0, res.DeletedCount)
}

func (s *testSuite) iucDeleteIssuesDryRun() {
	s.seedOpenIssue("bd-iuc-dry-a")
	s.seedOpenIssue("bd-iuc-dry-b")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-dry-a", "bd-iuc-dry-b", types.DepBlocks), "tester", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssues(s.Ctx(), domain.DeleteIssuesParams{
		IDs:    []string{"bd-iuc-dry-a"},
		DryRun: true,
	}, "tester")
	s.Require().NoError(err)
	s.Equal(0, res.DeletedCount, "DryRun must not actually delete")
	s.Equal(1, res.DependenciesCount)

	var rows int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM issues WHERE id = ?", "bd-iuc-dry-a").Scan(&rows))
	s.Equal(1, rows, "row must still exist after DryRun")
}

func (s *testSuite) iucDeleteCleansAuxiliaryTables() {
	s.seedOpenIssue("bd-iuc-aux-a")
	s.Require().NoError(s.labelRepo().Insert(s.Ctx(),
		"bd-iuc-aux-a", "tag1", "tester", domain.LabelOpts{}))
	s.Require().NoError(s.labelRepo().Insert(s.Ctx(),
		"bd-iuc-aux-a", "tag2", "tester", domain.LabelOpts{}))
	s.Require().NoError(s.eventsRepo().Record(s.Ctx(),
		domain.Event{IssueID: "bd-iuc-aux-a", Type: types.EventCreated, Actor: "tester"},
		domain.RecordEventOpts{}))

	res, err := s.issueUseCase().DeleteIssue(s.Ctx(), "bd-iuc-aux-a", "tester")
	s.Require().NoError(err)
	s.Equal(2, res.LabelsCount)
	s.GreaterOrEqual(res.EventsCount, 1)

	var rows int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM labels WHERE issue_id = ?", "bd-iuc-aux-a").Scan(&rows))
	s.Equal(0, rows)
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM events WHERE issue_id = ?", "bd-iuc-aux-a").Scan(&rows))
	s.Equal(0, rows)
}

func (s *testSuite) iucDeleteSkipsRefsWhenFlagOff() {
	s.seedOpenIssue("bd-iuc-noref-target")
	s.seedOpenIssue("bd-iuc-noref-neighbor")
	original := "links bd-iuc-noref-target here"
	s.Require().NoError(s.issueRepo().Update(s.Ctx(), "bd-iuc-noref-neighbor",
		map[string]any{"description": original},
		"seeder", domain.IssueTableOpts{}))
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-noref-target", "bd-iuc-noref-neighbor", types.DepRelated),
		"tester", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssues(s.Ctx(), domain.DeleteIssuesParams{
		IDs:                  []string{"bd-iuc-noref-target"},
		UpdateTextReferences: false,
	}, "tester")
	s.Require().NoError(err)
	s.Equal(0, res.ReferencesUpdated)

	survived, err := s.issueRepo().Get(s.Ctx(), "bd-iuc-noref-neighbor", domain.IssueTableOpts{})
	s.Require().NoError(err)
	s.Equal(original, survived.Description, "description must be untouched when flag is off")
}

// iucDeleteIssuesNoCascadeKeepsDependents is the beads-rir3 regression guard:
// DeleteIssues with Cascade=false must delete ONLY the named IDs and leave
// dependents alive (rewriting their refs to [deleted:X]), matching direct-mode
// default semantics. Before the fix, deleteMany always FindAllDependents-expanded
// so the dependent was also deleted (silent data loss).
func (s *testSuite) iucDeleteIssuesNoCascadeKeepsDependents() {
	s.seedOpenIssue("bd-iuc-nocas-root")
	s.seedOpenIssue("bd-iuc-nocas-dependent")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-nocas-dependent", "bd-iuc-nocas-root", types.DepBlocks),
		"tester", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssues(s.Ctx(), domain.DeleteIssuesParams{
		IDs:                  []string{"bd-iuc-nocas-root"},
		UpdateTextReferences: true,
		Cascade:              false,
	}, "tester")
	s.Require().NoError(err)
	s.Equal(1, res.DeletedCount, "only the named root must be deleted, not the dependent")

	var rootRows, depRows int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM issues WHERE id = ?", "bd-iuc-nocas-root").Scan(&rootRows))
	s.Equal(0, rootRows, "root should be deleted")
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM issues WHERE id = ?", "bd-iuc-nocas-dependent").Scan(&depRows))
	s.Equal(1, depRows, "dependent must survive a non-cascade delete (beads-rir3)")
}

// iucDeleteIssuesCascadeRemovesDependents proves Cascade=true still expands to
// dependents (the opt-in cascade path remains intact).
func (s *testSuite) iucDeleteIssuesCascadeRemovesDependents() {
	s.seedOpenIssue("bd-iuc-cas2-root")
	s.seedOpenIssue("bd-iuc-cas2-dependent")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-cas2-dependent", "bd-iuc-cas2-root", types.DepBlocks),
		"tester", domain.DepInsertOpts{}))

	res, err := s.issueUseCase().DeleteIssues(s.Ctx(), domain.DeleteIssuesParams{
		IDs:                  []string{"bd-iuc-cas2-root"},
		UpdateTextReferences: true,
		Cascade:              true,
	}, "tester")
	s.Require().NoError(err)
	s.Equal(2, res.DeletedCount, "root + dependent under cascade")

	for _, id := range []string{"bd-iuc-cas2-root", "bd-iuc-cas2-dependent"} {
		var rows int
		s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
			"SELECT COUNT(*) FROM issues WHERE id = ?", id).Scan(&rows))
		s.Equal(0, rows, "%s should be deleted under cascade", id)
	}
}

func (s *testSuite) iucDeleteWispDispatches() {
	s.seedOpenWisp("bd-iuc-delw-1")
	s.seedOpenIssue("bd-iuc-delw-1")

	res, err := s.issueUseCase().DeleteWisp(s.Ctx(), "bd-iuc-delw-1", "tester")
	s.Require().NoError(err)
	s.Equal(1, res.DeletedCount)

	var wispRows, issueRows int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM wisps WHERE id = ?", "bd-iuc-delw-1").Scan(&wispRows))
	s.Equal(0, wispRows)
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM issues WHERE id = ?", "bd-iuc-delw-1").Scan(&issueRows))
	s.Equal(1, issueRows, "issues row with shadowed ID must remain")
}

func (s *testSuite) iucPreviewEmpty() {
	out, err := s.issueUseCase().PreviewDelete(s.Ctx(), nil)
	s.Require().NoError(err)
	s.Empty(out.Issues)
	s.Empty(out.ConnectedIssues)
	s.Empty(out.NotFound)
}

func (s *testSuite) iucPreviewPopulates() {
	s.seedOpenIssue("bd-iuc-pv-target")
	s.seedOpenIssue("bd-iuc-pv-neighbor")
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-iuc-pv-target", "bd-iuc-pv-neighbor", types.DepBlocks),
		"seeder", domain.DepInsertOpts{}))

	out, err := s.issueUseCase().PreviewDelete(s.Ctx(),
		[]string{"bd-iuc-pv-target", "bd-iuc-pv-missing"})
	s.Require().NoError(err)
	s.Contains(out.Issues, "bd-iuc-pv-target")
	s.Equal([]string{"bd-iuc-pv-missing"}, out.NotFound)
	s.Contains(out.ConnectedIssues, "bd-iuc-pv-neighbor")
	s.Require().Len(out.DepRecords["bd-iuc-pv-target"], 1)
	s.Equal("bd-iuc-pv-neighbor", out.DepRecords["bd-iuc-pv-target"][0].DependsOnID)
}

func (s *testSuite) iucPreviewIsReadOnly() {
	s.seedOpenIssue("bd-iuc-pvro")
	_, err := s.issueUseCase().PreviewDelete(s.Ctx(), []string{"bd-iuc-pvro"})
	s.Require().NoError(err)

	got, err := s.issueRepo().Get(s.Ctx(), "bd-iuc-pvro", domain.IssueTableOpts{})
	s.Require().NoError(err)
	s.Equal("bd-iuc-pvro", got.ID, "preview must not mutate")
}

func (s *testSuite) iucPreviewWisp() {
	s.seedOpenWisp("bd-iuc-pvw")
	out, err := s.issueUseCase().PreviewDeleteWisp(s.Ctx(), []string{"bd-iuc-pvw"})
	s.Require().NoError(err)
	s.Contains(out.Issues, "bd-iuc-pvw", "wisp target should be hydrated from wisps table")
	s.Empty(out.NotFound)
}
