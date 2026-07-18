package db

import (
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

func (s *testSuite) TestDependencyUseCase() {
	s.Run("RemoveDependency", func() {
		s.Run("EmptyIDsReturnError", s.ducRemoveDependencyEmptyIDs)
		s.Run("DelegatesToRepoDelete", s.ducRemoveDependencyDelegates)
		s.Run("MissingEdgeIsNoop", s.ducRemoveDependencyMissingNoop)
	})
	s.Run("Reparent", func() {
		s.Run("EmptyChildReturnsError", s.ducReparentEmptyChild)
		s.Run("SelfParentReturnsError", s.ducReparentSelf)
		s.Run("AddsParentWhenNoneExists", s.ducReparentFromNone)
		s.Run("ReplacesExistingParent", s.ducReparentReplaces)
		s.Run("EmptyNewParentUnparents", s.ducReparentUnparent)
		s.Run("SameParentIsNoop", s.ducReparentSameParent)
		s.Run("RejectsParentChildCycle", s.ducReparentRejectsCycle)
	})
	s.Run("CycleCheck", func() {
		s.Run("AddRejectsParentChildCycle", s.ducAddRejectsParentChildCycle)
		s.Run("AddBulkRejectsParentChildCycle", s.ducAddBulkRejectsParentChildCycle)
		s.Run("AddRejectsBlockingCycleStill", s.ducAddRejectsBlockingCycle)
		s.Run("AddAllowsAcyclicParentChild", s.ducAddAllowsAcyclicParentChild)
	})
	s.Run("Wisp", func() {
		s.Run("RemoveWispDependencyRoutesToWispDeps", s.ducRemoveWispDependencyRoutes)
		s.Run("ReparentWispRoutesToWispDeps", s.ducReparentWispRoutes)
	})
}

func (s *testSuite) depUseCase() domain.DependencyUseCase {
	return domain.NewDependencyUseCase(NewDependencySQLRepository(s.Runner()))
}

func (s *testSuite) currentParent(childID string) string {
	res, err := s.depUseCase().ListByIssueIDs(s.Ctx(), []string{childID}, domain.DepListFilter{
		Types:     []types.DependencyType{types.DepParentChild},
		Direction: domain.DepDirectionOut,
	})
	s.Require().NoError(err)
	for _, dep := range res.Outgoing[childID] {
		if dep.Type == types.DepParentChild {
			return dep.DependsOnID
		}
	}
	return ""
}

func (s *testSuite) ducRemoveDependencyEmptyIDs() {
	uc := s.depUseCase()
	s.Require().Error(uc.RemoveDependency(s.Ctx(), "", "bd-x", "tester"))
	s.Require().Error(uc.RemoveDependency(s.Ctx(), "bd-x", "", "tester"))
}

func (s *testSuite) ducRemoveDependencyDelegates() {
	s.seedIssueRow("bd-duc-rd-1")
	s.seedIssueRow("bd-duc-rd-2")
	depRepo := NewDependencySQLRepository(s.Runner())
	s.Require().NoError(depRepo.Insert(s.Ctx(),
		newDep("bd-duc-rd-1", "bd-duc-rd-2", types.DepBlocks), "tester", domain.DepInsertOpts{}))

	s.Require().NoError(s.depUseCase().RemoveDependency(s.Ctx(), "bd-duc-rd-1", "bd-duc-rd-2", "tester"))

	res, err := s.depUseCase().ListByIssueIDs(s.Ctx(), []string{"bd-duc-rd-1"},
		domain.DepListFilter{Direction: domain.DepDirectionOut})
	s.Require().NoError(err)
	s.Empty(res.Outgoing["bd-duc-rd-1"])
}

func (s *testSuite) ducRemoveDependencyMissingNoop() {
	s.seedIssueRow("bd-duc-rd-miss-a")
	s.seedIssueRow("bd-duc-rd-miss-b")
	s.Require().NoError(s.depUseCase().RemoveDependency(s.Ctx(), "bd-duc-rd-miss-a", "bd-duc-rd-miss-b", "tester"))
}

func (s *testSuite) ducReparentEmptyChild() {
	s.Require().Error(s.depUseCase().Reparent(s.Ctx(), "", "bd-x", "tester"))
}

func (s *testSuite) ducReparentSelf() {
	s.Require().Error(s.depUseCase().Reparent(s.Ctx(), "bd-x", "bd-x", "tester"))
}

func (s *testSuite) ducReparentFromNone() {
	s.seedIssueRow("bd-duc-rp-none-c")
	s.seedIssueRow("bd-duc-rp-none-p")

	s.Require().NoError(s.depUseCase().Reparent(s.Ctx(), "bd-duc-rp-none-c", "bd-duc-rp-none-p", "tester"))

	s.Equal("bd-duc-rp-none-p", s.currentParent("bd-duc-rp-none-c"))
}

func (s *testSuite) ducReparentReplaces() {
	s.seedIssueRow("bd-duc-rp-c")
	s.seedIssueRow("bd-duc-rp-old")
	s.seedIssueRow("bd-duc-rp-new")
	depRepo := NewDependencySQLRepository(s.Runner())
	s.Require().NoError(depRepo.Insert(s.Ctx(),
		newDep("bd-duc-rp-c", "bd-duc-rp-old", types.DepParentChild), "tester", domain.DepInsertOpts{}))

	s.Require().NoError(s.depUseCase().Reparent(s.Ctx(), "bd-duc-rp-c", "bd-duc-rp-new", "tester"))

	s.Equal("bd-duc-rp-new", s.currentParent("bd-duc-rp-c"))
}

func (s *testSuite) ducReparentUnparent() {
	s.seedIssueRow("bd-duc-rp-up-c")
	s.seedIssueRow("bd-duc-rp-up-p")
	depRepo := NewDependencySQLRepository(s.Runner())
	s.Require().NoError(depRepo.Insert(s.Ctx(),
		newDep("bd-duc-rp-up-c", "bd-duc-rp-up-p", types.DepParentChild), "tester", domain.DepInsertOpts{}))

	s.Require().NoError(s.depUseCase().Reparent(s.Ctx(), "bd-duc-rp-up-c", "", "tester"))

	s.Equal("", s.currentParent("bd-duc-rp-up-c"))
}

func (s *testSuite) ducReparentSameParent() {
	s.seedIssueRow("bd-duc-rp-same-c")
	s.seedIssueRow("bd-duc-rp-same-p")
	depRepo := NewDependencySQLRepository(s.Runner())
	s.Require().NoError(depRepo.Insert(s.Ctx(),
		newDep("bd-duc-rp-same-c", "bd-duc-rp-same-p", types.DepParentChild), "tester", domain.DepInsertOpts{}))

	s.Require().NoError(s.depUseCase().Reparent(s.Ctx(), "bd-duc-rp-same-c", "bd-duc-rp-same-p", "tester"))

	s.Equal("bd-duc-rp-same-p", s.currentParent("bd-duc-rp-same-c"))
	var count int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? AND depends_on_issue_id = ? AND type = 'parent-child'",
		"bd-duc-rp-same-c", "bd-duc-rp-same-p").Scan(&count))
	s.Equal(1, count)
}

