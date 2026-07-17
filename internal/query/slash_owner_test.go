package query

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestLexerSlashInBareValue covers beads-r8w symptom #1/#2: gt addresses are
// slash-paths (rig/crew/name), so a bare value containing '/' must tokenize as
// a single identifier — mirroring the ':' precedent added for namespaced
// labels (gt:merge-request). Before the fix, '/' produced
// "unexpected character '/' at position N".
func TestLexerSlashInBareValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []TokenType
		values   []string
	}{
		{
			name:     "unquoted assignee slash-path",
			input:    "assignee=beads/crew/beads_dogfooder",
			expected: []TokenType{TokenIdent, TokenEquals, TokenIdent, TokenEOF},
			values:   []string{"assignee", "=", "beads/crew/beads_dogfooder", ""},
		},
		{
			name:     "unquoted owner slash-path in AND chain",
			input:    "owner=beads/crew/beads_sr_pm AND status=open",
			expected: []TokenType{TokenIdent, TokenEquals, TokenIdent, TokenAnd, TokenIdent, TokenEquals, TokenIdent, TokenEOF},
			values:   []string{"owner", "=", "beads/crew/beads_sr_pm", "AND", "status", "=", "open", ""},
		},
		{
			name:     "quoted slash-path still works (regression)",
			input:    `assignee="test-rig/crew/dave"`,
			expected: []TokenType{TokenIdent, TokenEquals, TokenString, TokenEOF},
			values:   []string{"assignee", "=", "test-rig/crew/dave", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := NewLexer(tt.input).Tokenize()
			if err != nil {
				t.Fatalf("Tokenize(%q) error = %v", tt.input, err)
			}
			if len(tokens) != len(tt.expected) {
				t.Fatalf("got %d tokens, want %d: %+v", len(tokens), len(tt.expected), tokens)
			}
			for i, tok := range tokens {
				if tok.Type != tt.expected[i] {
					t.Errorf("token %d: got type %v, want %v", i, tok.Type, tt.expected[i])
				}
				if tok.Value != tt.values[i] {
					t.Errorf("token %d: got value %q, want %q", i, tok.Value, tt.values[i])
				}
			}
		})
	}
}

// TestAssigneeSlashValueParses covers the end-to-end path for symptom #1: the
// natural unquoted form filters on assignee without a parse error.
func TestAssigneeSlashValueParses(t *testing.T) {
	result, err := Evaluate("assignee=beads/crew/beads_dogfooder")
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Filter.Assignee == nil || *result.Filter.Assignee != "beads/crew/beads_dogfooder" {
		t.Fatalf("Filter.Assignee = %v, want beads/crew/beads_dogfooder", result.Filter.Assignee)
	}
}

// TestOwnerFilterUsesPredicateMode covers beads-r8w symptom #3: owner= is a
// documented field with a working predicate (buildOwnerPredicate) but the
// filter-only fast path hard-errored. owner= must route to predicate mode and
// evaluate correctly instead of erroring "owner filtering requires predicate
// mode".
func TestOwnerFilterUsesPredicateMode(t *testing.T) {
	now := time.Date(2025, 2, 4, 12, 0, 0, 0, time.UTC)

	owned := &types.Issue{ID: "bd-1", Owner: "beads/crew/beads_sr_pm", Status: types.StatusOpen}
	other := &types.Issue{ID: "bd-2", Owner: "mayor", Status: types.StatusOpen}

	tests := []struct {
		name    string
		query   string
		issue   *types.Issue
		matches bool
	}{
		{"owner equals matches", "owner=beads/crew/beads_sr_pm", owned, true},
		{"owner equals no match", "owner=beads/crew/beads_sr_pm", other, false},
		{"owner not-equals matches", "owner!=mayor", owned, true},
		{"owner not-equals no match", "owner!=mayor", other, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EvaluateAt(tt.query, now)
			if err != nil {
				t.Fatalf("EvaluateAt(%q) error = %v", tt.query, err)
			}
			if !result.RequiresPredicate {
				t.Fatalf("owner query %q should require predicate mode", tt.query)
			}
			if result.Predicate == nil {
				t.Fatalf("owner query %q produced nil predicate", tt.query)
			}
			if got := result.Predicate(tt.issue); got != tt.matches {
				t.Errorf("predicate(%s) = %v, want %v", tt.issue.ID, got, tt.matches)
			}
		})
	}
}
