package formula

import (
	"testing"
)

// These tests exercise the uncovered branches of the pure comparison and
// evaluation helpers in condition.go (no DB, no external state — hermetic).

// TestCompare_NilBranches covers the nil-actual arm: ==/!= against empty vs
// non-empty expected, and the default (uncomparable) operator path.
func TestCompare_NilBranches(t *testing.T) {
	cases := []struct {
		name      string
		op        Operator
		expected  string
		satisfied bool
	}{
		{"nil == empty is true", OpEqual, "", true},
		{"nil == nonempty is false", OpEqual, "x", false},
		{"nil != nonempty is true", OpNotEqual, "x", true},
		{"nil != empty is false", OpNotEqual, "", false},
		{"nil > anything is false", OpGreater, "3", false},
		{"nil <= anything is false", OpLessEqual, "3", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := compare(nil, tc.op, tc.expected)
			if got != tc.satisfied {
				t.Errorf("compare(nil, %s, %q) = %v (%s), want %v", tc.op, tc.expected, got, reason, tc.satisfied)
			}
		})
	}
}

// TestCompare_BoolNotEqual covers the bool actual path for the != operator (the
// == path is already exercised by TestEvaluateCondition_BoolOutput).
func TestCompare_BoolNotEqual(t *testing.T) {
	if got, reason := compare(true, OpNotEqual, "false"); !got {
		t.Errorf("compare(true, !=, false) = false (%s), want true", reason)
	}
	if got, reason := compare(true, OpNotEqual, "true"); got {
		t.Errorf("compare(true, !=, true) = true (%s), want false", reason)
	}
}

// TestCompare_UnknownOperator covers the terminal unknown-operator return.
func TestCompare_UnknownOperator(t *testing.T) {
	if got, reason := compare("value", Operator("~="), "value"); got {
		t.Errorf("compare with unknown operator = true (%s), want false", reason)
	}
}

// TestCompareFloat_OperatorMatrix covers every operator arm of compareFloat via
// numeric comparisons routed through compare (both operands parse as floats).
func TestCompareFloat_OperatorMatrix(t *testing.T) {
	cases := []struct {
		op        Operator
		actual    string
		expected  string
		satisfied bool
	}{
		{OpEqual, "1.5", "1.5", true},
		{OpEqual, "1.5", "2.0", false},
		{OpNotEqual, "1.5", "2.0", true},
		{OpNotEqual, "1.5", "1.5", false},
		{OpGreater, "2.5", "1.5", true},
		{OpGreater, "1.5", "2.5", false},
		{OpGreaterEqual, "1.5", "1.5", true},
		{OpGreaterEqual, "0.5", "1.5", false},
		{OpLess, "1.5", "2.5", true},
		{OpLess, "2.5", "1.5", false},
		{OpLessEqual, "1.5", "1.5", true},
		{OpLessEqual, "2.5", "1.5", false},
	}
	for _, tc := range cases {
		got, reason := compare(tc.actual, tc.op, tc.expected)
		if got != tc.satisfied {
			t.Errorf("compare(%q, %s, %q) = %v (%s), want %v", tc.actual, tc.op, tc.expected, got, reason, tc.satisfied)
		}
	}
}

// TestCompareFloat_EqualityArms covers compareFloat's ==/!= arms directly. The
// compare() dispatcher routes ==/!= through string comparison before reaching
// the numeric path, so these arms are only reachable by calling compareFloat.
func TestCompareFloat_EqualityArms(t *testing.T) {
	if got, _ := compareFloat(1.5, OpEqual, 1.5); !got {
		t.Error("compareFloat(1.5, ==, 1.5) = false, want true")
	}
	if got, _ := compareFloat(1.5, OpEqual, 2.5); got {
		t.Error("compareFloat(1.5, ==, 2.5) = true, want false")
	}
	if got, _ := compareFloat(1.5, OpNotEqual, 2.5); !got {
		t.Error("compareFloat(1.5, !=, 2.5) = false, want true")
	}
}

// TestCondition_Evaluate_UnknownType covers the default arm of Evaluate.
func TestCondition_Evaluate_UnknownType(t *testing.T) {
	c := &Condition{Type: ConditionType("bogus")}
	if _, err := c.Evaluate(&ConditionContext{}); err == nil {
		t.Fatal("Evaluate with unknown condition type: err = nil, want error")
	}
}

// TestEvaluateField_UnknownField covers the unknown-field error in evaluateField.
func TestEvaluateField_UnknownField(t *testing.T) {
	ctx := &ConditionContext{
		Steps:       map[string]*StepState{"s1": {ID: "s1", Status: "complete"}},
		CurrentStep: "s1",
	}
	c := &Condition{
		Type:     ConditionTypeField,
		StepRef:  "s1",
		Field:    "not_a_field",
		Operator: OpEqual,
		Value:    "x",
	}
	if _, err := c.Evaluate(ctx); err == nil {
		t.Fatal("evaluateField unknown field: err = nil, want error")
	}
}