func (s *testSuite) currentWispParent(childID string) string {
	res, err := s.depUseCase().ListByWispIDs(s.Ctx(), []string{childID}, domain.DepListFilter{
		Types:     []types.DependencyType{types.DepParentChild},
		Direction: domain.DepDirectionOut,
	})
	s.Require().NoError(err)
	for _, dep := range res.Outgoing[childID] {
		if dep.Type == types.DepParentChild {
			return dep.DependsOnID
		}
	}
	return ""
}

func (s *testSuite) ducRemoveWispDependencyRoutes() {
	s.seedWispRow("bd-duc-rwd-a")
	s.seedWispRow("bd-duc-rwd-b")
	wispDepRepo := NewDependencySQLRepository(s.Runner())
	s.Require().NoError(wispDepRepo.Insert(s.Ctx(),
		newDep("bd-duc-rwd-a", "bd-duc-rwd-b", types.DepBlocks), "tester", domain.DepInsertOpts{UseWispsTable: true}))

	s.Require().NoError(s.depUseCase().RemoveWispDependency(s.Ctx(), "bd-duc-rwd-a", "bd-duc-rwd-b", "tester"))

	wispRes, err := s.depUseCase().ListByWispIDs(s.Ctx(), []string{"bd-duc-rwd-a"},
		domain.DepListFilter{Direction: domain.DepDirectionOut})
	s.Require().NoError(err)
	s.Empty(wispRes.Outgoing["bd-duc-rwd-a"])
}

func (s *testSuite) ducReparentWispRoutes() {
	s.seedWispRow("bd-duc-rpw-c")
	s.seedWispRow("bd-duc-rpw-old")
	s.seedWispRow("bd-duc-rpw-new")
	depRepo := NewDependencySQLRepository(s.Runner())
	s.Require().NoError(depRepo.Insert(s.Ctx(),
		newDep("bd-duc-rpw-c", "bd-duc-rpw-old", types.DepParentChild), "seeder", domain.DepInsertOpts{UseWispsTable: true}))

	s.Require().NoError(s.depUseCase().ReparentWisp(s.Ctx(), "bd-duc-rpw-c", "bd-duc-rpw-new", "tester"))

	s.Equal("bd-duc-rpw-new", s.currentWispParent("bd-duc-rpw-c"))

	var issuesCount int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM dependencies WHERE issue_id = ?", "bd-duc-rpw-c").Scan(&issuesCount))
	s.Equal(0, issuesCount, "wisp-routed Reparent must not touch the issues dep table")
}

// --- beads-7a6n: parent-child cycle rejection on the proxied/domain dep stack ---
//
// The direct/embedded path rejects a parent-child cycle (beads-8qij); before
// 7a6n the domain use-case gated its cycle check on isBlockingDep, so a
// parent-child edge closing a cycle was accepted here. These exercise the real
// db repo through the use-case (the proxied stack's DependencyUseCase impl).

