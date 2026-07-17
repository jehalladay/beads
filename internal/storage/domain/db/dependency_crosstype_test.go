package db

import (
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// seedTypedIssueRow seeds an issue with a specific issue_type so cross-type
// blocking (GH#1495) can be exercised on the proxied dep-add path (beads-kzmq).
func (s *testSuite) seedTypedIssueRow(id, issueType string) {
	_, err := s.Runner().ExecContext(s.Ctx(), `
		INSERT INTO issues (id, title, description, design, acceptance_criteria, notes, issue_type)
		VALUES (?, ?, '', '', '', '', ?)
	`, id, "seed", issueType)
	s.Require().NoError(err)
}

// depInsertRejectsTaskBlocksEpic: a task blocking an epic is rejected on the
// proxied path, matching the direct path (issueops.AddDependencyInTx, GH#1495).
func (s *testSuite) depInsertRejectsTaskBlocksEpic() {
	s.seedTypedIssueRow("bd-ct-task-1", string(types.TypeTask))
	s.seedTypedIssueRow("bd-ct-epic-1", string(types.TypeEpic))
	err := s.depRepo().Insert(s.Ctx(),
		newDep("bd-ct-task-1", "bd-ct-epic-1", types.DepBlocks), "tester", domain.DepInsertOpts{})
	s.Require().Error(err)
	s.Contains(err.Error(), "tasks can only block")
}

// depInsertRejectsEpicBlocksTask: an epic blocking a task is rejected.
func (s *testSuite) depInsertRejectsEpicBlocksTask() {
	s.seedTypedIssueRow("bd-ct-epic-2", string(types.TypeEpic))
	s.seedTypedIssueRow("bd-ct-task-2", string(types.TypeTask))
	err := s.depRepo().Insert(s.Ctx(),
		newDep("bd-ct-epic-2", "bd-ct-task-2", types.DepBlocks), "tester", domain.DepInsertOpts{})
	s.Require().Error(err)
	s.Contains(err.Error(), "epics can only block")
}

// depInsertAllowsTaskBlocksTask: same-type (task→task) blocks edge is allowed.
func (s *testSuite) depInsertAllowsTaskBlocksTask() {
	s.seedTypedIssueRow("bd-ct-task-3a", string(types.TypeTask))
	s.seedTypedIssueRow("bd-ct-task-3b", string(types.TypeTask))
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-ct-task-3a", "bd-ct-task-3b", types.DepBlocks), "tester", domain.DepInsertOpts{}))
}

// depInsertAllowsEpicBlocksEpic: same-type (epic→epic) blocks edge is allowed.
func (s *testSuite) depInsertAllowsEpicBlocksEpic() {
	s.seedTypedIssueRow("bd-ct-epic-3a", string(types.TypeEpic))
	s.seedTypedIssueRow("bd-ct-epic-3b", string(types.TypeEpic))
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-ct-epic-3a", "bd-ct-epic-3b", types.DepBlocks), "tester", domain.DepInsertOpts{}))
}

// depInsertAllowsCrossTypeNonBlocks: cross-type blocking only applies to
// "blocks" edges — a related/parent-child edge across types is allowed.
func (s *testSuite) depInsertAllowsCrossTypeNonBlocks() {
	s.seedTypedIssueRow("bd-ct-task-4", string(types.TypeTask))
	s.seedTypedIssueRow("bd-ct-epic-4", string(types.TypeEpic))
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-ct-task-4", "bd-ct-epic-4", types.DepRelated), "tester", domain.DepInsertOpts{}))
}

// depInsertRejectsMissingSource: a dep whose SOURCE issue does not exist is
// rejected (the direct path errors "issue X not found"; the proxied path
// previously inserted a dangling edge — beads-kzmq).
func (s *testSuite) depInsertRejectsMissingSource() {
	s.seedIssueRow("bd-ct-existing-target")
	err := s.depRepo().Insert(s.Ctx(),
		newDep("bd-ct-no-such-source", "bd-ct-existing-target", types.DepBlocks), "tester", domain.DepInsertOpts{})
	s.Require().Error(err)
	s.Contains(err.Error(), "not found")
}