// TestEvaluateAggregate_DescendantsAll covers the descendants aggregate-over
// branch (collectDescendants) with the all() function.
func TestEvaluateAggregate_DescendantsAll(t *testing.T) {
	child := &StepState{ID: "c1", Status: "complete"}
	grandchild := &StepState{ID: "g1", Status: "complete"}
	child.Children = []*StepState{grandchild}
	parent := &StepState{ID: "p1", Status: "complete", Children: []*StepState{child}}
	ctx := &ConditionContext{
		Steps:       map[string]*StepState{"p1": parent},
		CurrentStep: "p1",
	}
	c := &Condition{
		Type:          ConditionTypeAggregate,
		AggregateOver: "descendants",
		AggregateFunc: "all",
		StepRef:       "step",
		Field:         "status",
		Operator:      OpEqual,
		Value:         "complete",
	}
	res, err := c.Evaluate(ctx)
	if err != nil {
		t.Fatalf("Evaluate descendants.all: %v", err)
	}
	if !res.Satisfied {
		t.Errorf("descendants.all(status==complete) = %v (%s), want true", res.Satisfied, res.Reason)
	}
}

// TestEvaluateAggregate_DescendantsMissingStep covers the not-found branch of
// the descendants case.
func TestEvaluateAggregate_DescendantsMissingStep(t *testing.T) {
	c := &Condition{
		Type:          ConditionTypeAggregate,
		AggregateOver: "descendants",
		AggregateFunc: "all",
		StepRef:       "missing",
		Field:         "status",
		Operator:      OpEqual,
		Value:         "complete",
	}
	res, err := c.Evaluate(&ConditionContext{Steps: map[string]*StepState{}})
	if err != nil {
		t.Fatalf("Evaluate descendants missing: %v", err)
	}
	if res.Satisfied {
		t.Errorf("descendants of missing step = satisfied, want unsatisfied (%s)", res.Reason)
	}
}

// TestEvaluateAggregate_StepsAny covers the "steps" aggregate-over branch with
// any() (iterates all steps in context).
func TestEvaluateAggregate_StepsAny(t *testing.T) {
	ctx := &ConditionContext{
		Steps: map[string]*StepState{
			"a": {ID: "a", Status: "pending"},
			"b": {ID: "b", Status: "complete"},
		},
	}
	c := &Condition{
		Type:          ConditionTypeAggregate,
		AggregateOver: "steps",
		AggregateFunc: "any",
		Field:         "status",
		Operator:      OpEqual,
		Value:         "complete",
	}
	res, err := c.Evaluate(ctx)
	if err != nil {
		t.Fatalf("Evaluate steps.any: %v", err)
	}
	if !res.Satisfied {
		t.Errorf("steps.any(status==complete) = false (%s), want true", res.Reason)
	}
}

// TestEvaluateAggregate_CountNonIntegerValue covers the strconv.Atoi error in
// the count function when Value is not an integer.
func TestEvaluateAggregate_CountNonIntegerValue(t *testing.T) {
	ctx := &ConditionContext{
		Steps: map[string]*StepState{"a": {ID: "a", Status: "complete"}},
	}
	c := &Condition{
		Type:          ConditionTypeAggregate,
		AggregateOver: "steps",
		AggregateFunc: "count",
		Field:         "complete",
		Operator:      OpGreaterEqual,
		Value:         "not-a-number",
	}
	if _, err := c.Evaluate(ctx); err == nil {
		t.Fatal("count with non-integer value: err = nil, want error")
	}
}

// TestEvaluateAggregate_UnknownFunc covers the terminal unknown-aggregate-func
// error.
func TestEvaluateAggregate_UnknownFunc(t *testing.T) {
	c := &Condition{
		Type:          ConditionTypeAggregate,
		AggregateOver: "steps",
		AggregateFunc: "median",
		Field:         "status",
		Operator:      OpEqual,
		Value:         "complete",
	}
	if _, err := c.Evaluate(&ConditionContext{Steps: map[string]*StepState{}}); err == nil {
		t.Fatal("unknown aggregate func: err = nil, want error")
	}
}

// TestEvaluateExternal_EnvBranch covers the env external-type branch (compares
// an environment variable's value).
func TestEvaluateExternal_EnvBranch(t *testing.T) {
	t.Setenv("BEADS_FORMULA_TEST_VAR", "yes")
	c := &Condition{
		Type:         ConditionTypeExternal,
		ExternalType: "env",
		ExternalArg:  "BEADS_FORMULA_TEST_VAR",
		Operator:     OpEqual,
		Value:        "yes",
	}
	res, err := c.Evaluate(&ConditionContext{})
	if err != nil {
		t.Fatalf("Evaluate env: %v", err)
	}
	if !res.Satisfied {
		t.Errorf("env.BEADS_FORMULA_TEST_VAR == yes = false (%s), want true", res.Reason)
	}
}

// TestEvaluateExternal_UnknownType covers the terminal unknown-external-type
// error.
func TestEvaluateExternal_UnknownType(t *testing.T) {
	c := &Condition{
		Type:         ConditionTypeExternal,
		ExternalType: "http.get",
		ExternalArg:  "http://example.com",
	}
	if _, err := c.Evaluate(&ConditionContext{}); err == nil {
		t.Fatal("unknown external type: err = nil, want error")
	}
}