func (s *testSuite) ducAddRejectsParentChildCycle() {
	// a -> b (parent-child); adding b -> a closes a parent-child cycle.
	s.seedIssueRow("bd-7a6n-a")
	s.seedIssueRow("bd-7a6n-b")
	s.Require().NoError(s.depUseCase().AddDependency(s.Ctx(),
		newDep("bd-7a6n-a", "bd-7a6n-b", types.DepParentChild), "tester"))

	err := s.depUseCase().AddDependency(s.Ctx(),
		newDep("bd-7a6n-b", "bd-7a6n-a", types.DepParentChild), "tester")
	s.Require().Error(err, "parent-child cycle must be rejected (beads-7a6n)")
	s.Contains(err.Error(), "cycle")

	// The rejected edge must not have been inserted.
	var n int
	s.Require().NoError(s.Runner().QueryRowContext(s.Ctx(),
		"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? AND depends_on_issue_id = ?",
		"bd-7a6n-b", "bd-7a6n-a").Scan(&n))
	s.Equal(0, n, "cyclic parent-child edge must not be persisted")
}

func (s *testSuite) ducAddBulkRejectsParentChildCycle() {
	s.seedIssueRow("bd-7a6n-bulk-a")
	s.seedIssueRow("bd-7a6n-bulk-b")
	s.Require().NoError(s.depUseCase().AddDependency(s.Ctx(),
		newDep("bd-7a6n-bulk-a", "bd-7a6n-bulk-b", types.DepParentChild), "tester"))

	_, err := s.depUseCase().AddDependencies(s.Ctx(),
		[]*types.Dependency{newDep("bd-7a6n-bulk-b", "bd-7a6n-bulk-a", types.DepParentChild)},
		"tester", domain.BulkAddDepsOpts{})
	s.Require().Error(err, "bulk parent-child cycle must be rejected (beads-7a6n)")
	s.Contains(err.Error(), "cycle")
}

func (s *testSuite) ducAddRejectsBlockingCycle() {
	// Regression guard: the blocking-family cycle check must still fire after
	// the switch from HasCycle to the family-aware CheckCycleForType.
	s.seedIssueRow("bd-7a6n-blk-a")
	s.seedIssueRow("bd-7a6n-blk-b")
	s.Require().NoError(s.depUseCase().AddDependency(s.Ctx(),
		newDep("bd-7a6n-blk-a", "bd-7a6n-blk-b", types.DepBlocks), "tester"))

	err := s.depUseCase().AddDependency(s.Ctx(),
		newDep("bd-7a6n-blk-b", "bd-7a6n-blk-a", types.DepBlocks), "tester")
	s.Require().Error(err, "blocking cycle must still be rejected")
	s.Contains(err.Error(), "cycle")
}

func (s *testSuite) ducAddAllowsAcyclicParentChild() {
	// A parent-child edge that does NOT close a cycle must still be accepted,
	// and a blocks edge that shares nodes with a parent-child chain must not be
	// spuriously rejected (family isolation).
	s.seedIssueRow("bd-7a6n-ok-a")
	s.seedIssueRow("bd-7a6n-ok-b")
	s.seedIssueRow("bd-7a6n-ok-c")
	s.Require().NoError(s.depUseCase().AddDependency(s.Ctx(),
		newDep("bd-7a6n-ok-a", "bd-7a6n-ok-b", types.DepParentChild), "tester"))
	// b -> c parent-child extends the chain, no cycle.
	s.Require().NoError(s.depUseCase().AddDependency(s.Ctx(),
		newDep("bd-7a6n-ok-b", "bd-7a6n-ok-c", types.DepParentChild), "tester"))
	// A blocks edge c -> a walks a DIFFERENT family (blocking), so even though
	// a ⇝ c exists in the parent-child graph it must not be treated as a cycle.
	s.Require().NoError(s.depUseCase().AddDependency(s.Ctx(),
		newDep("bd-7a6n-ok-c", "bd-7a6n-ok-a", types.DepBlocks), "tester"))
}

func (s *testSuite) ducReparentRejectsCycle() {
	// child a has parent b (a -> b). Reparenting b under a (b -> a) would close
	// a parent-child cycle and must be rejected against the post-delete graph.
	s.seedIssueRow("bd-7a6n-rp-a")
	s.seedIssueRow("bd-7a6n-rp-b")
	s.Require().NoError(s.depUseCase().AddDependency(s.Ctx(),
		newDep("bd-7a6n-rp-a", "bd-7a6n-rp-b", types.DepParentChild), "tester"))

	err := s.depUseCase().Reparent(s.Ctx(), "bd-7a6n-rp-b", "bd-7a6n-rp-a", "tester")
	s.Require().Error(err, "reparent closing a parent-child cycle must be rejected (beads-7a6n)")
	s.Contains(err.Error(), "cycle")
}
