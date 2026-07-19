package sqlbuild

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-7f3g: `--status blocked` must map to the derived is_blocked column, not
// the stored status column (which can never equal "blocked" → silent 0). These
// pure builder tests assert the WHERE fragment mirrors bd blocked's is_blocked
// semantics (issueops/blocked.go:247), incl. the closed/pinned exclusion.
func TestBuildIssueFilterClauses_Blocked(t *testing.T) {
	t.Run("blocked=true emits is_blocked=1 with closed/pinned exclusion", func(t *testing.T) {
		b := true
		where, _, err := BuildIssueFilterClauses("", types.IssueFilter{Blocked: &b}, IssuesFilterTables)
		if err != nil {
			t.Fatalf("BuildIssueFilterClauses: %v", err)
		}
		if !hasClause(where, "is_blocked = 1 AND status <> 'closed' AND status <> 'pinned'") {
			t.Errorf("Blocked=true should emit is_blocked=1 clause, got %v", where)
		}
		// Must NOT filter the status column to the literal "blocked" (the bug).
		if hasClause(where, "status = ?") {
			t.Errorf("Blocked filter must not add a status-column clause, got %v", where)
		}
	})

	t.Run("blocked=false emits negated is_blocked", func(t *testing.T) {
		b := false
		where, _, err := BuildIssueFilterClauses("", types.IssueFilter{Blocked: &b}, IssuesFilterTables)
		if err != nil {
			t.Fatalf("BuildIssueFilterClauses: %v", err)
		}
		if !hasClause(where, "(is_blocked = 0 OR is_blocked IS NULL)") {
			t.Errorf("Blocked=false should emit negated is_blocked clause, got %v", where)
		}
	})

	t.Run("nil Blocked emits no is_blocked clause", func(t *testing.T) {
		where, _, err := BuildIssueFilterClauses("", types.IssueFilter{}, IssuesFilterTables)
		if err != nil {
			t.Fatalf("BuildIssueFilterClauses: %v", err)
		}
		for _, c := range where {
			if c == "is_blocked = 1 AND status <> 'closed' AND status <> 'pinned'" ||
				c == "(is_blocked = 0 OR is_blocked IS NULL)" {
				t.Errorf("nil Blocked should emit no is_blocked clause, got %v", where)
			}
		}
	})
}
