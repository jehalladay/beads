package db

import (
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// TestIssueUseCase_CreateValidation covers beads-83h3: the proxied-server create
// path (domain create use-case) must enforce the same Issue.Validate invariants
// as the direct/embedded path, instead of persisting malformed issues.
func (s *testSuite) TestIssueUseCase_CreateValidation() {
	s.Run("RejectsOverlongTitle", s.ucCreateRejectsOverlongTitle)
	s.Run("RejectsInvalidStatus", s.ucCreateRejectsInvalidStatus)
	s.Run("RejectsNegativeEstimate", s.ucCreateRejectsNegativeEstimate)
	s.Run("RejectsEphemeralPlusNoHistory", s.ucCreateRejectsEphemeralPlusNoHistory)
	s.Run("RejectsNonClosedWithClosedAt", s.ucCreateRejectsNonClosedWithClosedAt)
	s.Run("AllowsValidClosedIssue", s.ucCreateAllowsValidClosedIssue)
	s.Run("AllowsValidIssue", s.ucCreateAllowsValidIssue)
}

func (s *testSuite) ucCreateRejectsOverlongTitle() {
	s.resetMintConfig("bd", "")
	_, err := s.issueUseCase().CreateIssue(s.Ctx(), domain.CreateIssueParams{
		Issue: &types.Issue{Title: strings.Repeat("x", 501), IssueType: types.TypeTask, Priority: 2},
	}, "tester")
	s.Require().Error(err)
	s.Contains(err.Error(), "title must be 500 characters or less")
}

func (s *testSuite) ucCreateRejectsInvalidStatus() {
	s.resetMintConfig("bd", "")
	_, err := s.issueUseCase().CreateIssue(s.Ctx(), domain.CreateIssueParams{
		Issue: &types.Issue{Title: "bad status", IssueType: types.TypeTask, Priority: 2, Status: types.Status("not-a-real-status")},
	}, "tester")
	s.Require().Error(err)
	s.Contains(err.Error(), "invalid status")
}

func (s *testSuite) ucCreateRejectsNegativeEstimate() {
	s.resetMintConfig("bd", "")
	neg := -5
	_, err := s.issueUseCase().CreateIssue(s.Ctx(), domain.CreateIssueParams{
		Issue: &types.Issue{Title: "neg est", IssueType: types.TypeTask, Priority: 2, EstimatedMinutes: &neg},
	}, "tester")
	s.Require().Error(err)
	s.Contains(err.Error(), "estimated_minutes cannot be negative")
}

func (s *testSuite) ucCreateRejectsEphemeralPlusNoHistory() {
	s.resetMintConfig("bd", "")
	_, err := s.issueUseCase().CreateIssue(s.Ctx(), domain.CreateIssueParams{
		Issue: &types.Issue{Title: "eph+nohist", IssueType: types.TypeTask, Priority: 2, Ephemeral: true, NoHistory: true},
	}, "tester")
	s.Require().Error(err)
}

func (s *testSuite) ucCreateRejectsNonClosedWithClosedAt() {
	s.resetMintConfig("bd", "")
	now := time.Now().UTC()
	_, err := s.issueUseCase().CreateIssue(s.Ctx(), domain.CreateIssueParams{
		Issue: &types.Issue{Title: "open w/ closed_at", IssueType: types.TypeTask, Priority: 2, Status: types.StatusOpen, ClosedAt: &now},
	}, "tester")
	s.Require().Error(err)
	s.Contains(err.Error(), "closed_at")
}

// ucCreateAllowsValidClosedIssue: a `--status closed` create with no closed_at
// must SUCCEED — the create path defaults closed_at (as the direct path does)
// before validating, so the closed_at invariant is satisfied.
func (s *testSuite) ucCreateAllowsValidClosedIssue() {
	s.resetMintConfig("bd", "")
	res, err := s.issueUseCase().CreateIssue(s.Ctx(), domain.CreateIssueParams{
		Issue: &types.Issue{Title: "valid closed", IssueType: types.TypeTask, Priority: 2, Status: types.StatusClosed},
	}, "tester")
	s.Require().NoError(err)
	s.Require().NotNil(res.Issue.ClosedAt, "closed issue should get a defaulted closed_at")
}

func (s *testSuite) ucCreateAllowsValidIssue() {
	s.resetMintConfig("bd", "")
	_, err := s.issueUseCase().CreateIssue(s.Ctx(), domain.CreateIssueParams{
		Issue: &types.Issue{Title: "perfectly fine", IssueType: types.TypeTask, Priority: 2},
	}, "tester")
	s.Require().NoError(err)
}
