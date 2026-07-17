package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ExpandPath -------------------------------------------------------------

func TestExpandPathPassthrough(t *testing.T) {
	// Empty and "." are returned verbatim (no expansion).
	for _, in := range []string{"", "."} {
		if got := ExpandPath(in); got != in {
			t.Errorf("ExpandPath(%q) = %q, want %q (verbatim)", in, got, in)
		}
	}
}

func TestExpandPathTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir available: %v", err)
	}
	got := ExpandPath("~/sub/dir")
	want := filepath.Join(home, "sub/dir")
	if got != want {
		t.Errorf("ExpandPath(~/sub/dir) = %q, want %q", got, want)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("ExpandPath(~/sub/dir) = %q, want absolute path", got)
	}
}

func TestExpandPathRelativeToAbsolute(t *testing.T) {
	got := ExpandPath("relative/path")
	if !filepath.IsAbs(got) {
		t.Errorf("ExpandPath(relative/path) = %q, want absolute path", got)
	}
	if !strings.HasSuffix(got, filepath.Join("relative", "path")) {
		t.Errorf("ExpandPath(relative/path) = %q, want suffix relative/path", got)
	}
}

func TestExpandPathAbsoluteUnchanged(t *testing.T) {
	abs := filepath.Join(string(filepath.Separator), "tmp", "already", "absolute")
	if got := ExpandPath(abs); got != abs {
		t.Errorf("ExpandPath(%q) = %q, want unchanged (already absolute)", abs, got)
	}
}

// --- remoteRepositorySlug ---------------------------------------------------

func TestRemoteRepositorySlug(t *testing.T) {
	tests := []struct {
		name     string
		remote   string
		wantSlug string
		wantOK   bool
	}{
		{"scp-like with .git", "git@github.com:owner/repo.git", "owner/repo", true},
		{"scp-like without .git", "git@github.com:owner/repo", "owner/repo", true},
		{"https with .git", "https://github.com/owner/repo.git", "owner/repo", true},
		{"https without .git", "https://github.com/owner/repo", "owner/repo", true},
		{"https trailing slash", "https://github.com/owner/repo/", "owner/repo", true},
		{"ssh url scheme", "ssh://git@github.com/owner/repo.git", "owner/repo", true},
		{"empty", "", "", false},
		{"whitespace only", "   ", "", false},
		{"scp-like empty path", "git@github.com:", "", false},
		// Control char makes url.Parse fail → (,false) via the parse-error branch.
		{"unparseable url", "ht\x7ftp://bad/owner/repo", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, ok := remoteRepositorySlug(tt.remote)
			if ok != tt.wantOK {
				t.Errorf("remoteRepositorySlug(%q) ok = %v, want %v", tt.remote, ok, tt.wantOK)
			}
			if slug != tt.wantSlug {
				t.Errorf("remoteRepositorySlug(%q) slug = %q, want %q", tt.remote, slug, tt.wantSlug)
			}
		})
	}
}

// --- sameRemoteRepository ---------------------------------------------------

func TestSameRemoteRepository(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical https", "https://github.com/o/r.git", "https://github.com/o/r.git", true},
		{"scp vs https same repo", "git@github.com:o/r.git", "https://github.com/o/r", true},
		{"different repos", "https://github.com/o/r1.git", "https://github.com/o/r2.git", false},
		// Neither parses to a slug → raw trimmed-string comparison fallback.
		{"raw fallback equal", "not a url", "not a url", true},
		{"raw fallback trimmed", "  local-path  ", "local-path", true},
		{"raw fallback unequal", "pathA", "pathB", false},
		// One side parses to a slug, the other is empty → okA && okB false → raw fallback.
		{"mixed parse falls back", "https://github.com/o/r.git", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameRemoteRepository(tt.a, tt.b); got != tt.want {
				t.Errorf("sameRemoteRepository(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// --- normalizeRemotePath ----------------------------------------------------

func TestNormalizeRemotePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantSlug string
		wantOK   bool
	}{
		{"plain", "/owner/repo", "owner/repo", true},
		{"strip .git", "/owner/repo.git", "owner/repo", true},
		{"trailing slash", "owner/repo/", "owner/repo", true},
		{"empty", "", "", false},
		{"only slashes", "///", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, ok := normalizeRemotePath(tt.path)
			if ok != tt.wantOK {
				t.Errorf("normalizeRemotePath(%q) ok = %v, want %v", tt.path, ok, tt.wantOK)
			}
			if slug != tt.wantSlug {
				t.Errorf("normalizeRemotePath(%q) = %q, want %q", tt.path, slug, tt.wantSlug)
			}
		})
	}
}
