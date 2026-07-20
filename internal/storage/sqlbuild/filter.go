package sqlbuild

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// EscapeLikePattern escapes the LIKE wildcard metacharacters (% and _) plus the
// escape character itself (backslash) in a user-supplied substring, so the
// substring is matched LITERALLY inside a `LIKE '%'+s+'%'` operand (beads-b9ova).
// Without this a user searching --title-contains '%' (or '_') has their input
// act as a wildcard and over-matches every row. Callers must pair the escaped
// value with an `ESCAPE '\\'` clause on the LIKE (see LikeContains). Backslash is
// escaped FIRST so the backslashes introduced for % and _ are not re-escaped.
// Exported so the dolt-package in-tx read path (transaction.go) shares one
// implementation instead of a drifting copy.
func EscapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// LikeContains returns the `%`-wrapped, metachar-escaped LIKE operand for a
// case-insensitive substring match (used with `LOWER(col) LIKE ? ESCAPE '\\'`).
func LikeContains(s string) string {
	return "%" + EscapeLikePattern(strings.ToLower(s)) + "%"
}

// BuildIssueFilterClauses builds WHERE clause fragments and args from a query
// string and IssueFilter. The tables parameter controls which table names are
// referenced in subqueries (issues vs wisps).
func BuildIssueFilterClauses(query string, filter types.IssueFilter, tables FilterTables) ([]string, []any, error) {
	var whereClauses []string
	var args []any

	if query != "" {
		lowerQuery := strings.ToLower(query)
		// beads-b9ova: escape LIKE metachars in the user's query + ESCAPE '\\' so a
		// bare %/_ matches literally instead of acting as a wildcard. The id-prefix
		// leg (id LIKE lowerQuery+'%') is escaped too so a query with %/_ doesn't
		// over-match on the id column either; the trailing '%' stays a wildcard.
		escaped := EscapeLikePattern(lowerQuery)
		if LooksLikeIssueID(query) {
			whereClauses = append(whereClauses, `(id = ? OR id LIKE ? ESCAPE '\\' OR LOWER(title) LIKE ? ESCAPE '\\' OR LOWER(external_ref) LIKE ? ESCAPE '\\')`)
			args = append(args, lowerQuery, escaped+"%", "%"+escaped+"%", "%"+escaped+"%")
		} else {
			whereClauses = append(whereClauses, `(LOWER(title) LIKE ? ESCAPE '\\' OR id LIKE ? ESCAPE '\\')`)
			pattern := "%" + escaped + "%"
			args = append(args, pattern, pattern)
		}
	}

	if filter.TitleSearch != "" {
		whereClauses = append(whereClauses, `LOWER(title) LIKE ? ESCAPE '\\'`)
		args = append(args, LikeContains(filter.TitleSearch))
	}
	if filter.TitleContains != "" {
		whereClauses = append(whereClauses, `LOWER(title) LIKE ? ESCAPE '\\'`)
		args = append(args, LikeContains(filter.TitleContains))
	}
	if filter.DescriptionContains != "" {
		whereClauses = append(whereClauses, `LOWER(description) LIKE ? ESCAPE '\\'`)
		args = append(args, LikeContains(filter.DescriptionContains))
	}
	if filter.NotesContains != "" {
		whereClauses = append(whereClauses, `LOWER(notes) LIKE ? ESCAPE '\\'`)
		args = append(args, LikeContains(filter.NotesContains))
	}
	if filter.ExternalRefContains != "" {
		whereClauses = append(whereClauses, `LOWER(external_ref) LIKE ? ESCAPE '\\'`)
		args = append(args, LikeContains(filter.ExternalRefContains))
	}

	if filter.Status != nil {
		whereClauses = append(whereClauses, "status = ?")
		args = append(args, *filter.Status)
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, s := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, string(s))
		}
		whereClauses = append(whereClauses, fmt.Sprintf("status IN (%s)", strings.Join(placeholders, ",")))
	}
	if len(filter.ExcludeStatus) > 0 {
		placeholders := make([]string, len(filter.ExcludeStatus))
		for i, s := range filter.ExcludeStatus {
			placeholders[i] = "?"
			args = append(args, string(s))
		}
		whereClauses = append(whereClauses, fmt.Sprintf("status NOT IN (%s)", strings.Join(placeholders, ",")))
	}

	if filter.IssueType != nil {
		whereClauses = append(whereClauses, "issue_type = ?")
		args = append(args, *filter.IssueType)
	}
	if len(filter.ExcludeTypes) > 0 {
		placeholders := make([]string, len(filter.ExcludeTypes))
		for i, t := range filter.ExcludeTypes {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		whereClauses = append(whereClauses, fmt.Sprintf("issue_type NOT IN (%s)", strings.Join(placeholders, ",")))
	}

	if filter.Assignee != nil {
		// Case-insensitive to match the predicate path (buildAssigneePredicate
		// uses strings.EqualFold), so `assignee=Alice` returns the same set in a
		// simple filter query as in an OR/complex predicate query (beads-xl4k,
		// sibling of the label-case fix beads-hqp8; owner is predicate-only so
		// it is already consistent).
		whereClauses = append(whereClauses, "LOWER(assignee) = LOWER(?)")
		args = append(args, *filter.Assignee)
	}

	if filter.Priority != nil {
		whereClauses = append(whereClauses, "priority = ?")
		args = append(args, *filter.Priority)
	}
	if filter.PriorityMin != nil {
		whereClauses = append(whereClauses, "priority >= ?")
		args = append(args, *filter.PriorityMin)
	}
	if filter.PriorityMax != nil {
		whereClauses = append(whereClauses, "priority <= ?")
		args = append(args, *filter.PriorityMax)
	}
	if len(filter.ExcludePriority) > 0 {
		placeholders := make([]string, len(filter.ExcludePriority))
		for i, p := range filter.ExcludePriority {
			placeholders[i] = "?"
			args = append(args, p)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("priority NOT IN (%s)", strings.Join(placeholders, ",")))
	}

	if len(filter.IDs) > 0 {
		placeholders := make([]string, len(filter.IDs))
		for i, id := range filter.IDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("id IN (%s)", strings.Join(placeholders, ", ")))
	}
	if filter.IDPrefix != "" {
		whereClauses = append(whereClauses, "id LIKE ?")
		args = append(args, filter.IDPrefix+"%")
	}
	if filter.SpecIDPrefix != "" {
		whereClauses = append(whereClauses, "spec_id LIKE ?")
		args = append(args, filter.SpecIDPrefix+"%")
	}

	if filter.ParentID != nil {
		parentID := *filter.ParentID
		whereClauses = append(whereClauses, fmt.Sprintf("(id IN (SELECT issue_id FROM %s WHERE type = 'parent-child' AND %s = ?) OR (id LIKE CONCAT(?, '.%%') AND id NOT IN (SELECT issue_id FROM %s WHERE type = 'parent-child')))", tables.Dependencies, DepTargetExpr, tables.Dependencies))
		args = append(args, parentID, parentID)
	}
	if filter.NoParent {
		whereClauses = append(whereClauses, fmt.Sprintf("id NOT IN (SELECT issue_id FROM %s WHERE type = 'parent-child')", tables.Dependencies))
	}

	if filter.MolType != nil {
		whereClauses = append(whereClauses, "mol_type = ?")
		args = append(args, string(*filter.MolType))
	}
	if filter.WispType != nil {
		whereClauses = append(whereClauses, "wisp_type = ?")
		args = append(args, string(*filter.WispType))
	}

	// Label matching is case-insensitive to stay consistent with the predicate
	// path (query.buildLabelPredicate uses strings.EqualFold). Without LOWER()
	// on both sides the SQL compare is case-SENSITIVE under the label column's
	// collation, so `label=Bug` matched a different set in a simple filter query
	// than in an OR/complex predicate query (beads-hqp8, confirmed via live
	// embedded-dolt). LOWER(?) is applied to the bound value too so callers pass
	// the label verbatim. (Matches how TitleContains/DescriptionContains already
	// lowercase both sides.)
	if len(filter.Labels) > 0 {
		for _, label := range filter.Labels {
			whereClauses = append(whereClauses, fmt.Sprintf("id IN (SELECT issue_id FROM %s WHERE LOWER(label) = LOWER(?))", tables.Labels))
			args = append(args, label)
		}
	}
	if len(filter.LabelsAny) > 0 {
		placeholders := make([]string, len(filter.LabelsAny))
		for i, label := range filter.LabelsAny {
			placeholders[i] = "LOWER(?)"
			args = append(args, label)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("id IN (SELECT issue_id FROM %s WHERE LOWER(label) IN (%s))", tables.Labels, strings.Join(placeholders, ", ")))
	}
	if len(filter.ExcludeLabels) > 0 {
		placeholders := make([]string, len(filter.ExcludeLabels))
		for i, label := range filter.ExcludeLabels {
			placeholders[i] = "LOWER(?)"
			args = append(args, label)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("id NOT IN (SELECT issue_id FROM %s WHERE LOWER(label) IN (%s))", tables.Labels, strings.Join(placeholders, ", ")))
	}
	if filter.NoLabels {
		whereClauses = append(whereClauses, fmt.Sprintf("id NOT IN (SELECT DISTINCT issue_id FROM %s)", tables.Labels))
	}

	if filter.Pinned != nil {
		if *filter.Pinned {
			whereClauses = append(whereClauses, "pinned = 1")
		} else {
			whereClauses = append(whereClauses, "(pinned = 0 OR pinned IS NULL)")
		}
	}
	if filter.Blocked != nil {
		// beads-7f3g: "blocked" is a derived pseudo-status, not a stored status
		// value, so `--status blocked` routes here instead of to the status
		// column (which could never match → silent 0). Match bd blocked's
		// is_blocked semantics (issueops/blocked.go:247), incl. its closed/pinned
		// exclusion, so count/list agree with bd blocked and stats blocked_issues.
		if *filter.Blocked {
			whereClauses = append(whereClauses, "is_blocked = 1 AND status <> 'closed' AND status <> 'pinned'")
		} else {
			whereClauses = append(whereClauses, "(is_blocked = 0 OR is_blocked IS NULL)")
		}
	}
	if filter.SourceRepo != nil {
		whereClauses = append(whereClauses, "source_repo = ?")
		args = append(args, *filter.SourceRepo)
	}
	if filter.Ephemeral != nil {
		if *filter.Ephemeral {
			whereClauses = append(whereClauses, "ephemeral = 1")
		} else {
			whereClauses = append(whereClauses, "(ephemeral = 0 OR ephemeral IS NULL)")
		}
	}
	if filter.IsTemplate != nil {
		if *filter.IsTemplate {
			whereClauses = append(whereClauses, "is_template = 1")
		} else {
			whereClauses = append(whereClauses, "(is_template = 0 OR is_template IS NULL)")
		}
	}

	if filter.EmptyDescription {
		whereClauses = append(whereClauses, "(description IS NULL OR description = '')")
	}
	if filter.EmptyNotes {
		whereClauses = append(whereClauses, "(notes IS NULL OR notes = '')")
	}
	if filter.NoAssignee {
		whereClauses = append(whereClauses, "(assignee IS NULL OR assignee = '')")
	}

	// beads-ycoly: date range bounds are INCLUSIVE (>= / <=), matching the
	// priority axis above (priority >= ? / priority <= ?) and the documented
	// contract in the reversed-range guard (list_input.go / reversed_range.go),
	// which states "Equal bounds (after==before) are valid" and describes the
	// WHERE as "col >= after AND col <= before". Date-only flags parse to
	// midnight, so a value stored exactly at 00:00:00 sits ON the bound; with
	// the old strict >/< such a boundary row was dropped from BOTH --X-after D
	// and --X-before D, and an equal-bounds point query (--X-after D --X-before D)
	// was always empty — contradicting the guard's own promise.
	for _, tc := range []struct {
		col, op string
		v       *time.Time
	}{
		{"created_at", ">=", filter.CreatedAfter},
		{"created_at", "<=", filter.CreatedBefore},
		{"updated_at", ">=", filter.UpdatedAfter},
		{"updated_at", "<=", filter.UpdatedBefore},
		{"closed_at", ">=", filter.ClosedAfter},
		{"closed_at", "<=", filter.ClosedBefore},
		{"started_at", ">=", filter.StartedAfter},
		{"started_at", "<=", filter.StartedBefore},
		{"defer_until", ">=", filter.DeferAfter},
		{"defer_until", "<=", filter.DeferBefore},
		{"due_at", ">=", filter.DueAfter},
		{"due_at", "<=", filter.DueBefore},
	} {
		if tc.v != nil {
			whereClauses = append(whereClauses, fmt.Sprintf("%s %s ?", tc.col, tc.op))
			args = append(args, tc.v.Format(time.RFC3339))
		}
	}

	if filter.Deferred {
		whereClauses = append(whereClauses, "(defer_until IS NOT NULL OR status = ?)")
		args = append(args, types.StatusDeferred)
	}
	if filter.Overdue {
		whereClauses = append(whereClauses, "due_at IS NOT NULL AND due_at < ? AND status != ?")
		args = append(args, time.Now().UTC().Format(time.RFC3339), types.StatusClosed)
	}

	var err error
	whereClauses, args, err = AppendMetadataClauses(whereClauses, args, filter.HasMetadataKey, filter.MetadataFields)
	if err != nil {
		return nil, nil, err
	}

	return whereClauses, args, nil
}

// AppendMetadataClauses appends JSON metadata predicates (has-key and exact
// field matches, keys in sorted order) to an existing clause/arg list.
func AppendMetadataClauses(where []string, args []any, hasKey string, fields map[string]string) ([]string, []any, error) {
	if hasKey != "" {
		if err := storage.ValidateMetadataKey(hasKey); err != nil {
			return nil, nil, err
		}
		where = append(where, "JSON_EXTRACT(metadata, ?) IS NOT NULL")
		args = append(args, storage.JSONMetadataPath(hasKey))
	}
	if len(fields) > 0 {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := storage.ValidateMetadataKey(k); err != nil {
				return nil, nil, err
			}
			where = append(where, "JSON_UNQUOTE(JSON_EXTRACT(metadata, ?)) = ?")
			args = append(args, storage.JSONMetadataPath(k), fields[k])
		}
	}
	return where, args, nil
}

// LooksLikeIssueID returns true if the query string looks like a beads issue ID.
func LooksLikeIssueID(query string) bool {
	idx := strings.Index(query, "-")
	if idx <= 0 || idx >= len(query)-1 {
		return false
	}
	if strings.Contains(query, " ") {
		return false
	}
	for _, c := range query {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
