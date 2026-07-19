package query

import (
	"strings"
	"testing"
)

// beads-q1pi: a bare (unquoted) absolute date in a query string fragments at
// the lexer (created>2026-01-15 lexes 2026, then a leftover "-01" token) — this
// is intentional, tested lexer design (beads-0vxw), so dates must be quoted in
// queries. But the resulting "unexpected token" error gave no hint, and the
// shared timeparsing error advertises the bare form (correct for flags, wrong
// here). The query parser should append a quote-the-date hint when the leftover
// token looks like a date fragment, WITHOUT touching the shared flag-path error.
func TestParseBareDateGivesQuoteHint(t *testing.T) {
	t.Parallel()

	_, err := Parse("created>2026-01-15")
	if err == nil {
		t.Fatal("expected an error for a bare (unquoted) date in a query")
	}
	msg := err.Error()
	// Still the unexpected-token error...
	if !strings.Contains(msg, "unexpected token") {
		t.Errorf("expected the unexpected-token error, got: %q", msg)
	}
	// ...now with an actionable quoting hint.
	if !strings.Contains(strings.ToLower(msg), "quote") {
		t.Errorf("bare-date error should hint to quote the date, got: %q", msg)
	}
}

// A non-date unexpected token must NOT get the date hint (the hint is specific).
func TestParseNonDateUnexpectedTokenNoDateHint(t *testing.T) {
	t.Parallel()

	_, err := Parse("status=open open")
	if err == nil {
		t.Fatal("expected an error for a trailing bare word")
	}
	if strings.Contains(strings.ToLower(err.Error()), "quote") &&
		strings.Contains(strings.ToLower(err.Error()), "date") {
		t.Errorf("non-date unexpected token must not get the date-quoting hint, got: %q", err.Error())
	}
}
