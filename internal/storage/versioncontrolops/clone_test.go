package versioncontrolops

import (
	"strings"
	"testing"
)

// TestSanitizeURL verifies that credentials are stripped for safe error
// reporting, including the parse-failure path (beads-cc1): a
// malformed-but-credential-bearing URL must never leak user:pass.
func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantOut string // exact expected output; empty means "assert no creds" only
	}{
		{
			name:    "strips userinfo from valid url",
			raw:     "https://user:secret@github.com/org/repo.git",
			wantOut: "https://github.com/org/repo.git",
		},
		{
			name:    "strips query and fragment",
			raw:     "https://user:secret@host/db?token=abc#frag",
			wantOut: "https://host/db",
		},
		{
			name: "no credentials passes through",
			raw:  "https://github.com/org/repo.git",
			// url.Parse re-serializes identically here.
			wantOut: "https://github.com/org/repo.git",
		},
		{
			// A control character makes url.Parse fail; the old code returned
			// the raw string, leaking user:secret. The fix must redact instead.
			name: "parse failure does not leak credentials",
			raw:  "http://user:secret@host\x7f/db",
		},
		{
			// Schemeless "user:secret@host/path" parses OK with nil User (the
			// creds land in Path/Opaque), so clearing User leaves them intact.
			// Must be redacted wholesale (beads-enax).
			name: "schemeless userinfo does not leak credentials",
			raw:  "user:secret@github.com/org/repo.git",
		},
		{
			// scp-like remote (git@host:path) — '@' before any '/', no scheme.
			name: "scp-like remote does not leak credentials",
			raw:  "deploy:secret@github.com:org/repo.git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeURL(tt.raw)
			if strings.Contains(got, "secret") || strings.Contains(got, "user:") {
				t.Errorf("SanitizeURL(%q) = %q, leaks credentials", tt.raw, got)
			}
			if tt.wantOut != "" && got != tt.wantOut {
				t.Errorf("SanitizeURL(%q) = %q, want %q", tt.raw, got, tt.wantOut)
			}
		})
	}
}
