package main

import (
	"strings"
	"testing"
)

// TestRedactURLCredentials verifies that redactURLCredentials strips embedded
// passwords/tokens from every URL shape a printed git-origin hint can carry,
// while preserving host/path (and bare SSH usernames) so the hint stays
// runnable (beads-v7zc). All fixtures use SYNTHETIC credentials.
func TestRedactURLCredentials(t *testing.T) {
	const secret = "s3cr3t-token"
	cases := []struct {
		name       string
		in         string
		wantOut    string // exact expected output ("" = don't assert exact)
		wantHost   string // must be present in output ("" = skip)
		wantNoLeak bool   // secret must be absent
	}{
		{
			name:       "https user:token stripped, username kept",
			in:         "https://user:" + secret + "@github.com/org/repo.git",
			wantOut:    "https://user@github.com/org/repo.git",
			wantNoLeak: true,
		},
		{
			name:       "schemeless user:token redacted wholesale",
			in:         "user:" + secret + "@github.com/org/repo.git",
			wantOut:    "<redacted-url>",
			wantNoLeak: true,
		},
		{
			name:       "ssh scheme bare username unchanged",
			in:         "ssh://git@github.com/org/repo.git",
			wantOut:    "ssh://git@github.com/org/repo.git",
			wantNoLeak: true,
		},
		{
			name:       "git+ssh bare username unchanged",
			in:         "git+ssh://git@github.com/org/repo.git",
			wantOut:    "git+ssh://git@github.com/org/repo.git",
			wantNoLeak: true,
		},
		{
			name:       "scp-like ssh unchanged (no inline secret)",
			in:         "git@github.com:org/repo.git",
			wantOut:    "git@github.com:org/repo.git",
			wantNoLeak: true,
		},
		{
			name:       "scp-like with inline password redacted wholesale",
			in:         "user:" + secret + "@github.com:org/repo.git",
			wantOut:    "<redacted-url>",
			wantNoLeak: true,
		},
		{
			name:     "no credentials passes through unchanged",
			in:       "https://github.com/org/repo.git",
			wantOut:  "https://github.com/org/repo.git",
			wantHost: "github.com",
		},
		{
			name:    "file url unchanged",
			in:      "file:///data/beads",
			wantOut: "file:///data/beads",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactURLCredentials(tc.in)
			if tc.wantNoLeak && strings.Contains(got, secret) {
				t.Fatalf("redactURLCredentials(%q) = %q — leaks secret", tc.in, got)
			}
			if tc.wantOut != "" && got != tc.wantOut {
				t.Fatalf("redactURLCredentials(%q) = %q, want %q", tc.in, got, tc.wantOut)
			}
			if tc.wantHost != "" && !strings.Contains(got, tc.wantHost) {
				t.Fatalf("redactURLCredentials(%q) = %q — expected host %q preserved", tc.in, got, tc.wantHost)
			}
		})
	}
}
