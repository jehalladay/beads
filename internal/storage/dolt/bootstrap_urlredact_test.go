package dolt

import (
	"strings"
	"testing"
)

// TestRedactBootstrapURL verifies that redactBootstrapURL strips credentials
// from every URL shape the bootstrap success message can print (beads-sh85).
// The success print at BootstrapFromRemoteWithDB previously echoed the raw
// remoteURL, and remoteURL commonly embeds credentials (sync.remote config or
// a git origin URL like https://user:token@host/repo), leaking user:token into
// stderr/logs/CI on a SUCCESSFUL clone.
func TestRedactBootstrapURL(t *testing.T) {
	const secret = "s3cr3t-token"
	cases := []struct {
		name       string
		in         string
		wantSecret bool // whether the secret must be ABSENT from output
		wantHost   string
	}{
		{
			name:     "https with userinfo",
			in:       "https://user:" + secret + "@github.com/org/repo.git",
			wantHost: "github.com",
		},
		{
			name:     "ssh scheme with userinfo and port",
			in:       "ssh://git:" + secret + "@host.example:22/repo",
			wantHost: "host.example",
		},
		{
			// url.Parse routes "user:pass@host/path" (no // authority) to an
			// OPAQUE URL (scheme=user, opaque=pass@host/...), so clearing
			// parsed.User leaves the secret intact — must be redacted wholesale.
			name: "schemeless scp-like with userinfo",
			in:   "user:" + secret + "@github.com/org/repo.git",
		},
		{
			name:     "query string does not leak but url is preserved",
			in:       "https://github.com/org/repo.git?token=" + secret,
			wantHost: "github.com",
		},
		{
			name:     "no credentials passes through",
			in:       "https://github.com/org/repo.git",
			wantHost: "github.com",
		},
		{
			name:     "file url passes through",
			in:       "file:///data/beads",
			wantHost: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactBootstrapURL(tc.in)
			if strings.Contains(got, secret) {
				t.Fatalf("redactBootstrapURL(%q) = %q — leaks secret %q", tc.in, got, secret)
			}
			if tc.wantHost != "" && !strings.Contains(got, tc.wantHost) {
				t.Fatalf("redactBootstrapURL(%q) = %q — expected to preserve host %q", tc.in, got, tc.wantHost)
			}
		})
	}
}

// TestRedactBootstrapURL_MalformedRedactsWholesale verifies that a URL which
// fails to parse is redacted wholesale rather than echoed raw (a malformed but
// credential-bearing string must never reach stderr).
func TestRedactBootstrapURL_MalformedRedactsWholesale(t *testing.T) {
	// A control character makes url.Parse fail.
	in := "https://user:s3cr3t-token@host\x7f/repo"
	got := redactBootstrapURL(in)
	if strings.Contains(got, "s3cr3t-token") {
		t.Fatalf("redactBootstrapURL(%q) = %q — leaks secret on parse failure", in, got)
	}
}
