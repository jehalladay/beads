package sqlbuild

import (
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// ReadyWorkExcludeTypes returns the issue types excluded from ready work by
// default, plus any caller extras (deduped, empty entries dropped). Infra types
// stay hidden from ready work, and rig identity beads are also hidden even
// though they are durable issues rather than infra wisps.
func ReadyWorkExcludeTypes(extra []types.IssueType) []types.IssueType {
	out := []types.IssueType{
		types.IssueType("merge-request"),
		types.TypeGate,
		types.TypeMolecule,
		// beads-2vu8: epic is a container/parent (surfaced via bd epic status +
		// parent-annotation on ready children), never directly-actionable ready
		// work — exclude it like molecule so a childless/all-open epic doesn't
		// surface as its own ready item and inflate the ready/stats count.
		types.TypeEpic,
		types.IssueType("rig"),
	}
	for _, t := range domain.DefaultInfraTypes() {
		out = append(out, types.IssueType(t))
	}
	seen := make(map[types.IssueType]bool, len(out)+len(extra))
	for _, t := range out {
		seen[t] = true
	}
	for _, t := range extra {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// readyWorkExcludeLabels are the identity/registration label families always
// hidden from ready work (beads-wqs). gt stamps agent, role, and rig
// registration beads with these labels regardless of their issue_type, which
// is heterogeneous in practice (some land as type=task with no discriminating
// column). Excluding the label catches the whole identity family across every
// ID prefix (hq-/gt-/gs-/pt-) in one predicate, so a dead polecat/witness/
// mayor registration never surfaces as claimable "ready" work.
var readyWorkExcludeLabels = []string{"gt:agent", "gt:role", "gt:rig"}

// ReadyWorkExcludeLabels returns the labels excluded from ready work by
// default, plus any caller extras (deduped, empty entries dropped). Mirrors
// ReadyWorkExcludeTypes for the label lever.
func ReadyWorkExcludeLabels(extra []string) []string {
	out := make([]string, len(readyWorkExcludeLabels))
	copy(out, readyWorkExcludeLabels)
	seen := make(map[string]bool, len(out)+len(extra))
	for _, l := range out {
		seen[l] = true
	}
	for _, l := range extra {
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

// ReadyWorkOrder is an ORDER BY fragment plus any args its CASE expressions
// need (the hybrid policy parameterizes a recency cutoff).
type ReadyWorkOrder struct {
	SQL  string
	Args []any
}

// BuildReadyWorkOrder renders the ready-work ORDER BY for a sort policy.
// createdCol/priorityCol name the sortable columns: real columns
// ("created_at"/"priority") for per-table queries, or the sort_* aliases
// ("sort_created"/"sort_priority") for UNION outer queries.
func BuildReadyWorkOrder(policy types.SortPolicy, createdCol, priorityCol string) ReadyWorkOrder {
	switch policy {
	case types.SortPolicyOldest:
		return ReadyWorkOrder{SQL: fmt.Sprintf("ORDER BY %s ASC, id ASC", createdCol)}
	case types.SortPolicyPriority:
		return ReadyWorkOrder{SQL: fmt.Sprintf("ORDER BY %s ASC, %s DESC, id ASC", priorityCol, createdCol)}
	case types.SortPolicyHybrid, "":
		recentCutoff := time.Now().UTC().Add(-48 * time.Hour)
		return ReadyWorkOrder{
			SQL: fmt.Sprintf(`ORDER BY
			CASE WHEN %s >= ? THEN 0 ELSE 1 END ASC,
			CASE WHEN %s >= ? THEN %s ELSE 999 END ASC,
			%s ASC, id ASC`, createdCol, createdCol, priorityCol, createdCol),
			Args: []any{recentCutoff, recentCutoff},
		}
	default:
		return ReadyWorkOrder{SQL: fmt.Sprintf("ORDER BY %s ASC, %s DESC, id ASC", priorityCol, createdCol)}
	}
}

// ReadyWorkWhereInputs carries the precomputed ID sets the ready-work WHERE
// clause folds in. Computing them takes queries, which is execution-context
// work each stack does its own way.
type ReadyWorkWhereInputs struct {
	// DeferredChildIDs are children of future-deferred parents; consulted
	// only when !filter.IncludeDeferred.
	DeferredChildIDs []string
	// ParentDescendantIDs are the transitive descendants of *filter.ParentID;
	// consulted only when filter.ParentID != nil.
	ParentDescendantIDs []string
}

// BuildReadyWorkWhere renders the full ready-work WHERE clause for one table
// family. Both stacks must keep ready semantics identical (Seam A parity
// suite); all ready predicates live here.
func BuildReadyWorkWhere(filter types.WorkFilter, tables FilterTables, in ReadyWorkWhereInputs) (string, []any, error) {
	var statusClause string
	var args []any
	if filter.Status != "" {
		// beads-inb4: `bd ready --include-deferred` was a dead flag — the CLI
		// ready path passes Status:"open", which became `status = 'open'` and
		// excluded deferred-STATUS rows before the defer_until relaxation below
		// could matter. Since the only way to set a future defer_until
		// (create/update --defer <future>) ALSO flips status→deferred, admit the
		// deferred status alongside the requested one when IncludeDeferred is set
		// so upcoming deferred work actually surfaces.
		if filter.IncludeDeferred && filter.Status != types.StatusDeferred {
			statusClause = "status IN (?, ?)"
			args = append(args, string(filter.Status), string(types.StatusDeferred))
		} else {
			statusClause = "status = ?"
			args = append(args, string(filter.Status))
		}
	} else if filter.IncludeDeferred {
		statusClause = "status IN ('open', 'in_progress', 'deferred')"
	} else {
		statusClause = "status IN ('open', 'in_progress')"
	}
	whereClauses := []string{
		statusClause,
		"(pinned = 0 OR pinned IS NULL)",
		"is_blocked = 0",
	}
	if !filter.IncludeEphemeral {
		whereClauses = append(whereClauses, "(ephemeral = 0 OR ephemeral IS NULL)")
	}

	if filter.Priority != nil {
		whereClauses = append(whereClauses, "priority = ?")
		args = append(args, *filter.Priority)
	}
	// beads-cseh3: honor the --priority-min/--priority-max range filters
	// (feature-parity with bd list, whose sqlbuild WHERE builder already
	// applies these). The direct ready.go RunE + gatherReadyInput (proxied)
	// both populate filter.PriorityMin/Max; here is the single shared WHERE
	// site both paths flow through. Mirrors labels.go's exact clauses.
	if filter.PriorityMin != nil {
		whereClauses = append(whereClauses, "priority >= ?")
		args = append(args, *filter.PriorityMin)
	}
	if filter.PriorityMax != nil {
		whereClauses = append(whereClauses, "priority <= ?")
		args = append(args, *filter.PriorityMax)
	}
	// beads-6na9a: case-insensitive description substring, parity with bd list
	// (filter.go DescriptionContains → LOWER(description) LIKE ?). Same issues-table column.
	if filter.DescriptionContains != "" {
		// beads-b9ova: escape LIKE metachars + ESCAPE '\\' (shared likeContains).
		whereClauses = append(whereClauses, `LOWER(description) LIKE ? ESCAPE '\\'`)
		args = append(args, LikeContains(filter.DescriptionContains))
	}
	// beads-j95lq: case-insensitive notes substring, parity with bd list
	// (filter.go NotesContains → LOWER(notes) LIKE ?). Same issues-table column.
	if filter.NotesContains != "" {
		// beads-b9ova: escape LIKE metachars + ESCAPE '\\' (shared likeContains).
		whereClauses = append(whereClauses, `LOWER(notes) LIKE ? ESCAPE '\\'`)
		args = append(args, LikeContains(filter.NotesContains))
	}
	// beads-d1as8: case-insensitive title substring, parity with bd list
	// (filter.go TitleContains → LOWER(title) LIKE ?). Same issues-table column.
	if filter.TitleContains != "" {
		// beads-b9ova: escape LIKE metachars + ESCAPE '\\' (shared likeContains).
		whereClauses = append(whereClauses, `LOWER(title) LIKE ? ESCAPE '\\'`)
		args = append(args, LikeContains(filter.TitleContains))
	}
	// beads-gqcmu: empty/missing description, parity with bd list (filter.go:252
	// EmptyDescription → same clause verbatim). Same issues-table column.
	if filter.EmptyDescription {
		whereClauses = append(whereClauses, "(description IS NULL OR description = '')")
	}
	// beads-10y4y: created/updated date-range filters (parity with bd list,
	// whose filter.go applies the same {col, op, *time.Time} loop over these
	// columns). Both the direct ready.go RunE and gatherReadyInput (proxied)
	// populate these via the shared parseListTimeFlag helper; here is the single
	// WHERE site both paths flow through. RFC3339 args match filter.go exactly.
	for _, tc := range []struct {
		col, op string
		v       *time.Time
	}{
		// beads-ycoly: INCLUSIVE bounds (>= / <=), matching filter.go's date loop
		// and the reversed-range guard's documented "after==before is valid" /
		// ">= AND <=" contract. Strict >/< dropped exact-boundary rows (date-only
		// flags parse to midnight) and made equal-bounds point queries always empty.
		{"created_at", ">=", filter.CreatedAfter},
		{"created_at", "<=", filter.CreatedBefore},
		{"updated_at", ">=", filter.UpdatedAfter},
		{"updated_at", "<=", filter.UpdatedBefore},
		// beads-zmtp6: due_at range, parity with bd list (filter.go same loop).
		{"due_at", ">=", filter.DueAfter},
		{"due_at", "<=", filter.DueBefore},
	} {
		if tc.v != nil {
			whereClauses = append(whereClauses, fmt.Sprintf("%s %s ?", tc.col, tc.op))
			args = append(args, tc.v.Format(time.RFC3339))
		}
	}
	// beads-zmtp6: --overdue, parity with bd list (filter.go:289). due_at set,
	// past, and not closed. RFC3339 arg + StatusClosed match filter.go exactly.
	if filter.Overdue {
		whereClauses = append(whereClauses, "due_at IS NOT NULL AND due_at < ? AND status != ?")
		args = append(args, time.Now().UTC().Format(time.RFC3339), string(types.StatusClosed))
	}
	if filter.Type != "" {
		whereClauses = append(whereClauses, "issue_type = ?")
		args = append(args, filter.Type)
		// beads-2a7n1: --type is the escape-hatch past the DEFAULT ready-work
		// exclusions (epic/molecule/mr/gate/rig/infra), but the user's explicit
		// --exclude-type must STILL compose with it — bd list applies both --type
		// and --exclude-type (AND), so `--type X --exclude-type Y` narrows the same
		// way there. Previously the whole else-branch (incl. the user excludes,
		// which ReadyWorkExcludeTypes merges in) was skipped when --type was set, so
		// --exclude-type was silently dropped and ready↔list diverged on any
		// overlapping input. Emit only the user's excludes here (not the defaults),
		// preserving the escape-hatch while restoring parity.
		var userExcludes []types.IssueType
		for _, t := range filter.ExcludeTypes {
			if t != "" {
				userExcludes = append(userExcludes, t)
			}
		}
		if len(userExcludes) > 0 {
			ph, a := InPlaceholders(userExcludes)
			whereClauses = append(whereClauses, fmt.Sprintf("issue_type NOT IN (%s)", ph))
			args = append(args, a...)
		}
	} else {
		ph, a := InPlaceholders(ReadyWorkExcludeTypes(filter.ExcludeTypes))
		whereClauses = append(whereClauses, fmt.Sprintf("issue_type NOT IN (%s)", ph))
		args = append(args, a...)
	}
	// --mol-type: molecule subtype filter, parity with bd list (filter.go:171
	// MolType → `mol_type = ?`). Both the direct ready.go RunE and gatherReadyInput
	// (proxied) populate WorkFilter.MolType, and the wisp tier already forwards it
	// (readyWorkWispIssueFilter, beads-3y8y8) claiming "identical semantics to the
	// main ready-issues path (and bd list)" — but the main WHERE builder never
	// emitted the predicate, so `bd ready --type molecule --mol-type swarm`
	// silently returned ALL molecule subtypes (same silent-ignore class as the
	// label glob/regex + priority-range parity fixes). Same issues-table column.
	if filter.MolType != nil {
		whereClauses = append(whereClauses, "mol_type = ?")
		args = append(args, string(*filter.MolType))
	}
	if filter.Unassigned {
		whereClauses = append(whereClauses, "(assignee IS NULL OR assignee = '')")
	} else if filter.Assignee != nil {
		// Case-insensitive to match the predicate path and the bd list/query
		// filter path (beads-xl4k): `bd ready --assignee Alice` must find an
		// issue assigned "alice" the same way `bd list --assignee` does.
		whereClauses = append(whereClauses, "LOWER(assignee) = LOWER(?)")
		args = append(args, *filter.Assignee)
	}

	if !filter.IncludeDeferred {
		whereClauses = append(whereClauses, "(defer_until IS NULL OR defer_until <= UTC_TIMESTAMP())")
		for start := 0; start < len(in.DeferredChildIDs); start += QueryBatchSize {
			end := start + QueryBatchSize
			if end > len(in.DeferredChildIDs) {
				end = len(in.DeferredChildIDs)
			}
			placeholders, batchArgs := InPlaceholders(in.DeferredChildIDs[start:end])
			args = append(args, batchArgs...)
			whereClauses = append(whereClauses, fmt.Sprintf("id NOT IN (%s)", placeholders))
		}
	}

	// Label matching is case-insensitive (LOWER both sides), consistent with the
	// bd list/query filter + predicate paths (beads-xl4k / beads-hqp8). Without
	// it a `bd ready --label Bug` misses "bug" and a mixed-case identity label
	// dodges the default exclusion below.
	if len(filter.Labels) > 0 {
		for _, label := range filter.Labels {
			whereClauses = append(whereClauses, fmt.Sprintf("id IN (SELECT issue_id FROM %s WHERE LOWER(label) = LOWER(?))", tables.Labels))
			args = append(args, label)
		}
	}
	// LabelsAny (OR-set): the issue must carry AT LEAST ONE of these labels. The
	// flag (`bd ready --label-any`) plumbs into WorkFilter.LabelsAny and the wisp
	// sub-path honors it (readyWorkWispIssueFilter), but BuildReadyWorkWhere — the
	// MAIN ready-issues query — never consumed it, so `--label-any` silently
	// returned every ready issue (only wisps were narrowed). Case-insensitive to
	// match the filter.Labels clause above (beads-mz2p; v5i7/hqp8 parity class).
	if labelsAny := CompactNonEmptyStrings(filter.LabelsAny); len(labelsAny) > 0 {
		placeholders := make([]string, len(labelsAny))
		for i, label := range labelsAny {
			placeholders[i] = "LOWER(?)"
			args = append(args, label)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("id IN (SELECT issue_id FROM %s WHERE LOWER(label) IN (%s))", tables.Labels, strings.Join(placeholders, ", ")))
	}
	// --label-pattern (glob) / --label-regex: feature-parity with bd list
	// (beads-v5i7 added them there). Without these clauses the flags flowed into
	// WorkFilter but no ready-path query consumed them, so they'd be silently
	// ignored and return everything (the same silent-ignore v5i7 fixed for list;
	// beads-v8e8). Case-insensitive LIKE (glob translated by globToLike) and SQL
	// REGEXP, mirroring BuildLabelDrivenSearch, in the ready path's id-IN style.
	if pattern := strings.TrimSpace(filter.LabelPattern); pattern != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("id IN (SELECT issue_id FROM %s WHERE LOWER(label) LIKE LOWER(?))", tables.Labels))
		args = append(args, globToLike(pattern))
	}
	if regex := strings.TrimSpace(filter.LabelRegex); regex != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("id IN (SELECT issue_id FROM %s WHERE label REGEXP ?)", tables.Labels))
		args = append(args, regex)
	}
	// Identity/registration labels (gt:agent/role/rig) are excluded from ready
	// work by default (beads-wqs), merged with any caller-supplied ExcludeLabels.
	// An explicit `filter.Labels` request for an identity label wins over the
	// default exclusion (the escape hatch: `bd ready --label gt:agent` still
	// returns the identity beads), mirroring how an explicit `--type` bypasses
	// ReadyWorkExcludeTypes. The escape-hatch match is case-insensitive too so
	// `--label GT:Agent` still bypasses the exclusion of `gt:agent`.
	requested := make(map[string]bool, len(filter.Labels))
	for _, l := range filter.Labels {
		requested[strings.ToLower(l)] = true
	}
	var excludeLabels []string
	for _, l := range ReadyWorkExcludeLabels(filter.ExcludeLabels) {
		if !requested[strings.ToLower(l)] {
			excludeLabels = append(excludeLabels, l)
		}
	}
	if len(excludeLabels) > 0 {
		placeholders := make([]string, len(excludeLabels))
		for i, label := range excludeLabels {
			placeholders[i] = "LOWER(?)"
			args = append(args, label)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("id NOT IN (SELECT issue_id FROM %s WHERE LOWER(label) IN (%s))", tables.Labels, strings.Join(placeholders, ", ")))
	}
	// beads-gqcmu: only issues with NO labels, parity with bd list (filter.go:210
	// NoLabels → id NOT IN SELECT DISTINCT issue_id FROM labels). tables.Labels
	// matches the ready builder's per-table-family pattern used just above.
	if filter.NoLabels {
		whereClauses = append(whereClauses, fmt.Sprintf("id NOT IN (SELECT DISTINCT issue_id FROM %s)", tables.Labels))
	}

	// Parent filtering: return all transitive descendants of parentID.
	// GH#3396: a one-hop subquery silently dropped grandchildren despite the
	// help text and WorkFilter.ParentID godoc both promising recursion.
	if filter.ParentID != nil {
		parentID := *filter.ParentID
		parentClauses := []string{fmt.Sprintf("(id LIKE CONCAT(?, '.%%') AND id NOT IN (SELECT issue_id FROM %s WHERE type = 'parent-child'))", tables.Dependencies)}
		args = append(args, parentID)
		for start := 0; start < len(in.ParentDescendantIDs); start += QueryBatchSize {
			end := start + QueryBatchSize
			if end > len(in.ParentDescendantIDs) {
				end = len(in.ParentDescendantIDs)
			}
			placeholders, batchArgs := InPlaceholders(in.ParentDescendantIDs[start:end])
			parentClauses = append(parentClauses, fmt.Sprintf("id IN (%s)", placeholders))
			args = append(args, batchArgs...)
		}
		whereClauses = append(whereClauses, "("+strings.Join(parentClauses, " OR ")+")")
	}

	if filter.MoleculeID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("(id IN (SELECT issue_id FROM %s WHERE type = 'parent-child' AND %s = ?) OR (id LIKE CONCAT(?, '.%%') AND id NOT IN (SELECT issue_id FROM %s WHERE type = 'parent-child')))", tables.Dependencies, DepTargetExpr, tables.Dependencies))
		args = append(args, filter.MoleculeID, filter.MoleculeID)
	}

	var err error
	whereClauses, args, err = AppendMetadataClauses(whereClauses, args, filter.HasMetadataKey, filter.MetadataFields)
	if err != nil {
		return "", nil, err
	}

	return "WHERE " + strings.Join(whereClauses, " AND "), args, nil
}
