package formula

import (
	"testing"
)

// --- compareInt / compareString via compare() ------------------------------
//
// compare() dispatches to compareInt for int actuals and compareString for
// non-numeric strings, so exercising the operator matrix through compare()
// covers both unexported comparators end-to-end.

func TestCompareInt_OperatorMatrix(t *testing.T) {
	tests := []struct {
		actual int
		op     Operator
		exp    int
		want   bool
	}{
		{5, OpEqual, 5, true},
		{5, OpEqual, 6, false},
		{5, OpNotEqual, 6, true},
		{5, OpNotEqual, 5, false},
		{6, OpGreater, 5, true},
		{5, OpGreater, 5, false},
		{5, OpGreaterEqual, 5, true},
		{4, OpGreaterEqual, 5, false},
		{4, OpLess, 5, true},
		{5, OpLess, 5, false},
		{5, OpLessEqual, 5, true},
		{6, OpLessEqual, 5, false},
	}
	for _, tt := range tests {
		got, _ := compareInt(tt.actual, tt.op, tt.exp)
		if got != tt.want {
			t.Errorf("compareInt(%d, %q, %d) = %v, want %v", tt.actual, tt.op, tt.exp, got, tt.want)
		}
	}
}

func TestCompareString_OperatorMatrix(t *testing.T) {
	tests := []struct {
		actual string
		op     Operator
		exp    string
		want   bool
	}{
		{"abc", OpEqual, "abc", true},
		{"abc", OpEqual, "abd", false},
		{"abc", OpNotEqual, "abd", true},
		{"abc", OpNotEqual, "abc", false},
		{"b", OpGreater, "a", true},
		{"a", OpGreater, "a", false},
		{"a", OpGreaterEqual, "a", true},
		{"a", OpGreaterEqual, "b", false},
		{"a", OpLess, "b", true},
		{"b", OpLess, "b", false},
		{"a", OpLessEqual, "a", true},
		{"b", OpLessEqual, "a", false},
	}
	for _, tt := range tests {
		got, _ := compareString(tt.actual, tt.op, tt.exp)
		if got != tt.want {
			t.Errorf("compareString(%q, %q, %q) = %v, want %v", tt.actual, tt.op, tt.exp, got, tt.want)
		}
	}
}

// compare() with a non-numeric string actual falls through to compareString.
func TestCompare_StringFallback(t *testing.T) {
	got, _ := compare("hello", OpEqual, "hello")
	if !got {
		t.Errorf("compare(\"hello\", ==, \"hello\") = false, want true")
	}
	got, _ = compare("hello", OpNotEqual, "world")
	if !got {
		t.Errorf("compare(\"hello\", !=, \"world\") = false, want true")
	}
}

// --- parseUnary via EvaluateExpr -------------------------------------------

