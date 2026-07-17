package sqlbuild

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestOrderByKnownKeys(t *testing.T) {
	t.Parallel()

	cases := []struct {
		sortBy   string
		sortDesc bool
		table    string
		want     string
	}{
		{"", false, "", "ORDER BY priority ASC, created_at DESC, id ASC"},
		{"priority", true, "", "ORDER BY priority DESC, created_at DESC, id ASC"},
		{"created", false, "", "ORDER BY created_at DESC, id ASC"},
		{"created", true, "", "ORDER BY created_at ASC, id ASC"},
		{"title", false, "i", "ORDER BY LOWER(i.title) ASC, i.id ASC"},
		{"updated", false, "i", "ORDER BY i.updated_at DESC, i.id ASC"},
		{"bogus-key", false, "", "ORDER BY priority ASC, created_at DESC, id ASC"},
		{"id", false, "", ""}, // Go-side sort
	}
	for _, tc := range cases {
		if got := OrderBy(tc.sortBy, tc.sortDesc, tc.table); got != tc.want {
			t.Errorf("OrderBy(%q, %v, %q) = %q, want %q", tc.sortBy, tc.sortDesc, tc.table, got, tc.want)
		}
	}
}

// TestUnionSortColumnsCoverSortDefs pins that every SQL-side sort key has a
// sort_* alias in UnionSortColumnsSQL, so UNION consumers can order by any
// key OrderByForColumns may emit.
func TestUnionSortColumnsCoverSortDefs(t *testing.T) {
	t.Parallel()

	for key := range SortDefs {
		alias := "sort_" + key
		if key == "" {
			alias = "sort_priority"
		}
		if !strings.Contains(UnionSortColumnsSQL, alias) {
			t.Errorf("UnionSortColumnsSQL missing alias %q for sort key %q", alias, key)
		}
	}
}

// TestLessMirrorsOrderBy spot-checks that the Go-side comparator agrees with
// the SQL default ordering on the documented tie-break chain: priority ASC,
// then created_at DESC, then id ASC.
func TestLessMirrorsOrderBy(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	older := now.Add(-time.Hour)
	a := &types.Issue{ID: "a", Priority: 1, CreatedAt: now}
	b := &types.Issue{ID: "b", Priority: 2, CreatedAt: now}
	if !Less(a, b, "", false) || Less(b, a, "", false) {
		t.Error("default sort must order priority 1 before priority 2")
	}
	c := &types.Issue{ID: "c", Priority: 1, CreatedAt: older}
	if !Less(a, c, "", false) {
		t.Error("equal priority must order newer created_at first (created_at DESC)")
	}
	d := &types.Issue{ID: "d", Priority: 1, CreatedAt: now}
	if !Less(a, d, "", false) || Less(d, a, "", false) {
		t.Error("full tie must break by id ASC")
	}
}

func TestReadyWorkExcludeTypes(t *testing.T) {
	t.Parallel()

	base := ReadyWorkExcludeTypes(nil)
	seen := make(map[types.IssueType]bool, len(base))
	for _, typ := range base {
		if seen[typ] {
			t.Errorf("duplicate type %q in default exclude list", typ)
		}
		seen[typ] = true
	}
	for _, want := range []types.IssueType{"merge-request", types.TypeGate, types.TypeMolecule, "agent", "rig", "role", "message"} {
		if !seen[want] {
			t.Errorf("default exclude list missing %q", want)
		}
	}

	extended := ReadyWorkExcludeTypes([]types.IssueType{"custom", "", types.TypeGate})
	if got, want := len(extended), len(base)+1; got != want {
		t.Errorf("extras must dedupe and drop empties: len = %d, want %d", got, want)
	}
}

func TestBuildReadyWorkWhereBatchesIDSets(t *testing.T) {
	t.Parallel()

	ids := make([]string, QueryBatchSize+1)
	for i := range ids {
		ids[i] = "x-" + strings.Repeat("a", 3)
	}
	where, args, err := BuildReadyWorkWhere(types.WorkFilter{}, IssuesFilterTables, ReadyWorkWhereInputs{DeferredChildIDs: ids})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Two batched deferred-child clauses (201 IDs > QueryBatchSize). Count the
	// ID-list form specifically ("id NOT IN (?...") so the identity-label
	// subquery ("id NOT IN (SELECT ...") is not miscounted here.
	if got := strings.Count(where, "id NOT IN (?"); got != 2 {
		t.Errorf("expected 2 batched NOT IN clauses for %d IDs, got %d", len(ids), got)
	}
	// The identity-label exclusion adds one more subquery + its label args.
	if !strings.Contains(where, "id NOT IN (SELECT issue_id FROM labels WHERE label IN (") {
		t.Errorf("expected identity-label exclusion subquery, where = %q", where)
	}
	wantArgs := len(ids) + len(ReadyWorkExcludeTypes(nil)) + len(ReadyWorkExcludeLabels(nil))
	if len(args) != wantArgs {
		t.Errorf("args = %d, want %d", len(args), wantArgs)
	}
}

