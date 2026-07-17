package configfile

import (
	"os"
	"path/filepath"
	"testing"
)

// A password containing '#' must NOT be truncated. The parser strips inline
// comments, but a '#' is only a comment marker when preceded by whitespace
// (standard INI convention) — so '#' embedded in a value is literal. Before
// beads-l9rx the parser cut at the first '#' unconditionally, so
// 'password=p#ssw0rd' silently read as 'p' and Dolt auth failed.
func TestReadPasswordFromFile_HashInPassword(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{"hash in middle", "password=p#ssw0rd", "p#ssw0rd"},
		{"multiple hashes", "password=a#b#c", "a#b#c"},
		{"leading hash value", "password=#secret", "#secret"},
		{"trailing hash", "password=secret#", "secret#"},
		{"hash then inline comment", "password=p#ss # real comment", "p#ss"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "creds")
			content := "[h:3307]\n" + tt.line + "\n"
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := readPasswordFromFile(p, "h:3307"); got != tt.want {
				t.Errorf("readPasswordFromFile() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Regression guard: the existing inline-comment + full-line-comment contracts
// (TestReadPasswordFromFile_InlineComments) must keep working — a comment
// preceded by whitespace is still stripped.
func TestReadPasswordFromFile_InlineCommentStillStripped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds")
	content := "# full line comment\n[h:3307]\npassword=myPass # this is an inline comment\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readPasswordFromFile(p, "h:3307"); got != "myPass" {
		t.Errorf("readPasswordFromFile() = %q, want %q", got, "myPass")
	}
}
