package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestValidateGraphApplyPlanAcceptsCustomTypes(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "root", Title: "Workflow", Type: "task"},
			{Key: "spec", Title: "Step spec", Type: "spec"},
		},
	}
	if err := validateGraphApplyPlan(plan, []string{"spec"}); err != nil {
		t.Fatalf("expected custom type %q to validate, got %v", "spec", err)
	}
}

func TestValidateGraphApplyPlanRejectsTypeWhenCustomTypesAbsent(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "spec", Title: "Step spec", Type: "spec"},
		},
	}
	err := validateGraphApplyPlan(plan, nil)
	if err == nil {
		t.Fatal("expected custom type to fail when nil customTypes")
	}
	want := `node "spec": invalid type "spec"`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestValidateGraphApplyPlanRejectsInvalidTypes(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "root", Title: "Root", Type: "definitely-not-a-type"},
		},
	}
	err := validateGraphApplyPlan(plan, nil)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
	want := `node "root": invalid type "definitely-not-a-type"`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// beads-0kno: graph-apply edge validation gated only on IsValid() (non-empty,
// <=32), so an unknown/typo'd dependency type (e.g. "blockd") was accepted and
// would store as a non-gating custom edge — the same silent-gate-drift beads-qfka
// closed for `bd dep add`. Edge types must be well-known, mirroring the node-type
// membership check.
func TestValidateGraphApplyPlanRejectsUnknownEdgeType(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "a", Title: "A", Type: "task"},
			{Key: "b", Title: "B", Type: "task"},
		},
		Edges: []GraphApplyEdge{
			{FromKey: "a", ToKey: "b", Type: "blockd"},
		},
	}
	err := validateGraphApplyPlan(plan, nil)
	if err == nil {
		t.Fatal("expected error for unknown edge dependency type 'blockd'")
	}
	if !strings.Contains(err.Error(), "unknown dependency type") {
		t.Fatalf("expected 'unknown dependency type' error, got: %v", err)
	}
}

func TestValidateGraphApplyPlanAcceptsWellKnownEdgeTypes(t *testing.T) {
	for _, typ := range []string{"blocks", "parent-child", "related", "tracks", "discovered-from"} {
		plan := &GraphApplyPlan{
			Nodes: []GraphApplyNode{
				{Key: "a", Title: "A", Type: "task"},
				{Key: "b", Title: "B", Type: "task"},
			},
			Edges: []GraphApplyEdge{
				{FromKey: "a", ToKey: "b", Type: typ},
			},
		}
		if err := validateGraphApplyPlan(plan, nil); err != nil {
			t.Errorf("well-known edge type %q rejected: %v", typ, err)
		}
	}
}

func TestValidateGraphApplyPlanAcceptsBuiltInTypes(t *testing.T) {
	for _, typ := range []string{"task", "bug", "feature", "epic", "chore", "decision"} {
		plan := &GraphApplyPlan{
			Nodes: []GraphApplyNode{
				{Key: "n1", Title: "Node", Type: typ},
			},
		}
		if err := validateGraphApplyPlan(plan, nil); err != nil {
			t.Errorf("type %q rejected: %v", typ, err)
		}
	}
}

func TestValidateGraphApplyPlanAcceptsEmptyType(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "n1", Title: "Node", Type: ""},
		},
	}
	if err := validateGraphApplyPlan(plan, nil); err != nil {
		t.Fatalf("empty type rejected: %v", err)
	}
}

