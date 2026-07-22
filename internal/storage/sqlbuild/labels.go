package sqlbuild

import (
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// LabelSearchPlan rewrites label predicates (Labels AND-set, LabelsAny) from
// IN-subqueries into JOINs against the labels table, which the optimizer
// handles far better on large corpora. Filter is the input filter with the
// rewritten label fields cleared; callers pass it (not the original) to
// BuildIssueFilterClauses and then merge Where/Args via MergeInto.
type LabelSearchPlan struct {
	FromSQL  string
	Where    []string
	Args     []any
	Distinct bool
	Filter   types.IssueFilter
}

// MergeInto prepends the plan's label clauses to filter-built clauses,
// preserving the historical clause and arg ordering of both stacks.
// Single-use: the result shares the plan's backing arrays, so merging one
// plan into two clause sets could clobber the first result's tail.
func (p LabelSearchPlan) MergeInto(where []string, args []any) ([]string, []any) {
	if len(p.Where) == 0 {
		return where, args
	}
	return append(p.Where, where...), append(p.Args, args...)
}

// BuildLabelDrivenSearch produces the FROM clause (with label JOINs when label
// predicates are present) and the residual filter for a table family.
func BuildLabelDrivenSearch(filter types.IssueFilter, tables FilterTables) LabelSearchPlan {
	labels := CompactNonEmptyStrings(filter.Labels)
	labelsAny := CompactNonEmptyStrings(filter.LabelsAny)
	pattern := strings.TrimSpace(filter.LabelPattern)
	regex := strings.TrimSpace(filter.LabelRegex)
	if len(labels) == 0 && len(labelsAny) == 0 && pattern == "" && regex == "" {
		return LabelSearchPlan{FromSQL: tables.Main, Filter: filter}
	}

	filterForClauses := filter
	filterForClauses.Labels = nil
	filterForClauses.LabelsAny = nil
	filterForClauses.LabelPattern = ""
	filterForClauses.LabelRegex = ""

	var joins, where []string
	var args []any

	// Label matching is case-insensitive (LOWER both sides) to stay consistent
	// with the predicate path (query.buildLabelPredicate uses strings.EqualFold)
	// and with the ExcludeLabels/NoLabels clauses in BuildIssueFilterClauses.
	// Without this the JOIN compare is case-SENSITIVE under the label column
	// collation, so `label=Bug` matched a different set in a simple filter query
	// than in an OR/complex predicate query (beads-hqp8, confirmed via live
	// embedded-dolt).
	for i, label := range labels {
		alias := fmt.Sprintf("label_filter_%d", i)
		joins = append(joins, fmt.Sprintf("JOIN %s %s ON %s.issue_id = %s.id", tables.Labels, alias, alias, tables.Main))
		where = append(where, fmt.Sprintf("LOWER(%s.label) = LOWER(?)", alias))
		args = append(args, label)
	}

	if len(labelsAny) > 0 {
		alias := "label_filter_any"
		joins = append(joins, fmt.Sprintf("JOIN %s %s ON %s.issue_id = %s.id", tables.Labels, alias, alias, tables.Main))
		placeholders := make([]string, len(labelsAny))
		for i, label := range labelsAny {
			placeholders[i] = "LOWER(?)"
			args = append(args, label)
		}
		where = append(where, fmt.Sprintf("LOWER(%s.label) IN (%s)", alias, strings.Join(placeholders, ", ")))
	}

	// --label-pattern: glob match (e.g. "tech-*"). Translate the glob to a SQL
	// LIKE pattern and match case-insensitively, consistent with the exact/any
	// label JOINs above. Without this the flag flowed into the filter but no
	// query consumed it, so it was silently ignored and returned everything
	// (beads-v5i7).
	if pattern != "" {
		alias := "label_filter_pattern"
		joins = append(joins, fmt.Sprintf("JOIN %s %s ON %s.issue_id = %s.id", tables.Labels, alias, alias, tables.Main))
		// ESCAPE '\\' so the backslash-escapes globToLike emits for literal
		// '%'/'_'/'\' actually match literally — go-mysql-server has NO default
		// LIKE escape char, so without this a literal '_' or '%' in a pattern is
		// silently treated as a wildcard (beads-k3xye; mirrors beads-b9ova which
		// added ESCAPE to the free-text title/description/notes clauses but
		// missed the two label-pattern clauses).
		where = append(where, fmt.Sprintf("LOWER(%s.label) LIKE LOWER(?) ESCAPE '\\\\'", alias))
		args = append(args, globToLike(pattern))
	}

	// --label-regex: regex match (e.g. "tech-(debt|legacy)") via SQL REGEXP.
	// Also previously silently ignored (beads-v5i7).
	if regex != "" {
		alias := "label_filter_regex"
		joins = append(joins, fmt.Sprintf("JOIN %s %s ON %s.issue_id = %s.id", tables.Labels, alias, alias, tables.Main))
		where = append(where, fmt.Sprintf("%s.label REGEXP ?", alias))
		args = append(args, regex)
	}

	return LabelSearchPlan{
		FromSQL:  tables.Main + " " + strings.Join(joins, " "),
		Where:    where,
		Args:     args,
		Distinct: true,
		Filter:   filterForClauses,
	}
}

// globToLike converts a shell-style glob (as accepted by --label-pattern) into
// a SQL LIKE pattern: '*' -> '%' (any run) and '?' -> '_' (any single char).
// Literal SQL LIKE wildcards ('%', '_') and the escape char ('\') in the input
// are backslash-escaped so they match literally. This REQUIRES the consuming
// LIKE clause to carry an explicit ESCAPE '\' — go-mysql-server has NO default
// LIKE escape char, so without it the backslashes match literally and the
// intended metachar still acts as a wildcard (beads-v5i7; escape wired into the
// label clauses by beads-k3xye).
func globToLike(glob string) string {
	var b strings.Builder
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteByte('%')
		case '?':
			b.WriteByte('_')
		case '%', '_', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
