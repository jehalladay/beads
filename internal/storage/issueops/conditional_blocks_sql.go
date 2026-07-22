package issueops

import (
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// failureCloseSQLPredicate returns a SQL boolean expression, over the given
// target-table alias, that is TRUE when that row's close_reason indicates a
// FAILURE close — the SQL mirror of types.IsFailureClose (case-insensitive
// WHOLE-WORD match over types.FailureCloseKeywords). It is generated from the
// same keyword slice so the two can never drift (beads-a3hm).
//
// beads-cwaj5: IsFailureClose was changed from a naive strings.Contains
// substring match to a whole-WORD regexp (failureCloseKeywordRE = `(?i)\b(?:kw…)\b`)
// so a SUCCESS reason that merely embeds a keyword inside a larger word — e.g.
// "unblocked the pipeline" (⊃"blocked"), "no errors found" (⊃"error") — is NOT
// misread as a failure close. This SQL side must mirror that same word-boundary
// semantic or the two drift: the is_blocked recompute (blocked_state.go, driven
// by this predicate) would still release a conditional-blocks dependent on such
// a SUCCESS close while the Go display layer keeps it blocked. So this now uses
// REGEXP with `\b` boundaries (go-mysql-server's REGEXP is backed by
// go-icu-regex, whose default `\b` is the traditional word boundary that matches
// Go's regexp/syntax `\b`), rather than the old INSTR substring test.
//
// Sprintf-safety: these predicate strings are embedded into UPDATE/COUNT
// templates that are subsequently run through fmt.Sprintf again (to fill the
// IN-clause placeholder). The generated REGEXP pattern contains no '%', so it is
// Sprintf-safe at every embedding site (the same property that made INSTR
// preferable to LIKE). The column is LOWER()'d and the keywords are already
// lower-case, so no case-insensitive flag is needed. Keywords are compile-time
// constants; escaping doubles single quotes for the SQL string literal (e.g.
// "won't fix") and the SQL literal backslash for `\b` is written as '\\b'.
func failureCloseSQLPredicate(alias string) string {
	col := fmt.Sprintf("LOWER(%s.close_reason)", alias)
	alts := make([]string, 0, len(types.FailureCloseKeywords))
	for _, kw := range types.FailureCloseKeywords {
		// Keywords are already lower-case; escape single quotes for the SQL
		// string literal. The keywords contain only letters, spaces and an
		// apostrophe — none are regexp metacharacters — so no regexp escaping
		// beyond the SQL-literal quote-doubling is required.
		escaped := strings.ReplaceAll(kw, "'", "''")
		alts = append(alts, escaped)
	}
	// '\\b(?:kw1|kw2|…)\\b' — a single whole-word alternation, mirroring
	// failureCloseKeywordRE. In the SQL string literal a literal backslash is
	// written '\\', so '\\b' delivers the regex token `\b` to REGEXP.
	pattern := `\\b(?:` + strings.Join(alts, "|") + `)\\b`
	// Non-empty and matches at least one failure keyword on a word boundary.
	return fmt.Sprintf("(%s <> '' AND %s REGEXP '%s')", col, col, pattern)
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