func TestReadyWorkExcludeLabels(t *testing.T) {
	t.Parallel()

	base := ReadyWorkExcludeLabels(nil)
	seen := make(map[string]bool, len(base))
	for _, l := range base {
		if seen[l] {
			t.Errorf("duplicate label %q in default exclude list", l)
		}
		seen[l] = true
	}
	for _, want := range []string{"gt:agent", "gt:role", "gt:rig"} {
		if !seen[want] {
			t.Errorf("default exclude list missing %q", want)
		}
	}

	// Mutating the result must not corrupt the package-level default.
	base[0] = "mutated"
	if again := ReadyWorkExcludeLabels(nil); again[0] != "gt:agent" {
		t.Errorf("ReadyWorkExcludeLabels returned a shared backing slice: got %q", again[0])
	}

	extended := ReadyWorkExcludeLabels([]string{"custom", "", "gt:agent"})
	if got, want := len(extended), len(base)+1; got != want {
		t.Errorf("extras must dedupe and drop empties: len = %d, want %d", got, want)
	}
}

func TestBuildReadyWorkWhereAlwaysExcludesIdentityLabels(t *testing.T) {
	t.Parallel()

	// beads-wqs: even with no caller-supplied ExcludeLabels, ready work must
	// exclude the gt:agent/role/rig identity family so dead agent-registration
	// beads never surface as claimable work.
	where, args, err := BuildReadyWorkWhere(types.WorkFilter{}, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(where, "id NOT IN (SELECT issue_id FROM labels WHERE label IN (") {
		t.Errorf("ready work must exclude identity labels by default, where = %q", where)
	}
	// The last len(ReadyWorkExcludeLabels(nil)) args are the label values.
	labels := ReadyWorkExcludeLabels(nil)
	tail := args[len(args)-len(labels):]
	for i, want := range labels {
		if got, ok := tail[i].(string); !ok || got != want {
			t.Errorf("label arg[%d] = %v, want %q", i, tail[i], want)
		}
	}

	// Wisp table family must use the wisp_labels table, not labels.
	wwhere, _, err := BuildReadyWorkWhere(types.WorkFilter{}, WispsFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatalf("unexpected error (wisps): %v", err)
	}
	if !strings.Contains(wwhere, "SELECT issue_id FROM wisp_labels WHERE label IN (") {
		t.Errorf("wisp ready work must exclude identity labels via wisp_labels, where = %q", wwhere)
	}
}

func TestBuildReadyWorkWhereExplicitIdentityLabelEscapeHatch(t *testing.T) {
	t.Parallel()

	// beads-wqs escape hatch: an explicit request for an identity label
	// (bd ready --label gt:agent) must NOT be force-excluded — it wins over the
	// default exclusion, mirroring how explicit --type bypasses the type filter.
	where, args, err := BuildReadyWorkWhere(
		types.WorkFilter{Labels: []string{"gt:agent"}}, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The include clause for gt:agent must be present.
	if !strings.Contains(where, "id IN (SELECT issue_id FROM labels WHERE label = ?)") {
		t.Errorf("explicit label must produce an include clause, where = %q", where)
	}
	// gt:agent appears exactly once (the include-clause arg), not again in a
	// NOT IN exclusion — the escape hatch removed it from the exclude set.
	agentCount := 0
	for _, a := range args {
		if s, ok := a.(string); ok && s == "gt:agent" {
			agentCount++
		}
	}
	if agentCount != 1 {
		t.Errorf("gt:agent should appear once (include only), got %d occurrences in args %v", agentCount, args)
	}
	// The remaining identity labels (gt:role, gt:rig) are still excluded.
	if !strings.Contains(where, "id NOT IN (SELECT issue_id FROM labels WHERE label IN (?, ?))") {
		t.Errorf("non-requested identity labels must still be excluded, where = %q", where)
	}
}

func TestSearchCountsSQLShape(t *testing.T) {
	t.Parallel()

	sql := SearchCountsSQL(WispsFilterTables, "WHERE x = ?", "ORDER BY y", "LIMIT 5", true, false)
	for _, want := range []string{
		"FROM wisps i",
		"FROM wisp_dependencies",
		"FROM wisp_comments",
		"FROM wisp_labels",
		"UNION ALL", // wisp reverse deps included
		"WHERE x = ?",
		"ORDER BY y",
		"LIMIT 5",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("counts SQL missing %q", want)
		}
	}

	noWispDeps := SearchCountsSQL(IssuesFilterTables, "", "", "", false, true)
	if strings.Contains(noWispDeps, "UNION ALL") {
		t.Error("counts SQL must not union wisp reverse deps when probe says absent")
	}
	if strings.Contains(noWispDeps, "JSON_ARRAYAGG(label)") {
		t.Error("counts SQL must skip the labels join when skipLabels is set")
	}
	if !strings.Contains(noWispDeps, "NULL AS labels_json") {
		t.Error("counts SQL must project NULL labels_json when skipLabels is set")
	}
}
