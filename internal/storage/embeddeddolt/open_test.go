//go:build cgo

package embeddeddolt

import (
	"strings"
	"testing"
)

func TestBuildDSN_SpacesInPath(t *testing.T) {
	// Regression test for #2920: paths with spaces must not be
	// percent-encoded (%20) because the Dolt driver's ParseDataSource
	// uses the path as a literal filesystem path.
	dir := "/Users/bbrenner/Documents/Scripting Projects/beads/.beads/embeddeddolt"
	dsn := buildDSN(dir, "beads")

	if !strings.HasPrefix(dsn, "file://") {
		t.Fatalf("DSN should start with file://, got %q", dsn)
	}

	if strings.Contains(dsn, "%20") {
		t.Errorf("DSN must not percent-encode spaces; got %q", dsn)
	}

	if !strings.Contains(dsn, "Scripting Projects") {
		t.Errorf("DSN must preserve literal spaces in path; got %q", dsn)
	}

	// Verify the path portion is between "file://" and "?"
	afterScheme := strings.TrimPrefix(dsn, "file://")
	qIdx := strings.Index(afterScheme, "?")
	if qIdx == -1 {
		t.Fatalf("DSN missing query parameters: %q", dsn)
	}
	pathPortion := afterScheme[:qIdx]
	if pathPortion != dir {
		t.Errorf("path portion = %q, want %q", pathPortion, dir)
	}
}

func TestBuildDSN_NoDatabase(t *testing.T) {
	dsn := buildDSN("/tmp/test", "")
	if strings.Contains(dsn, "database=") {
		t.Errorf("DSN should not contain database param when empty; got %q", dsn)
	}
}

func TestBuildDSN_WithDatabase(t *testing.T) {
	dsn := buildDSN("/tmp/test", "mydb")
	if !strings.Contains(dsn, "database=mydb") {
		t.Errorf("DSN should contain database=mydb; got %q", dsn)
	}
}

func TestSQLStringLiteral(t *testing.T) {
	// beads-s4i: sqlStringLiteral wraps a value as a SQL string literal used in
	// SET @@<db>_head_ref = <literal> on a connection with MultiStatements=true.
	// It must neutralize BOTH the single-quote and the backslash: under Dolt's
	// default (NO_BACKSLASH_ESCAPES off) a lone backslash is an escape char, so
	// escaping only quotes lets a value like `main\'` render as `'main\''` where
	// `\'` is a literal quote — the next `'` closes the string and any trailing
	// `; ...` executes as a second statement.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "main", "'main'"},
		{"single_quote", "a'b", "'a''b'"},
		{"backslash", `a\b`, `'a\\b'`},
		{"trailing_backslash", `main\`, `'main\\'`},
		{"quote_breakout_attempt", `main\' ; DROP DATABASE x -- `, `'main\\'' ; DROP DATABASE x --'`},
		{"backslash_then_quote", `\'`, `'\\'''`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sqlStringLiteral(tc.in)
			if got != tc.want {
				t.Errorf("sqlStringLiteral(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Structural invariant: after removing every escaped pair (\\ and
			// ''), no bare single-quote may remain inside the body — that is
			// what would let an attacker close the literal early.
			body := got[1 : len(got)-1] // strip the wrapping quotes
			stripped := strings.ReplaceAll(body, `\\`, "")
			stripped = strings.ReplaceAll(stripped, "''", "")
			if strings.ContainsAny(stripped, `'\`) {
				t.Errorf("literal body has an unescaped ' or \\ after removing escaped pairs: %q (from %q)", body, tc.in)
			}
		})
	}
}
