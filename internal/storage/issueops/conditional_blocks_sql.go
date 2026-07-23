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

// doneStatusInListSQL returns "alias.status IN ('name1', 'name2', …)" for the
// given done-category custom status names, or "" when there are none. The names
// come from ResolveCustomStatusesDetailedInTx filtered to types.CategoryDone and
// each matches types.statusNameRegexp (^[a-z][a-z0-9_-]*$), so they carry no
// quotes, '%', or regexp metacharacters and embed directly as bare SQL string
// literals — Sprintf-safe through the double-Sprintf template pipeline (the same
// property that makes failureCloseSQLPredicate safe).
func doneStatusInListSQL(alias string, doneStatuses []string) string {
	if len(doneStatuses) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(doneStatuses))
	for _, name := range doneStatuses {
		quoted = append(quoted, "'"+name+"'")
	}
	return fmt.Sprintf("%s.status IN (%s)", alias, strings.Join(quoted, ", "))
}

// rowNotDoneClause returns a SQL fragment to APPEND to a mark-eligibility gate
// (which requires the subject row be "open"), excluding done-category rows the
// same way the literal `status <> 'closed' AND status <> 'pinned'` gate excludes
// closed/pinned rows (beads-x463g). A done-category subject is complete, so it
// must not be (re)marked is_blocked=1 — else a done parent would propagate its
// stale block onto parent-child children (p.is_blocked = 1). Returns "" when no
// done statuses are configured, so the gate is byte-identical to pre-x463g. The
// fragment is prefixed with the same newline+indentation as the surrounding
// template so the generated SQL and the coverage assertions stay stable.
func rowNotDoneClause(alias string, doneStatuses []string) string {
	inList := doneStatusInListSQL(alias, doneStatuses)
	if inList == "" {
		return ""
	}
	return "\n\t\t  AND NOT (" + inList + ")"
}

// rowOrDoneClause returns a SQL fragment to APPEND to an unmark-eligibility gate
// (which unmarks when the subject row is closed/pinned OR has no active blocker),
// so a done-category subject is unmarked is_blocked=0 like a closed/pinned row
// (beads-x463g). Returns "" when no done statuses are configured. Prefixed to
// sit inside the `status = 'closed' OR status = 'pinned' OR (...)` disjunction.
func rowOrDoneClause(alias string, doneStatuses []string) string {
	inList := doneStatusInListSQL(alias, doneStatuses)
	if inList == "" {
		return ""
	}
	return " OR " + inList
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
//
// beads-x463g: a custom status in the DONE category is treated exactly like a
// literal success-close in this predicate — done-category names are removed from
// the "open" set (so a 'blocks' edge to a done target UNBLOCKS, like any close)
// AND added to the conditional-blocks success-close set (so a done target keeps
// a 'conditional-blocks' edge blocked, like a non-failure close). doneStatuses
// carries the resolved done-category custom status names; when empty (the
// default, no custom statuses configured) the output is byte-identical to the
// pre-x463g SQL, so the bd-hpmw lockstep and coverage assertions are preserved.
func activeBlockerSQL(depAlias, targetAlias string, doneStatuses []string) string {
	open := fmt.Sprintf("%s.status <> 'closed' AND %s.status <> 'pinned'", targetAlias, targetAlias)
	closedSuccess := fmt.Sprintf("%s.status = 'closed' AND NOT %s", targetAlias, failureCloseSQLPredicate(targetAlias))
	if inList := doneStatusInListSQL(targetAlias, doneStatuses); inList != "" {
		// A done-category target is NOT open (so 'blocks' unblocks) and counts as
		// a success-close (so 'conditional-blocks' stays blocked).
		open = fmt.Sprintf("(%s) AND NOT (%s)", open, inList)
		closedSuccess = fmt.Sprintf("(%s) OR (%s)", closedSuccess, inList)
	}
	return fmt.Sprintf(
		"( (%s.type = 'blocks' AND (%s)) OR (%s.type = 'conditional-blocks' AND ((%s) OR (%s))) )",
		depAlias, open, depAlias, open, closedSuccess,
	)
}