// TestDetectUnknownGraphFields_ReporterRepro reproduces the schema-mismatch
// pattern from GH#3367: the user passes 'parent' (a string) and 'blocks' (an
// array) directly on nodes, expecting them to wire hierarchy/dependencies.
// json.Unmarshal silently drops them. detectUnknownGraphFields must surface
// both fields, scoped to the offending nodes.
func TestDetectUnknownGraphFields_ReporterRepro(t *testing.T) {
	planJSON := []byte(`{
        "nodes": [
            {"key": "root",   "type": "epic", "title": "Root epic",    "priority": 2},
            {"key": "child1", "type": "task", "title": "Child task 1", "parent": "root", "priority": 2, "blocks": ["child2"]},
            {"key": "child2", "type": "task", "title": "Child task 2", "parent": "root", "priority": 2}
        ]
    }`)

	got := detectUnknownGraphFields(planJSON)
	want := map[string][]string{
		`node["child1"]`: {"blocks", "parent"},
		`node["child2"]`: {"parent"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detectUnknownGraphFields:\n got=%#v\nwant=%#v", got, want)
	}
}

// TestDetectUnknownGraphFields_KnownSchemaIsClean verifies that a plan using
// only the documented schema (parent_key, edges array) reports no unknowns.
// Guards against the schema lists drifting from the GraphApplyPlan/Node/Edge
// json tags.
func TestDetectUnknownGraphFields_KnownSchemaIsClean(t *testing.T) {
	planJSON := []byte(`{
        "commit_message": "test",
        "nodes": [
            {"key": "root", "title": "Root", "type": "epic", "priority": 2,
             "description": "d", "assignee": "alice", "assign_after_create": false,
             "labels": ["a"], "metadata": {"k": "v"}, "metadata_refs": {"r": "root"}},
            {"key": "child", "title": "Child", "parent_key": "root",
             "parent_id": "ext-1"}
        ],
        "edges": [
            {"from_key": "child", "to_key": "root", "type": "blocks"},
            {"from_id": "ext-1", "to_id": "ext-2", "type": "related"}
        ]
    }`)

	if got := detectUnknownGraphFields(planJSON); len(got) != 0 {
		t.Fatalf("expected no unknown fields for canonical schema, got %#v", got)
	}
}

// TestDetectUnknownGraphFields_PlanAndEdgeLevel verifies coverage at the plan
// top level and edge level, not just node level.
func TestDetectUnknownGraphFields_PlanAndEdgeLevel(t *testing.T) {
	planJSON := []byte(`{
        "version": "1.0",
        "nodes": [{"key": "n", "title": "n"}],
        "edges": [{"from_key": "n", "to_key": "n", "weight": 5}]
    }`)

	got := detectUnknownGraphFields(planJSON)
	if !reflect.DeepEqual(got["plan"], []string{"version"}) {
		t.Errorf("plan-level unknowns: got=%v want=[version]", got["plan"])
	}
	if !reflect.DeepEqual(got["edge[0]"], []string{"weight"}) {
		t.Errorf("edge-level unknowns: got=%v want=[weight]", got["edge[0]"])
	}
}

// TestDetectUnknownGraphFields_BadJSON returns empty rather than panicking
// when the plan can't be parsed at the top level. Callers run the strict
// json.Unmarshal afterwards and surface the parse error there.
func TestDetectUnknownGraphFields_BadJSON(t *testing.T) {
	if got := detectUnknownGraphFields([]byte(`{not json`)); len(got) != 0 {
		t.Fatalf("expected empty map for bad JSON, got %#v", got)
	}
}

// TestWarnUnknownGraphFields_HintsForReporterFields asserts that the hint
// text for the two highest-friction fields ('parent', 'blocks' from GH#3367)
// is emitted and points the user at the canonical schema field.
func TestWarnUnknownGraphFields_HintsForReporterFields(t *testing.T) {
	var buf bytes.Buffer
	warnUnknownGraphFields(&buf, map[string][]string{
		`node["c1"]`: {"parent", "blocks"},
	})

	out := buf.String()
	if !strings.Contains(out, `unknown field(s): [blocks parent]`) {
		t.Errorf("warning missing field list: %q", out)
	}
	if !strings.Contains(out, "parent_key") {
		t.Errorf("expected 'parent' hint to mention parent_key: %q", out)
	}
	if !strings.Contains(out, "edges") {
		t.Errorf("expected 'blocks' hint to mention edges array: %q", out)
	}
}

// TestWarnUnknownGraphFields_NoUnknownsIsSilent verifies the warning function
// emits nothing when the input map is empty (the common path for well-formed
// plans).
func TestWarnUnknownGraphFields_NoUnknownsIsSilent(t *testing.T) {
	var buf bytes.Buffer
	warnUnknownGraphFields(&buf, nil)
	if buf.Len() != 0 {
		t.Fatalf("expected silent on empty input, wrote: %q", buf.String())
	}
}

// TestKnownGraphFieldSetsMatchStructTags is a guardrail: the
// knownGraphPlanFields / knownGraphNodeFields / knownGraphEdgeFields sets
// must match the json tags on the corresponding structs so that adding a
// new field on the schema doesn't silently re-introduce the false-positive
// warning that GH#3367 was trying to remove. Reflection lets us spot drift
// at test time without forcing manual upkeep on the schema author.
func TestKnownGraphFieldSetsMatchStructTags(t *testing.T) {
	check := func(name string, sample interface{}, known map[string]struct{}) {
		t.Helper()
		typ := reflect.TypeOf(sample)
		tagged := make(map[string]struct{})
		for i := 0; i < typ.NumField(); i++ {
			tag := typ.Field(i).Tag.Get("json")
			if tag == "" || tag == "-" {
				continue
			}
			if comma := strings.IndexByte(tag, ','); comma >= 0 {
				tag = tag[:comma]
			}
			tagged[tag] = struct{}{}
		}
		for k := range tagged {
			if _, ok := known[k]; !ok {
				t.Errorf("%s: json tag %q present on struct but missing from known set (would be flagged as unknown)", name, k)
			}
		}
		for k := range known {
			if _, ok := tagged[k]; !ok {
				t.Errorf("%s: %q in known set but not on struct (stale entry)", name, k)
			}
		}
	}
	check("GraphApplyPlan", GraphApplyPlan{}, knownGraphPlanFields)
	check("GraphApplyNode", GraphApplyNode{}, knownGraphNodeFields)
	check("GraphApplyEdge", GraphApplyEdge{}, knownGraphEdgeFields)
}

// TestEmitGraphApplyDryRun_Counts verifies the dry-run preview reports the
// node count, edge count, and parent-link count without performing any
// writes. Captures stdout (the dry-run path writes to stdout, with warnings
// going to stderr from the upstream caller).
func TestEmitGraphApplyDryRun_Counts(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "root", Title: "Root", Type: "epic"},
			{Key: "c1", Title: "Child 1", ParentKey: "root"},
			{Key: "c2", Title: "Child 2", ParentKey: "root"},
		},
		Edges: []GraphApplyEdge{
			{FromKey: "c1", ToKey: "c2", Type: "blocks"},
		},
	}

	out := captureStdout(t, func() error {
		emitGraphApplyDryRun(plan)
		return nil
	})

	if !strings.Contains(out, "would create 3 issue(s) and 1 edge(s) (2 parent-child link(s))") {
		t.Errorf("dry-run summary missing or wrong:\n%s", out)
	}
	for _, want := range []string{"root", "c1", "c2", "parent_key=root", "live create may still reject parent-child blocking paths"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run missing %q in output:\n%s", want, out)
		}
	}
}

