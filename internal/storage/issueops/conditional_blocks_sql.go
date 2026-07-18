package issueops

import (
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// failureCloseSQLPredicate returns a SQL boolean expression, over the given
// target-table alias, that is TRUE when that row's close_reason indicates a
// FAILURE close — the SQL mirror of types.IsFailureClose (case-insensitive
// substring match over types.FailureCloseKeywords). It is generated from the
// same keyword slice so the two can never drift (beads-a3hm).
//
// IsFailureClose lower-cases the reason then checks strings.Contains for each
// keyword; the SQL side does INSTR(LOWER(<alias>.close_reason), '<keyword>') > 0,
// the exact SQL equivalent of strings.Contains. INSTR is used rather than LIKE
// deliberately: these predicate strings are embedded into UPDATE/COUNT
// templates that are subsequently run through fmt.Sprintf again (to fill the
// IN-clause placeholder), so a literal '%' from a LIKE pattern would be
// misread as a format verb. INSTR contains no '%', so it is Sprintf-safe at
// every embedding site. Keywords are compile-time constants; the only escaping
// needed is doubling single quotes for the SQL string literal (e.g. "won't fix").
func failureCloseSQLPredicate(alias string) string {
	col := fmt.Sprintf("LOWER(%s.close_reason)", alias)
	terms := make([]string, 0, len(types.FailureCloseKeywords))
	for _, kw := range types.FailureCloseKeywords {
		// Keywords are already lower-case; escape single quotes for the literal.
		escaped := strings.ReplaceAll(kw, "'", "''")
		terms = append(terms, fmt.Sprintf("INSTR(%s, '%s') > 0", col, escaped))
	}
	// Non-empty and matches at least one failure keyword.
	return fmt.Sprintf("(%s <> '' AND (%s))", col, strings.Join(terms, " OR "))
}

// activeBlockerSQL returns the SQL boolean expression, over dependency alias d
// and target alias t, that is TRUE when a 'blocks'/'conditional-blocks' edge
// should currently hold the dependent blocked. This is the reason-aware core
// shared by the mark/unmark recompute templates (blocked_state.go), the
// lockstep consistency disjunction (blocked_consistency.go), and the display
// query candidate filter (dependency_queries.go) so all sites agree
// (beads-a3hm; lockstep guarded by bd-hpmw's test).
//
// Semantics:
//   - 'blocks' (hard): blocks while the target is not closed/pinned. Any close
//     unblocks (unchanged behavior).
//   - 'conditional-blocks' ("B runs only if A FAILS"): blocks while the target
//     is not closed/pinned, AND ALSO while the target is closed with a
//     NON-failure (success) reason — because a success close means B's
//     condition is never met, so B stays blocked (must not be surfaced as
//     runnable). A FAILURE close unblocks (B's condition is met).
func activeBlockerSQL(depAlias, targetAlias string) string {
	open := fmt.Sprintf("%s.status <> 'closed' AND %s.status <> 'pinned'", targetAlias, targetAlias)
	closedSuccess := fmt.Sprintf("%s.status = 'closed' AND NOT %s", targetAlias, failureCloseSQLPredicate(targetAlias))
	return fmt.Sprintf(
		"( (%s.type = 'blocks' AND (%s)) OR (%s.type = 'conditional-blocks' AND ((%s) OR (%s))) )",
		depAlias, open, depAlias, open, closedSuccess,
	)
}
