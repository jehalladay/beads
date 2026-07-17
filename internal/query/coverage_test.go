package query

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// fixedNow is a deterministic reference time used across the coverage tests so
// duration-relative filters/predicates are reproducible and hermetic.
var fixedNow = time.Date(2025, 2, 4, 12, 0, 0, 0, time.UTC)

// TestTokenTypeString exercises every branch of TokenType.String, including the
// UNKNOWN fallback.
func TestTokenTypeString(t *testing.T) {
	tests := []struct {
		tok  TokenType
		want string
	}{
		{TokenEOF, "EOF"},
		{TokenIdent, "IDENT"},
		{TokenString, "STRING"},
		{TokenNumber, "NUMBER"},
		{TokenDuration, "DURATION"},
		{TokenEquals, "="},
		{TokenNotEquals, "!="},
		{TokenLess, "<"},
		{TokenLessEq, "<="},
		{TokenGreater, ">"},
		{TokenGreaterEq, ">="},
		{TokenAnd, "AND"},
		{TokenOr, "OR"},
		{TokenNot, "NOT"},
		{TokenLParen, "("},
		{TokenRParen, ")"},
		{TokenComma, ","},
		{TokenType(999), "UNKNOWN(999)"},
	}
	for _, tt := range tests {
		if got := tt.tok.String(); got != tt.want {
			t.Errorf("TokenType(%d).String() = %q, want %q", tt.tok, got, tt.want)
		}
	}
}

// TestComparisonOpString exercises every branch of ComparisonOp.String,
// including the "?" fallback.
func TestComparisonOpString(t *testing.T) {
	tests := []struct {
		op   ComparisonOp
		want string
	}{
		{OpEquals, "="},
		{OpNotEquals, "!="},
		{OpLess, "<"},
		{OpLessEq, "<="},
		{OpGreater, ">"},
		{OpGreaterEq, ">="},
		{ComparisonOp(99), "?"},
	}
	for _, tt := range tests {
		if got := tt.op.String(); got != tt.want {
			t.Errorf("ComparisonOp(%d).String() = %q, want %q", tt.op, got, tt.want)
		}
	}
}

// TestNodeStringForms covers the String() method of each AST node type.
func TestNodeStringForms(t *testing.T) {
	tests := []struct {
		name string
		node Node
		want string
	}{
		{
			name: "comparison",
			node: &ComparisonNode{Field: "status", Op: OpEquals, Value: "open"},
			want: "status=open",
		},
		{
			name: "and",
			node: &AndNode{
				Left:  &ComparisonNode{Field: "status", Op: OpEquals, Value: "open"},
				Right: &ComparisonNode{Field: "priority", Op: OpGreater, Value: "1"},
			},
			want: "(status=open AND priority>1)",
		},
		{
			name: "or",
			node: &OrNode{
				Left:  &ComparisonNode{Field: "status", Op: OpEquals, Value: "open"},
				Right: &ComparisonNode{Field: "status", Op: OpEquals, Value: "blocked"},
			},
			want: "(status=open OR status=blocked)",
		},
		{
			name: "not",
			node: &NotNode{Operand: &ComparisonNode{Field: "status", Op: OpEquals, Value: "closed"}},
			want: "NOT status=closed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// node() is an unexported marker; call it to mark the type as a Node.
			tt.node.node()
			if got := tt.node.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLexerStringEscapes covers the escape-sequence branches of readString.
func TestLexerStringEscapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"newline escape", `title="a\nb"`, "a\nb"},
		{"tab escape", `title="a\tb"`, "a\tb"},
		{"backslash escape", `title="a\\b"`, `a\b`},
		{"double-quote escape", `title="a\"b"`, `a"b`},
		{"single-quote escape", `title="a\'b"`, "a'b"},
		{"unknown escape passthrough", `title="a\zb"`, "azb"},
		{"single-quoted string", `title='hello'`, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, err := NewLexer(tt.input).Tokenize()
			if err != nil {
				t.Fatalf("Tokenize() error = %v", err)
			}
			// tokens: IDENT("title") = STRING(value) EOF
			if len(toks) != 4 {
				t.Fatalf("got %d tokens, want 4: %+v", len(toks), toks)
			}
			if toks[2].Type != TokenString {
				t.Fatalf("token 2 type = %v, want STRING", toks[2].Type)
			}
			if toks[2].Value != tt.want {
				t.Errorf("string value = %q, want %q", toks[2].Value, tt.want)
			}
		})
	}
}