// TestEmitGraphApplyDryRun_NormalizesTypeAlias verifies the dry-run PREVIEW
// shows the canonical type the live create will store, not the raw alias
// (beads-h3k5): a node typed "feat" must preview as "feature", matching the
// validate + materialize sites. Otherwise --dry-run lies about the outcome.
func TestEmitGraphApplyDryRun_NormalizesTypeAlias(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "n1", Title: "Aliased", Type: "feat"},
		},
	}
	out := captureStdout(t, func() error {
		emitGraphApplyDryRun(plan)
		return nil
	})
	if !strings.Contains(out, "feature") {
		t.Errorf("dry-run should preview normalized type 'feature' for alias 'feat':\n%s", out)
	}
	if strings.Contains(out, "[feat]") || strings.Contains(out, " feat ") {
		t.Errorf("dry-run should NOT show the raw alias 'feat':\n%s", out)
	}
}

func TestGraphApplyOptionsValidateRejectsEphemeralNoHistory(t *testing.T) {
	err := (GraphApplyOptions{Ephemeral: true, NoHistory: true}).Validate()
	if err == nil {
		t.Fatal("expected mutually exclusive graph options to be rejected")
	}
	if got, want := err.Error(), "ephemeral and no_history are mutually exclusive"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestValidateGraphApplyPlanRejectsLocalBlockingCycle(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "a", Title: "A", Type: "task"},
			{Key: "b", Title: "B", Type: "task"},
			{Key: "c", Title: "C", Type: "task"},
		},
		Edges: []GraphApplyEdge{
			{FromKey: "a", ToKey: "b", Type: "blocks"},
			{FromKey: "b", ToKey: "c", Type: "conditional-blocks"},
			{FromKey: "c", ToKey: "a", Type: "blocks"},
		},
	}

	err := validateGraphApplyPlan(plan, nil)
	if err == nil {
		t.Fatal("expected local graph cycle to be rejected")
	}
	if got, want := err.Error(), "graph contains a blocking dependency cycle"; !strings.Contains(got, want) {
		t.Fatalf("error = %q, want to contain %q", got, want)
	}
}

