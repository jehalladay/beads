package db

import (
	"github.com/steveyegge/beads/internal/storage/depid"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// beads-837zn: completes the beads-xaxe option-(b) containment on the single-add
// dependency paths. depid.New keys a dependency edge id on the flattened
// (issue_id, target-string) with no target-kind marker, so an issue-target edge
// and a wisp-target edge that share the same id string derive the SAME CHAR(36)
// primary key while the DB keeps them as distinct edges (uk_dep_issue_target vs
// uk_dep_wisp_target). The batch import path (issueops.PersistDependencies*) was
// hardened in xaxe to detect this collision and surface it; these teeth prove
// the two single-add paths that ALSO run depid.New now do the same instead of
// silently collapsing the second edge onto the first:
//
//   - the direct/embedded path (issueops.AddDependencyInTx), and
//   - the proxied-server path (DependencySQLRepository.Insert).
//
// A wisp-target edge is pre-seeded at depid.New(A, X) in depends_on_wisp_id;
// then an issue-target edge A -> X (issue X exists) is added through each path.
// depid.New(A, X) is identical, so the INSERT hits the existing PK. The edge
// must be REJECTED (surfaced), and the pre-existing wisp-target row must survive
// untouched — the pre-fix flattened-COALESCE pre-check would instead have matched
// the wrong-kind row and either silently refreshed its metadata (same type) or
// returned a misleading "already exists" while dropping this genuinely-distinct
// edge.
func (s *testSuite) TestDependencyCrossKindIDCollision837zn() {
	s.Run("ProxiedInsertSurfacesCrossKindCollision", s.depCrossKindProxiedInsert)
	s.Run("DirectAddDependencySurfacesCrossKindCollision", s.depCrossKindDirectAdd)
	s.Run("ProxiedInsertSameKindStillIdempotent", s.depCrossKindProxiedSameKindIdempotent)
}

// seedCollidingWispTargetRow inserts a wisp-target dependency edge directly into
// the `dependencies` table at depid.New(issueID, target). The main dependencies
// table has no FK on depends_on_wisp_id, so the wisp need not exist as a row —
// only the flattened-PK collision matters. It also verifies the seeded row is
// present so a later "still there" assertion is meaningful.
func (s *testSuite) seedCollidingWispTargetRow(issueID, target string, depType types.DependencyType) {
	_, err := s.Runner().ExecContext(s.Ctx(), `
		INSERT INTO dependencies (id, issue_id, depends_on_wisp_id, type, created_by)
		VALUES (?, ?, ?, ?, ?)
	`, depid.New(issueID, target), issueID, target, string(depType), "tester")
	s.Require().NoError(err, "seed colliding wisp-target row")
}

// wispTargetRowType reads the type of the wisp-target edge at
// depid.New(issueID, target); ok=false if the row is gone.
func (s *testSuite) wispTargetRowType(issueID, target string) (depType string, ok bool) {
	err := s.Runner().QueryRowContext(s.Ctx(),
		"SELECT type FROM dependencies WHERE id = ? AND depends_on_wisp_id = ?",
		depid.New(issueID, target), target,
	).Scan(&depType)
	if err != nil {
		return "", false
	}
	return depType, true
}

func (s *testSuite) depCrossKindProxiedInsert() {
	s.seedIssueRow("bd-837zn-p-src")
	s.seedIssueRow("bd-837zn-p-tgt") // issue X exists → issue-target classification
	s.seedCollidingWispTargetRow("bd-837zn-p-src", "bd-837zn-p-tgt", types.DepBlocks)

	// Adding the issue-target edge A -> X derives the SAME depid.New PK as the
	// pre-seeded wisp-target row → the proxied path must reject it, not collapse.
	err := s.depRepo().Insert(s.Ctx(),
		newDep("bd-837zn-p-src", "bd-837zn-p-tgt", types.DepBlocks), "tester", domain.DepInsertOpts{})
	s.Require().Error(err, "cross-kind PK collision must be surfaced, not silently no-op'd")
	s.Contains(err.Error(), "different target kind")

	// The pre-existing wisp-target edge must be untouched (not silently reclassified).
	dt, ok := s.wispTargetRowType("bd-837zn-p-src", "bd-837zn-p-tgt")
	s.Require().True(ok, "the pre-existing wisp-target row must survive the rejected collision")
	s.Equal(string(types.DepBlocks), dt, "the pre-existing wisp-target edge must keep its type")
}

func (s *testSuite) depCrossKindDirectAdd() {
	s.seedIssueRow("bd-837zn-d-src")
	s.seedIssueRow("bd-837zn-d-tgt")
	s.seedCollidingWispTargetRow("bd-837zn-d-src", "bd-837zn-d-tgt", types.DepBlocks)

	tx := s.beginClassicTx()
	dep := &types.Dependency{IssueID: "bd-837zn-d-src", DependsOnID: "bd-837zn-d-tgt", Type: types.DepBlocks}
	err := issueops.AddDependencyInTx(s.Ctx(), tx, dep, "tester", issueops.AddDependencyOpts{})
	_ = tx.Rollback()
	s.Require().Error(err, "cross-kind PK collision must be surfaced on the direct path, not silently no-op'd")
	s.Contains(err.Error(), "different target kind")

	// The seeded wisp-target row was committed before the (rolled-back) attempt,
	// so it must still be present and unchanged.
	dt, ok := s.wispTargetRowType("bd-837zn-d-src", "bd-837zn-d-tgt")
	s.Require().True(ok, "the pre-existing wisp-target row must survive the rejected collision")
	s.Equal(string(types.DepBlocks), dt)
}

// depCrossKindProxiedSameKindIdempotent is the negative control: a SAME-kind
// re-add (issue-target edge added twice) is a legitimate idempotent no-op and
// must NOT be misreported as a cross-kind collision.
func (s *testSuite) depCrossKindProxiedSameKindIdempotent() {
	s.seedIssueRow("bd-837zn-i-src")
	s.seedIssueRow("bd-837zn-i-tgt")
	dep := newDep("bd-837zn-i-src", "bd-837zn-i-tgt", types.DepBlocks)
	s.Require().NoError(s.depRepo().Insert(s.Ctx(), dep, "tester", domain.DepInsertOpts{}))
	// Re-adding the identical issue-target edge is idempotent (same-kind), not a
	// collision — the kind-discriminated pre-check matches and returns cleanly.
	s.Require().NoError(s.depRepo().Insert(s.Ctx(),
		newDep("bd-837zn-i-src", "bd-837zn-i-tgt", types.DepBlocks), "tester", domain.DepInsertOpts{}),
		"same-kind idempotent re-add must succeed, not be flagged as a cross-kind collision")
}
