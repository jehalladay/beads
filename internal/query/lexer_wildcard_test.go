package query

import (
	"testing"
)

// TestLexerBareTrailingWildcard pins beads-p0hw: bd query's --help documents
// the wildcard with a BARE example ("id  Issue ID (supports wildcards: bd-*)"),
// but the lexer rejected a bare '*' ("unexpected character '*'"), so only the
// quoted form id="bd-*" worked. A trailing '*' on an unquoted value must lex
// into the value (e.g. "beads-*"), matching the documented form and the
// evaluator's existing HasSuffix("*") wildcard handling. '*' is not an operator
// anywhere in the grammar, so this cannot collide.
func TestLexerBareTrailingWildcard(t *testing.T) {
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

	t.Run("bare trailing wildcard value lexes with the star", func(t *testing.T) {
		tok := lastNonEOF(t, "id=beads-*")
		if tok.Type != TokenIdent {
			t.Fatalf("last token type = %v, want TokenIdent", tok.Type)
		}
		if tok.Value != "beads-*" {
			t.Errorf("value = %q, want %q", tok.Value, "beads-*")
		}
	})

	t.Run("bare wildcard in a compound expression tokenizes cleanly", func(t *testing.T) {
		// Previously failed with "unexpected character '*'".
		if _, err := NewLexer("id=beads-* AND status=open").Tokenize(); err != nil {
			t.Errorf("Tokenize of a bare-wildcard compound query = %v, want nil", err)
		}
	})

	t.Run("spec bare wildcard tokenizes", func(t *testing.T) {
		if _, err := NewLexer("spec=foo-*").Tokenize(); err != nil {
			t.Errorf("Tokenize(spec=foo-*) = %v, want nil", err)
		}
	})

	t.Run("mid-value star still errors (only a TRAILING star is a wildcard)", func(t *testing.T) {
		// A '*' with more value after it is not a wildcard; it must still error
		// at the '*' (the pre-p0hw behavior), not silently split into two tokens.
		if _, err := NewLexer("id=be*ads").Tokenize(); err == nil {
			t.Error("Tokenize(id=be*ads) = nil, want an error at the mid-value '*'")
		}
	})
}

// TestLexerBareWildcardMatchesQuoted is the parity teeth: the bare and quoted
// wildcard forms must produce the SAME value token, so downstream evaluation
// (which already handles the quoted form) treats them identically.
func TestLexerBareWildcardMatchesQuoted(t *testing.T) {
	valueOf := func(t *testing.T, q string) string {
		t.Helper()
		toks, err := NewLexer(q).Tokenize()
		if err != nil {
			t.Fatalf("Tokenize(%q): %v", q, err)
		}
		for _, tk := range toks {
			if tk.Type == TokenString || (tk.Type == TokenIdent && tk.Value != "id") {
				return tk.Value
			}
		}
		t.Fatalf("no value token in %q", q)
		return ""
	}

	bare := valueOf(t, "id=beads-*")
	quoted := valueOf(t, `id="beads-*"`)
	if bare != quoted {
		t.Errorf("bare value %q != quoted value %q; the two forms must match", bare, quoted)
	}
}