func TestValidateGraphApplyPlanReportsDeterministicCycleNode(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "a", Title: "A", Type: "task"},
			{Key: "b", Title: "B", Type: "task"},
			{Key: "c", Title: "C", Type: "task"},
		},
		Edges: []GraphApplyEdge{
			{FromKey: "b", ToKey: "c", Type: "blocks"},
			{FromKey: "c", ToKey: "a", Type: "blocks"},
			{FromKey: "a", ToKey: "b", Type: "blocks"},
		},
	}

	err := validateGraphApplyPlan(plan, nil)
	if err == nil {
		t.Fatal("expected local graph cycle to be rejected")
	}
	if got, want := err.Error(), `graph contains a blocking dependency cycle involving node "a"`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestValidateGraphApplyPlanAllowsNonBlockingLocalCycle(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "a", Title: "A", Type: "task"},
			{Key: "b", Title: "B", Type: "task"},
		},
		Edges: []GraphApplyEdge{
			{FromKey: "a", ToKey: "b", Type: "related"},
			{FromKey: "b", ToKey: "a", Type: "related"},
		},
	}

	if err := validateGraphApplyPlan(plan, nil); err != nil {
		t.Fatalf("non-blocking cycle rejected: %v", err)
	}
}

func TestValidateGraphApplyPlanRejectsImplicitParentChildReverseBlockingCycle(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "root", Title: "Root", Type: "epic"},
			{Key: "child", Title: "Child", Type: "task", ParentKey: "root"},
		},
		Edges: []GraphApplyEdge{
			{FromKey: "root", ToKey: "child", Type: "blocks"},
		},
	}

	err := validateGraphApplyPlan(plan, nil)
	if err == nil {
		t.Fatal("expected implicit parent-child plus reverse blocking edge to be rejected")
	}
	if got, want := err.Error(), "blocking dependency cycle"; !strings.Contains(got, want) {
		t.Fatalf("error = %q, want to contain %q", got, want)
	}
}

func TestValidateGraphApplyPlanIgnoresIDOverridesForLocalCycleValidation(t *testing.T) {
	plan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "a", Title: "A", Type: "task"},
			{Key: "b", Title: "B", Type: "task"},
		},
		Edges: []GraphApplyEdge{
			{FromKey: "a", FromID: "bd-existing", ToKey: "b", Type: "blocks"},
			{FromKey: "b", ToKey: "a", Type: "blocks"},
		},
	}

	if err := validateGraphApplyPlan(plan, nil); err != nil {
		t.Fatalf("ID override edge should not be treated as a local key cycle: %v", err)
	}
}

