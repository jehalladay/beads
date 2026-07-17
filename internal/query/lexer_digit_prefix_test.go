package query

import (
	"testing"
	"time"
)

// TestLexerDigitPrefixIdent pins beads-0vxw: an unquoted value that starts with
// a digit but continues with letters (id=123abc, title=2fa, assignee=7eng) is a
// single identifier value, not a NUMBER/DURATION token with trailing letters the
// parser then rejects as "unexpected token". The re-lex only triggers on a
// following letter/underscore, so numbers, durations, dates, and versions are
// unchanged.
func TestLexerDigitPrefixIdent(t *testing.T) {
	lastNonEOF := func(t *testing.T, q string) Token {
		t.Helper()
		toks, err := NewLexer(q).Tokenize()
		if err != nil {
			t.Fatalf("Tokenize(%q): %v", q, err)
		}
		var last Token
		for _, tk := range toks {
			if tk.Type != TokenEOF {
				last = tk
			}
		}
		return last
	}

	t.Run("digit-then-letters lexes as one IDENT", func(t *testing.T) {
		cases := map[string]string{
			`id=123abc`:     "123abc",
			`title=2fa`:     "2fa",
			`assignee=7eng`: "7eng",
			`x=7days`:       "7days", // duration suffix 'd' then more letters
			`x=1a2b3c`:      "1a2b3c",
		}
		for q, wantVal := range cases {
			tk := lastNonEOF(t, q)
			if tk.Type != TokenIdent {
				t.Errorf("Tokenize(%q): last token type = %v, want IDENT", q, tk.Type)
			}
			if tk.Value != wantVal {
				t.Errorf("Tokenize(%q): value = %q, want %q", q, tk.Value, wantVal)
			}
		}
	})

	t.Run("pure numbers and durations are unchanged", func(t *testing.T) {
		numeric := map[string]TokenType{
			`priority>1`:  TokenNumber,
			`x=123`:       TokenNumber,
			`updated>7d`:  TokenDuration,
			`created<30m`: TokenDuration,
			`x=7d`:        TokenDuration,
		}
		for q, wantType := range numeric {
			tk := lastNonEOF(t, q)
			if tk.Type != wantType {
				t.Errorf("Tokenize(%q): last token type = %v, want %v", q, tk.Type, wantType)
			}
		}
	})

	t.Run("unquoted date still splits into separate NUMBER tokens", func(t *testing.T) {
		// The re-lex excludes '-', so date/version tokenization is unaffected
		// (unquoted dates were never a single token; quoted dates are the path).
		toks, err := NewLexer(`created>2026-01-15`).Tokenize()
		if err != nil {
			t.Fatalf("Tokenize date: %v", err)
		}
		nums := 0
		for _, tk := range toks {
			if tk.Type == TokenNumber {
				nums++
			}
		}
		if nums != 3 {
			t.Errorf("date `2026-01-15` produced %d NUMBER tokens, want 3 (tokenization must be unchanged)", nums)
		}
	})

	t.Run("end-to-end: previously-erroring queries now evaluate", func(t *testing.T) {
		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		for _, q := range []string{`id=123abc`, `title=2fa`, `assignee=7eng`, `id=7days`} {
			if _, err := EvaluateAt(q, now); err != nil {
				t.Errorf("EvaluateAt(%q): %v (should parse+evaluate cleanly now)", q, err)
			}
		}
	})
}