func TestEvaluateExpr_UnaryMinus(t *testing.T) {
	tests := []struct {
		expr string
		want int
	}{
		{"-5", -5},
		{"-(2 + 3)", -5},
		{"10 + -3", 7},
		{"--4", 4}, // double unary minus recurses
		{"2 * -3", -6},
	}
	for _, tt := range tests {
		got, err := EvaluateExpr(tt.expr, nil)
		if err != nil {
			t.Errorf("EvaluateExpr(%q) unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("EvaluateExpr(%q) = %d, want %d", tt.expr, got, tt.want)
		}
	}
}

// --- MatchAnyPointcut ------------------------------------------------------

func TestMatchAnyPointcut_EmptyMatchesAll(t *testing.T) {
	if !MatchAnyPointcut(nil, &Step{ID: "anything"}) {
		t.Error("MatchAnyPointcut(nil, step) = false, want true (empty means match all)")
	}
	if !MatchAnyPointcut([]*Pointcut{}, &Step{ID: "anything"}) {
		t.Error("MatchAnyPointcut([], step) = false, want true (empty means match all)")
	}
}

func TestMatchAnyPointcut_Matching(t *testing.T) {
	step := &Step{ID: "shiny.implement", Type: "task", Labels: []string{"backend"}}

	// One of several pointcuts matches (glob).
	pcs := []*Pointcut{
		{Glob: "review"},  // no match
		{Glob: "shiny.*"}, // matches ID
	}
	if !MatchAnyPointcut(pcs, step) {
		t.Error("MatchAnyPointcut(glob shiny.*) = false, want true")
	}

	// Type match.
	if !MatchAnyPointcut([]*Pointcut{{Type: "task"}}, step) {
		t.Error("MatchAnyPointcut(type task) = false, want true")
	}

	// Label match.
	if !MatchAnyPointcut([]*Pointcut{{Label: "backend"}}, step) {
		t.Error("MatchAnyPointcut(label backend) = false, want true")
	}

	// No pointcut matches.
	none := []*Pointcut{{Glob: "other.*"}, {Type: "bug"}, {Label: "frontend"}}
	if MatchAnyPointcut(none, step) {
		t.Error("MatchAnyPointcut(non-matching) = true, want false")
	}
}

// --- Formula.GetRequiredVars -----------------------------------------------

func TestGetRequiredVars(t *testing.T) {
	f := &Formula{
		Vars: map[string]*VarDef{
			"a": {Required: true},
			"b": {Required: false},
			"c": {Required: true},
		},
	}
	got := f.GetRequiredVars()
	if len(got) != 2 {
		t.Fatalf("GetRequiredVars() len = %d (%v), want 2", len(got), got)
	}
	set := map[string]bool{}
	for _, v := range got {
		set[v] = true
	}
	if !set["a"] || !set["c"] {
		t.Errorf("GetRequiredVars() = %v, want to contain a and c", got)
	}
	if set["b"] {
		t.Errorf("GetRequiredVars() = %v, should not contain non-required b", got)
	}
}

func TestGetRequiredVars_None(t *testing.T) {
	f := &Formula{Vars: map[string]*VarDef{"a": {Required: false}}}
	if got := f.GetRequiredVars(); len(got) != 0 {
		t.Errorf("GetRequiredVars() = %v, want empty", got)
	}
}

// --- Formula.GetBondPoint --------------------------------------------------

func TestGetBondPoint_NilCompose(t *testing.T) {
	f := &Formula{}
	if bp := f.GetBondPoint("x"); bp != nil {
		t.Errorf("GetBondPoint on nil Compose = %v, want nil", bp)
	}
}

func TestGetBondPoint_FoundAndMissing(t *testing.T) {
	f := &Formula{
		Compose: &ComposeRules{
			BondPoints: []*BondPoint{
				{ID: "review", AfterStep: "implement"},
				{ID: "qa", BeforeStep: "done"},
			},
		},
	}
	bp := f.GetBondPoint("qa")
	if bp == nil || bp.ID != "qa" {
		t.Errorf("GetBondPoint(qa) = %v, want the qa bond point", bp)
	}
	if bp := f.GetBondPoint("missing"); bp != nil {
		t.Errorf("GetBondPoint(missing) = %v, want nil", bp)
	}
}

// --- collectDescendants ----------------------------------------------------

func TestCollectDescendants(t *testing.T) {
	// Tree: root -> [a -> [a1], b]
	a1 := &StepState{Status: "complete"}
	a := &StepState{Status: "in_progress", Children: []*StepState{a1}}
	b := &StepState{Status: "pending"}
	root := &StepState{Status: "pending", Children: []*StepState{a, b}}

	got := collectDescendants(root)
	if len(got) != 3 {
		t.Fatalf("collectDescendants() len = %d, want 3 (a, a1, b)", len(got))
	}
	// a1 must be reached transitively through a.
	found := false
	for _, s := range got {
		if s == a1 {
			found = true
		}
	}
	if !found {
		t.Error("collectDescendants() did not include the grandchild a1")
	}
}

func TestCollectDescendants_Leaf(t *testing.T) {
	if got := collectDescendants(&StepState{Status: "pending"}); len(got) != 0 {
		t.Errorf("collectDescendants(leaf) = %v, want empty", got)
	}
}

// --- matchStep -------------------------------------------------------------

func TestMatchStep(t *testing.T) {
	s := &StepState{
		Status: "complete",
		Output: map[string]interface{}{"count": "5", "nested": map[string]interface{}{"ok": "yes"}},
	}

	// field == "status" reads Status directly.
	if got, _ := matchStep(s, "status", OpEqual, "complete"); !got {
		t.Error("matchStep(status == complete) = false, want true")
	}

	// "output." prefix reads a nested output value.
	if got, _ := matchStep(s, "output.count", OpEqual, "5"); !got {
		t.Error("matchStep(output.count == 5) = false, want true")
	}
	if got, _ := matchStep(s, "output.nested.ok", OpEqual, "yes"); !got {
		t.Error("matchStep(output.nested.ok == yes) = false, want true")
	}

	// A bare field name falls back to Status shorthand.
	if got, _ := matchStep(s, "anything", OpEqual, "complete"); !got {
		t.Error("matchStep(bare field, status shorthand) = false, want true")
	}
}

// --- cloneOnComplete -------------------------------------------------------

func TestCloneOnComplete_Nil(t *testing.T) {
	if got := cloneOnComplete(nil); got != nil {
		t.Errorf("cloneOnComplete(nil) = %v, want nil", got)
	}
}

func TestCloneOnComplete_DeepCopiesVars(t *testing.T) {
	orig := &OnCompleteSpec{
		ForEach:  "output.items",
		Bond:     "child-formula",
		Parallel: true,
		Vars:     map[string]string{"item": "{item}", "idx": "{index}"},
	}
	clone := cloneOnComplete(orig)
	if clone == orig {
		t.Fatal("cloneOnComplete returned the same pointer, want a copy")
	}
	if clone.ForEach != orig.ForEach || clone.Bond != orig.Bond || clone.Parallel != orig.Parallel {
		t.Errorf("cloneOnComplete did not copy scalar fields: %+v", clone)
	}
	// Mutating the clone's Vars must not affect the original (deep copy).
	clone.Vars["item"] = "MUTATED"
	if orig.Vars["item"] == "MUTATED" {
		t.Error("cloneOnComplete did not deep-copy Vars — mutation leaked to original")
	}
}