func TestGraphApplyEdgeIsLocalCycleRelevantOnlyForLocalBlockingEdges(t *testing.T) {
	tests := []struct {
		name string
		edge GraphApplyEdge
		typ  string
		want bool
	}{
		{name: "local default blocks", edge: GraphApplyEdge{FromKey: "a", ToKey: "b"}, want: true},
		{name: "local conditional blocks", edge: GraphApplyEdge{FromKey: "a", ToKey: "b"}, typ: "conditional-blocks", want: true},
		{name: "local nonblocking", edge: GraphApplyEdge{FromKey: "a", ToKey: "b"}, typ: "related"},
		{name: "existing id target", edge: GraphApplyEdge{FromKey: "a", ToID: "bd-123"}, want: false},
		{name: "explicit id overrides key", edge: GraphApplyEdge{FromKey: "a", FromID: "bd-1", ToKey: "b"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := graphApplyEdgeIsLocalCycleRelevant(tt.edge, graphApplyDependencyType(tt.typ))
			if got != tt.want {
				t.Fatalf("localCycleRelevant = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGraphApplyParentDepPairs(t *testing.T) {
	nodes := []GraphApplyNode{
		{Key: "root", Title: "Root"},
		{Key: "child", Title: "Child", ParentKey: "root"},
		{Key: "external-child", Title: "External child", ParentID: "bd-parent"},
	}
	keyToID := map[string]string{
		"root":           "bd-root",
		"child":          "bd-child",
		"external-child": "bd-external-child",
	}

	pairs := graphApplyParentDepPairs(nodes, keyToID)
	for _, pair := range []struct {
		child  string
		parent string
	}{
		{"bd-child", "bd-root"},
		{"bd-external-child", "bd-parent"},
	} {
		if !pairs[graphApplyDepPairKey(pair.child, pair.parent)] {
			t.Fatalf("missing parent dep pair %s -> %s", pair.child, pair.parent)
		}
	}
	if pairs[graphApplyDepPairKey("bd-root", "bd-child")] {
		t.Fatal("unexpected reverse parent dep pair")
	}
}

// beads-s13vq: `bd create --graph <file>` mints each node with its
// user-supplied labels (GraphApplyNode.Labels -> Issue.Labels), but
// validateGraphApplyPlan never checked them against the reserved gt identity
// family (gt:agent/gt:role/gt:rig). The 3c4g write-reserve sweep guarded the
// single-create seams (create.go, --file markdown kvq0v, label add, tag,
// set-state) but missed the graph-create axis, so a graph node carrying
// "labels":["gt:agent"] silently minted a bead HIDDEN from `bd ready` (the wqs
// discriminator excludes that family) — the same spoof/foot-gun 3c4g closed for
// hand-written labels. validateGraphApplyPlan is the SHARED chokepoint for both
// the direct (createIssuesFromGraph) and proxied (create_proxied_server.go)
// graph-apply paths, so one guard here covers both backends.
// MUTATION-VERIFIED: removing the reservedIdentityLabelError check in
// validateGraphApplyPlan makes the reject sub-test go RED (the plan validates).
func TestValidateGraphApplyPlanRejectsReservedIdentityLabels_s13vq(t *testing.T) {
	// The guard is bypassed for privileged gt-internal writes (GT_INTERNAL=1);
	// clear it so a human/agent graph-apply is exercised deterministically.
	t.Setenv("GT_INTERNAL", "")

	for _, lbl := range []string{"gt:agent", "gt:role", "gt:rig"} {
		plan := &GraphApplyPlan{
			Nodes: []GraphApplyNode{
				{Key: "root", Title: "Root", Type: "task", Labels: []string{lbl}},
			},
		}
		err := validateGraphApplyPlan(plan, nil)
		if err == nil {
			t.Fatalf("expected graph node with reserved identity label %q to be rejected, got nil", lbl)
		}
		if !strings.Contains(err.Error(), lbl) || !strings.Contains(err.Error(), "reserved gt identity label") {
			t.Fatalf("reserved-label error for %q = %q, want it to name the label and 'reserved gt identity label'", lbl, err.Error())
		}
		if !strings.Contains(err.Error(), "root") {
			t.Fatalf("reserved-label error should name the offending node key %q, got %q", "root", err.Error())
		}
	}

	// A non-reserved label validates normally (guard is scoped to the identity family).
	// NOTE (beads-o70m1): "provides:*" was previously used here as an "ordinary"
	// label, but it is now reserved by providesLabelError (graph seam), so use a
	// plainly-unreserved label instead — the provides: reservation has its own
	// coverage in TestValidateGraphApplyPlanRejectsProvidesLabels_o70m1.
	okPlan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "root", Title: "Root", Type: "task", Labels: []string{"area:backend", "kind:chore"}},
		},
	}
	if err := validateGraphApplyPlan(okPlan, nil); err != nil {
		t.Fatalf("expected ordinary labels to validate, got %v", err)
	}

	// GT_INTERNAL=1 (the privileged gt orchestrator write) bypasses the
	// reservation so gt's own graph-based registration keeps working.
	t.Setenv("GT_INTERNAL", "1")
	internalPlan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "root", Title: "Root", Type: "task", Labels: []string{"gt:agent"}},
		},
	}
	if err := validateGraphApplyPlan(internalPlan, nil); err != nil {
		t.Fatalf("GT_INTERNAL graph-apply should bypass the reserved-label guard, got %v", err)
	}
}