// TestLexerErrorCases covers the lexer error branches.
func TestLexerErrorCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"bang without equals", "status!open"},
		{"unterminated escape", `t="a\`},
		{"minus without digit", "priority>-x"},
		{"lone plus", "priority>+"},
		{"invalid char", "a@b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewLexer(tt.input).Tokenize(); err == nil {
				t.Errorf("expected error for %q, got nil", tt.input)
			}
		})
	}
}

// TestFilterOnlyFields covers the apply*Filter branches that were previously
// unexercised (notes, id, spec, parent, mol_type, closed/started durations,
// and error paths).
func TestFilterOnlyFields(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		expectFilter func(*types.IssueFilter) bool
	}{
		{
			name:  "notes contains",
			query: "notes=followup",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.NotesContains == "followup"
			},
		},
		{
			name:  "id exact",
			query: "id=bd-abc",
			expectFilter: func(f *types.IssueFilter) bool {
				return len(f.IDs) == 1 && f.IDs[0] == "bd-abc"
			},
		},
		{
			name:  "id prefix wildcard",
			query: `id="bd-*"`,
			expectFilter: func(f *types.IssueFilter) bool {
				return f.IDPrefix == "bd-"
			},
		},
		{
			name:  "spec exact",
			query: "spec=SPEC-1",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.SpecIDPrefix == "SPEC-1"
			},
		},
		{
			name:  "spec wildcard",
			query: `spec="SPEC-*"`,
			expectFilter: func(f *types.IssueFilter) bool {
				return f.SpecIDPrefix == "SPEC-"
			},
		},
		{
			name:  "parent equals",
			query: "parent=bd-parent",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.ParentID != nil && *f.ParentID == "bd-parent"
			},
		},
		{
			name:  "mol_type swarm",
			query: "mol_type=swarm",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.MolType != nil && *f.MolType == types.MolTypeSwarm
			},
		},
		{
			name:  "ephemeral false",
			query: "ephemeral=false",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.Ephemeral != nil && !*f.Ephemeral
			},
		},
		{
			name:  "template true",
			query: "template=yes",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.IsTemplate != nil && *f.IsTemplate
			},
		},
		{
			name:  "closed greater duration",
			query: "closed>7d",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.ClosedAfter != nil
			},
		},
		{
			name:  "closed less-eq duration",
			query: "closed<=7d",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.ClosedBefore != nil
			},
		},
		{
			name:  "started greater duration",
			query: "started>7d",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.StartedAfter != nil
			},
		},
		{
			name:  "started less duration",
			query: "started<7d",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.StartedBefore != nil
			},
		},
		{
			name:  "created greater-eq duration",
			query: "created>=7d",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.CreatedAfter != nil
			},
		},
		{
			name:  "created less-eq duration sets end of day",
			query: "created<=7d",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.CreatedBefore != nil && f.CreatedBefore.Hour() == 23
			},
		},
		{
			name:  "updated equals duration brackets day",
			query: "updated=7d",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.UpdatedAfter != nil && f.UpdatedBefore != nil
			},
		},
		{
			name:  "updated greater-eq duration",
			query: "updated>=7d",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.UpdatedAfter != nil
			},
		},
		{
			name:  "updated less-eq duration",
			query: "updated<=7d",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.UpdatedBefore != nil && f.UpdatedBefore.Hour() == 23
			},
		},
		{
			name:  "description contains",
			query: "description=auth",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.DescriptionContains == "auth"
			},
		},
		{
			name:  "assignee explicit",
			query: "assignee=bob",
			expectFilter: func(f *types.IssueFilter) bool {
				return f.Assignee != nil && *f.Assignee == "bob"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EvaluateAt(tt.query, fixedNow)
			if err != nil {
				t.Fatalf("EvaluateAt(%q) error = %v", tt.query, err)
			}
			if result.RequiresPredicate {
				t.Fatalf("query %q unexpectedly requires predicate", tt.query)
			}
			if !tt.expectFilter(&result.Filter) {
				t.Errorf("filter check failed for %q: %+v", tt.query, result.Filter)
			}
		})
	}
}

// TestFilterErrorCases covers apply*Filter error branches (unsupported
// operators, invalid values, and owner which always requires predicate).
func TestFilterErrorCases(t *testing.T) {
	queries := []string{
		"status>open",        // status only = / !=
		"status=bogus",       // invalid status
		"priority=abc",       // non-numeric
		"priority=9",         // out of range
		"priority<0",         // matches nothing
		"priority>4",         // matches nothing
		"type<bug",           // type only = / !=
		"assignee!=x",        // assignee only =
		"label>x",            // label only =
		"title!=x",           // title only =
		"description<x",      // desc only =
		"notes<x",            // notes only =
		"id<x",               // id only =
		"spec<x",             // spec only =
		"parent<x",           // parent only =
		"pinned<x",           // bool only =
		"pinned=maybe",       // invalid bool
		"mol_type<x",         // mol_type only =
		"mol_type=bogus",     // invalid mol_type
		"has_metadata_key<x", // only =
		`metadata.k>x`,       // metadata only =
		"created<notatime",   // unparseable time
		"closed=7d",          // closed does not support =
		"started=7d",         // started does not support =
		"unknownfield=x",     // unknown field
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			if _, err := EvaluateAt(q, fixedNow); err == nil {
				t.Errorf("expected error for %q, got nil", q)
			}
		})
	}
}

// TestOwnerRequiresPredicate verifies a boolean-OR query always evaluates via
// the predicate path (owner has no IssueFilter field, so it can only be
// expressed as a predicate).
func TestOwnerRequiresPredicate(t *testing.T) {
	result, err := EvaluateAt(`owner="alice@example.com" OR status=open`, fixedNow)
	if err != nil {
		t.Fatalf("EvaluateAt error = %v", err)
	}
	if !result.RequiresPredicate || result.Predicate == nil {
		t.Fatal("owner OR query should require a predicate")
	}
}

// buildPred is a helper that parses a query and builds its predicate directly,
// exercising the buildComparisonPredicate dispatch and all build*Predicate
// leaf functions regardless of whether the query is filter-only.
func buildPred(t *testing.T, query string) func(*types.Issue) bool {
	t.Helper()
	node, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", query, err)
	}
	pred, err := NewEvaluator(fixedNow).buildPredicate(node)
	if err != nil {
		t.Fatalf("buildPredicate(%q) error = %v", query, err)
	}
	return pred
}

// TestScalarPredicates drives the string/bool/id/spec/owner/assignee/label
// predicate builders through concrete issue matches.
func TestScalarPredicates(t *testing.T) {
	closedAt := fixedNow.AddDate(0, 0, -1)
	startedAt := fixedNow.AddDate(0, 0, -3)
	issue := &types.Issue{
		ID:          "bd-abc123",
		SpecID:      "SPEC-42",
		Title:       "Fix Authentication Bug",
		Description: "The login flow is broken",
		Notes:       "See followup ticket",
		Assignee:    "Alice",
		Owner:       "alice@example.com",
		Labels:      []string{"urgent", "backend"},
		Status:      types.StatusClosed,
		Priority:    2,
		IssueType:   types.TypeBug,
		Pinned:      true,
		Ephemeral:   false,
		IsTemplate:  true,
		ClosedAt:    &closedAt,
		StartedAt:   &startedAt,
	}

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		// assignee
		{"assignee eq case-insensitive", "assignee=alice", true},
		{"assignee eq miss", "assignee=bob", false},
		{"assignee ne", "assignee!=bob", true},
		{"assignee none on set", "assignee=none", false},
		{"assignee ne none on set", "assignee!=none", true},
		// owner
		{"owner eq", `owner="alice@example.com"`, true},
		{"owner eq miss", `owner="bob@example.com"`, false},
		{"owner ne", `owner!="bob@example.com"`, true},
		// label
		{"label eq", "label=urgent", true},
		{"label eq miss", "label=frontend", false},
		{"label ne present", "label!=urgent", false},
		{"label ne absent", "label!=frontend", true},
		{"label none on labeled", "label=none", false},
		{"label ne none on labeled", "label!=none", true},
		// title / description / notes (substring, case-insensitive)
		{"title contains", "title=authentication", true},
		{"title ne contains", "title!=authentication", false},
		{"title miss", "title=missing", false},
		{"desc contains", "description=login", true},
		{"desc ne", "description!=login", false},
		{"desc none on set", "description=none", false},
		{"desc ne none on set", "description!=none", true},
		{"notes contains", "notes=followup", true},
		{"notes ne", "notes!=nope", true},
		// id
		{"id exact", "id=bd-abc123", true},
		{"id exact miss", "id=bd-other", false},
		{"id ne", "id!=bd-other", true},
		{"id wildcard", `id="bd-abc*"`, true},
		{"id wildcard miss", `id="xx-*"`, false},
		{"id wildcard ne", `id!="xx-*"`, true},
		// spec
		{"spec exact", "spec=SPEC-42", true},
		{"spec exact miss", "spec=SPEC-99", false},
		{"spec ne", "spec!=SPEC-99", true},
		{"spec wildcard", `spec="SPEC-*"`, true},
		{"spec wildcard ne", `spec!="OTHER-*"`, true},
		// bool flags
		{"pinned true", "pinned=true", true},
		{"pinned false miss", "pinned=false", false},
		{"pinned ne", "pinned!=false", true},
		{"ephemeral false", "ephemeral=false", true},
		{"template true", "template=1", true},
		// status / priority / type
		{"status eq", "status=closed", true},
		{"status ne", "status!=open", true},
		{"priority eq", "priority=2", true},
		{"priority ne", "priority!=3", true},
		{"priority lt", "priority<3", true},
		{"priority le", "priority<=2", true},
		{"priority gt", "priority>1", true},
		{"priority ge", "priority>=2", true},
		{"type eq", "type=bug", true},
		{"type ne", "type!=task", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pred := buildPred(t, tt.query)
			if got := pred(issue); got != tt.want {
				t.Errorf("pred(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

// TestNonePredicateBranches covers the isNone branches of assignee/label/desc
// predicates against an issue that has no assignee/labels/description.
func TestNonePredicateBranches(t *testing.T) {
	bare := &types.Issue{ID: "bd-bare"}
	tests := []struct {
		query string
		want  bool
	}{
		{"assignee=none", true},
		{"assignee!=none", false},
		{"label=none", true},
		{"label!=none", false},
		{"description=none", true},
		{"description!=none", false},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			pred := buildPred(t, tt.query)
			if got := pred(bare); got != tt.want {
				t.Errorf("pred(%q) on bare issue = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

// TestTimePredicates drives buildCreatedPredicate/buildUpdatedPredicate and
// compareTime across every operator, plus the closed/started nil-guard paths.
func TestTimePredicates(t *testing.T) {
	created := fixedNow.AddDate(0, 0, -5) // 2025-01-30
	updated := fixedNow.AddDate(0, 0, -1) // 2025-02-03
	closed := fixedNow.AddDate(0, 0, -2)
	started := fixedNow.AddDate(0, 0, -10)
	issue := &types.Issue{
		ID:        "bd-time",
		CreatedAt: created,
		UpdatedAt: updated,
		ClosedAt:  &closed,
		StartedAt: &started,
	}

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		// created is 5d ago; 7d filter boundary is further in the past.
		{"created greater than 7d ago", "created>7d", true},
		{"created less than 7d ago", "created<7d", false},
		{"created ge 7d", "created>=7d", true},
		{"created le 7d", "created<=7d", false},
		// equals uses same-day compare: created vs "5d" ago == same day.
		{"created equals 5d ago same day", "created=5d", true},
		{"created equals 7d ago diff day", "created=7d", false},
		{"created not-equals 7d", "created!=7d", true},
		// updated is 1d ago.
		{"updated greater than 2d ago", "updated>2d", true},
		{"updated less than 2d ago", "updated<2d", false},
		// closed present
		{"closed greater than 3d ago", "closed>3d", true},
		{"closed less than 3d ago", "closed<3d", false},
		// started present
		{"started less than 3d ago", "started<3d", true},
		{"started greater than 3d ago", "started>3d", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pred := buildPred(t, tt.query)
			if got := pred(issue); got != tt.want {
				t.Errorf("pred(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

// TestTimePredicateNilGuards verifies closed/started predicates return false
// when the issue has no ClosedAt/StartedAt timestamp.
func TestTimePredicateNilGuards(t *testing.T) {
	bare := &types.Issue{ID: "bd-nil"}
	for _, q := range []string{"closed>7d", "started>7d", "closed<7d", "started<7d"} {
		t.Run(q, func(t *testing.T) {
			pred := buildPred(t, q)
			if pred(bare) {
				t.Errorf("pred(%q) on issue without timestamp = true, want false", q)
			}
		})
	}
}

// TestCompareTimeAllOperators exercises compareTime directly, including the
// default (unsupported operator) branch.
func TestCompareTimeAllOperators(t *testing.T) {
	e := NewEvaluator(fixedNow)
	base := time.Date(2025, 2, 4, 8, 0, 0, 0, time.UTC)
	sameDay := time.Date(2025, 2, 4, 20, 0, 0, 0, time.UTC)
	earlier := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		op     ComparisonOp
		actual time.Time
		target time.Time
		want   bool
	}{
		{"equals same day", OpEquals, base, sameDay, true},
		{"equals diff day", OpEquals, base, later, false},
		{"not equals diff day", OpNotEquals, base, later, true},
		{"not equals same day", OpNotEquals, base, sameDay, false},
		{"less true", OpLess, earlier, base, true},
		{"less false", OpLess, later, base, false},
		{"less-eq via equal", OpLessEq, base, base, true},
		{"less-eq via before", OpLessEq, earlier, base, true},
		{"less-eq false", OpLessEq, later, base, false},
		{"greater true", OpGreater, later, base, true},
		{"greater false", OpGreater, earlier, base, false},
		{"greater-eq via equal", OpGreaterEq, base, base, true},
		{"greater-eq via after", OpGreaterEq, later, base, true},
		{"greater-eq false", OpGreaterEq, earlier, base, false},
		{"unsupported operator defaults false", ComparisonOp(99), base, base, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.compareTime(tt.op, tt.actual, tt.target); got != tt.want {
				t.Errorf("compareTime(%v) = %v, want %v", tt.op, got, tt.want)
			}
		})
	}
}

// TestPredicateBuilderErrors covers the operator-validation error branches in
// the build*Predicate functions.
func TestPredicateBuilderErrors(t *testing.T) {
	// Each query pairs an unsupported-operator comparison with an OR so it is
	// routed through buildPredicate; the build should fail.
	queries := []string{
		"status<open OR status=x",
		"type<bug OR status=x",
		"assignee<x OR status=x",
		"owner<x OR status=x",
		"label<x OR status=x",
		"title<x OR status=x",
		"description<x OR status=x",
		"notes<x OR status=x",
		"priority=abc OR status=x",
		"id<x OR status=x",
		`id<"x*" OR status=x`,
		"spec<x OR status=x",
		`spec<"x*" OR status=x`,
		"pinned<x OR status=x",
		"pinned=maybe OR status=x",
		"has_metadata_key<x OR status=x",
		"metadata.k<x OR status=x",
		"unknownfield=x OR status=x",
		"created<bad OR status=x",
		"updated<bad OR status=x",
		"closed<bad OR status=x",
		"started<bad OR status=x",
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			node, err := Parse(q)
			if err != nil {
				// A parse error is also an acceptable rejection.
				return
			}
			if _, err := NewEvaluator(fixedNow).buildPredicate(node); err == nil {
				t.Errorf("expected buildPredicate error for %q, got nil", q)
			}
		})
	}
}

// TestBuildPriorityPredicateOutOfRange verifies non-numeric priority in
// predicate mode is rejected (Atoi failure path).
func TestBuildPriorityPredicateNonNumeric(t *testing.T) {
	node, err := Parse("priority=xx OR status=open")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if _, err := NewEvaluator(fixedNow).buildPredicate(node); err == nil {
		t.Error("expected error for non-numeric priority in predicate mode")
	}
}

// TestParseErrorPropagation covers parser advance/peek error propagation from
// a lexer error mid-parse.
func TestParseErrorPropagation(t *testing.T) {
	// An invalid character after a valid start forces the lexer to error while
	// the parser is advancing.
	for _, q := range []string{"status=open AND @", "@bad", "status=open @"} {
		t.Run(q, func(t *testing.T) {
			if _, err := Parse(q); err == nil {
				t.Errorf("expected parse error for %q", q)
			}
		})
	}
}
