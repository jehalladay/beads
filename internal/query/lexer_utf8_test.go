package query

import "testing"

// TestLexerUTF8Values pins the lexer's UTF-8 handling (beads-wlz3). The lexer
// previously read one byte at a time (rune(input[pos]) with width=1), which
// split every multi-byte rune into its individual bytes: quoted non-ASCII
// values were silently corrupted into mojibake (café -> "cafÃ©", so the query
// matched nothing), and unquoted non-ASCII values failed to tokenize with a
// garbled byte-position error. next()/peek() now decode full runes.
func TestLexerUTF8Values(t *testing.T) {
	t.Run("quoted non-ASCII round-trips byte-exact", func(t *testing.T) {
		cases := []struct {
			query   string
			wantVal string
		}{
			{`title="café"`, "café"},
			{`title="日本語"`, "日本語"},
			{`label="münchen"`, "münchen"},
			{`assignee="josé/crew/x"`, "josé/crew/x"},
			{`desc="naïve résumé"`, "naïve résumé"},
			{`title="emoji 🚀 ok"`, "emoji 🚀 ok"},
		}
		for _, c := range cases {
			toks, err := NewLexer(c.query).Tokenize()
			if err != nil {
				t.Errorf("Tokenize(%q) error: %v", c.query, err)
				continue
			}
			var got string
			var found bool
			for _, tk := range toks {
				if tk.Type == TokenString {
					got, found = tk.Value, true
				}
			}
			if !found {
				t.Errorf("Tokenize(%q): no STRING token produced", c.query)
				continue
			}
			if got != c.wantVal {
				t.Errorf("Tokenize(%q): value = %q (% x), want %q (% x)",
					c.query, got, got, c.wantVal, c.wantVal)
			}
		}
	})

	t.Run("unquoted non-ASCII identifiers tokenize", func(t *testing.T) {
		// unicode.IsLetter is true for é/ü/日, so these are valid ident chars.
		cases := []struct {
			query   string
			wantVal string
		}{
			{`label=café`, "café"},
			{`assignee=josé`, "josé"},
			{`title=münchen`, "münchen"},
		}
		for _, c := range cases {
			toks, err := NewLexer(c.query).Tokenize()
			if err != nil {
				t.Errorf("Tokenize(%q) error: %v", c.query, err)
				continue
			}
			// tokens: IDENT(field) = IDENT(value) EOF
			var last string
			for _, tk := range toks {
				if tk.Type == TokenIdent {
					last = tk.Value
				}
			}
			if last != c.wantVal {
				t.Errorf("Tokenize(%q): value ident = %q, want %q", c.query, last, c.wantVal)
			}
		}
	})

	t.Run("full evaluate: quoted unicode value survives to filter", func(t *testing.T) {
		res, err := Evaluate(`title="café"`)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if res.Filter.TitleContains != "café" {
			t.Errorf("TitleContains = %q, want %q", res.Filter.TitleContains, "café")
		}
	})

	t.Run("ASCII invalid character still errors", func(t *testing.T) {
		// The fix must not swallow genuinely-invalid input.
		if _, err := NewLexer("a@b").Tokenize(); err == nil {
			t.Error(`Tokenize("a@b") should still error on the '@'`)
		}
	})
}
