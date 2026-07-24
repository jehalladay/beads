package db

import (
	"github.com/steveyegge/beads/internal/storage/domain"
)

// beads-y3tm3: DOMAIN-path twin of the DIRECT-path label case-fold coherence
// fix (beads-9jjj8, internal/storage/dolt/label_casing_coherence_9jjj8_test.go).
//
// The query/filter side is case-INSENSITIVE everywhere (LOWER(label)=LOWER(?)
// throughout sqlbuild). beads-9jjj8 folded labels at every DIRECT write
// chokepoint (issueops.AddLabelInTx ToLower, issueops.RemoveLabelInTx match on
// LOWER(label)) so add/query/remove agree. The DOMAIN/proxied write path
// (internal/storage/domain/db/label.go labelSQLRepositoryImpl) is a bespoke SQL
// reimplementation, NOT a delegation to those InTx functions: Insert wrote the
// label VERBATIM and Delete matched case-EXACT (WHERE label = ?). So a
// hub-connected (proxiedServerMode, store==nil) crew re-introduced the exact
// three-way divergence 9jjj8 closed — 'FOO' and 'foo' coexist, both surface
// under `--label foo`, and `label remove foo` cannot remove a stored 'FOO' the
// user just found (find-then-cannot-remove trap).
//
// Runs against the real Dolt container (suite_test.go) so the ON-DISK casing +
// the LOWER() DELETE are validated for real. MUTATION-VERIFY: drop the
// strings.ToLower fold from labelSQLRepositoryImpl.Insert and
// InsertFoldsStoredLower / NoCoexistingCaseVariants go RED; revert Delete to a
// case-exact `label = ?` match and DeleteIsCaseInsensitive goes RED.

func (s *testSuite) labelInsertFoldsLower() {
	s.seedIssueRow("bd-y3tm3-add")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-y3tm3-add", "FOO", "tester", domain.LabelOpts{}))

	out, err := r.List(s.Ctx(), "bd-y3tm3-add", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Equal([]string{"foo"}, out,
		"REGRESSION (beads-y3tm3): domain Insert(\"FOO\") must case-fold to \"foo\" to match the case-insensitive query (direct parity with issueops.AddLabelInTx)")
}

func (s *testSuite) labelInsertNoCoexistCase() {
	s.seedIssueRow("bd-y3tm3-dup")
	r := s.labelRepo()
	s.Require().NoError(r.Insert(s.Ctx(), "bd-y3tm3-dup", "Bar", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Insert(s.Ctx(), "bd-y3tm3-dup", "BAR", "tester", domain.LabelOpts{}))

	out, err := r.List(s.Ctx(), "bd-y3tm3-dup", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Equal([]string{"bar"}, out,
		"REGRESSION (beads-y3tm3): domain Insert of 'Bar' then 'BAR' must collapse to a single \"bar\" (case variants must not coexist)")
}

func (s *testSuite) labelDeleteCaseInsensitive() {
	s.seedIssueRow("bd-y3tm3-del")
	r := s.labelRepo()
	// Add via lower (as a folded write would); then remove using a
	// differently-cased arg — the query side would have surfaced the label under
	// either casing, so remove must succeed too.
	s.Require().NoError(r.Insert(s.Ctx(), "bd-y3tm3-del", "flaky", "tester", domain.LabelOpts{}))
	s.Require().NoError(r.Delete(s.Ctx(), "bd-y3tm3-del", "FLAKY", "tester", domain.LabelOpts{}))

	out, err := r.List(s.Ctx(), "bd-y3tm3-del", domain.LabelOpts{})
	s.Require().NoError(err)
	s.Empty(out,
		"REGRESSION (beads-y3tm3): domain Delete must match case-insensitively (LOWER(label)) so `remove FLAKY` clears a stored 'flaky' (direct parity with issueops.RemoveLabelInTx; closes the find-then-cannot-remove trap)")
}
