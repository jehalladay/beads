package sqlbuild

import (
	"testing"
)

// These cover the small pure helpers not already exercised by
// sqlbuild_test.go (which covers OrderBy, Less, ReadyWorkExcludeTypes, and the
// DB-coupled clause builders).

func TestQualifyColumns(t *testing.T) {
	t.Parallel()

	// Newlines/tabs are normalized to spaces and every column is prefixed.
	got := QualifyColumns("id,\n\ttitle, status", "i.")
	want := "i.id, i.title, i.status"
	if got != want {
		t.Errorf("QualifyColumns = %q, want %q", got, want)
	}

	// A single column with no separators is still prefixed.
	if got := QualifyColumns("id", "x."); got != "x.id" {
		t.Errorf("single-column QualifyColumns = %q, want x.id", got)
	}
}

func TestInPlaceholders(t *testing.T) {
	t.Parallel()

	ph, args := InPlaceholders([]string{"a", "b", "c"})
	if ph != "?,?,?" {
		t.Errorf("placeholders = %q, want ?,?,?", ph)
	}
	if len(args) != 3 || args[0] != "a" || args[2] != "c" {
		t.Errorf("args = %v, want [a b c]", args)
	}

	// Empty input yields an empty placeholder string and a zero-length arg slice.
	ph, args = InPlaceholders([]string{})
	if ph != "" || len(args) != 0 {
		t.Errorf("empty InPlaceholders = (%q,%v), want (\"\",[])", ph, args)
	}
}

func TestCompactNonEmptyStrings(t *testing.T) {
	t.Parallel()

	if got := CompactNonEmptyStrings(nil); got != nil {
		t.Errorf("nil input = %v, want nil", got)
	}
	got := CompactNonEmptyStrings([]string{"a", "", "b", ""})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("compact = %v, want [a b]", got)
	}
	// All-empty collapses to a zero-length slice.
	if got := CompactNonEmptyStrings([]string{"", ""}); len(got) != 0 {
		t.Errorf("all-empty = %v, want empty", got)
	}
}

func TestIsGoSideSort(t *testing.T) {
	t.Parallel()

	if !IsGoSideSort("id") {
		t.Error("IsGoSideSort(id) = false, want true")
	}
	if IsGoSideSort("priority") {
		t.Error("IsGoSideSort(priority) = true, want false")
	}
	if IsGoSideSort("") {
		t.Error("IsGoSideSort(\"\") = true, want false")
	}
}

func TestLooksLikeIssueID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want bool
	}{
		{"beads-c6c", true},
		{"hq-wisp-abc", true},
		{"gt-r06.12", true}, // dots are allowed
		{"nodash", false},
		{"-leading", false},
		{"trailing-", false},
		{"has space-x", false},
		{"bad_underscore-x", false},
		{"unicode-✓", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := LooksLikeIssueID(tt.in); got != tt.want {
				t.Errorf("LooksLikeIssueID(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestEscapeLikePattern is the teeth for beads-b9ova: LIKE wildcard metachars in
// user substring input must be escaped so they match literally, and the escape
// char (backslash) itself must be escaped first.
func TestEscapeLikePattern(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"100%", `100\%`},
		{"a_b", `a\_b`},
		{"%_%", `\%\_\%`},
		{`back\slash`, `back\\slash`},
		// backslash escaped FIRST: a literal `\%` becomes `\\` + `\%`, not `\` + `\\%`
		{`\%`, `\\\%`},
	}
	for _, c := range cases {
		if got := EscapeLikePattern(c.in); got != c.want {
			t.Errorf("EscapeLikePattern(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestLikeContains verifies the %-wrapped, lowercased, escaped operand.
func TestLikeContains(t *testing.T) {
	t.Parallel()
	if got := LikeContains("100%"); got != `%100\%%` {
		t.Errorf("LikeContains(100%%) = %q, want %q", got, `%100\%%`)
	}
	if got := LikeContains("AbC"); got != "%abc%" {
		t.Errorf("LikeContains(AbC) = %q, want %q", got, "%abc%")
	}
}