// TestValidateGraphApplyPlanRejectsProvidesLabels_o70m1 pins the beads-o70m1
// reservation of the 'provides:' cross-project capability family on graph-apply
// nodes, matching single `bd create --labels` and `bd label add`. Without it a
// graph node carrying "labels":["provides:cap"] silently mints an OPEN bead
// with a provides: capability at RC=0, bypassing the closed-requirement +
// single-provider invariants that `bd ship` enforces. validateGraphApplyPlan is
// the shared chokepoint for the direct and proxied graph-apply paths.
// MUTATION-VERIFIED: removing the providesLabelError check in
// validateGraphApplyPlan makes the reject sub-test go RED (the plan validates).
func TestValidateGraphApplyPlanRejectsProvidesLabels_o70m1(t *testing.T) {
	for _, lbl := range []string{"provides:api", "provides:auth-service"} {
		plan := &GraphApplyPlan{
			Nodes: []GraphApplyNode{
				{Key: "root", Title: "Root", Type: "task", Labels: []string{lbl}},
			},
		}
		err := validateGraphApplyPlan(plan, nil)
		if err == nil {
			t.Fatalf("expected graph node with reserved provides: label %q to be rejected, got nil", lbl)
		}
		if !strings.Contains(err.Error(), "provides:") || !strings.Contains(err.Error(), "bd ship") {
			t.Fatalf("provides: error for %q = %q, want it to mention 'provides:' and the 'bd ship' hint", lbl, err.Error())
		}
		if !strings.Contains(err.Error(), "root") {
			t.Fatalf("provides: error should name the offending node key %q, got %q", "root", err.Error())
		}
	}

	// A non-provides label validates normally (guard is scoped to the family).
	okPlan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "root", Title: "Root", Type: "task", Labels: []string{"export:api", "area:backend"}},
		},
	}
	if err := validateGraphApplyPlan(okPlan, nil); err != nil {
		t.Fatalf("expected ordinary labels (incl ship's export: input) to validate, got %v", err)
	}

	// Unlike the identity family, the provides: reservation is NOT bypassed by
	// GT_INTERNAL (ship stamps it via storage, not this seam).
	t.Setenv("GT_INTERNAL", "1")
	internalPlan := &GraphApplyPlan{
		Nodes: []GraphApplyNode{
			{Key: "root", Title: "Root", Type: "task", Labels: []string{"provides:api"}},
		},
	}
	if err := validateGraphApplyPlan(internalPlan, nil); err == nil {
		t.Fatal("GT_INTERNAL must NOT bypass the provides: reservation on graph-apply")
	}
}
