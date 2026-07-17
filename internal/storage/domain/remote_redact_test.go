package domain

import (
	"strings"
	"testing"
)

// TestRedactRemoteURL is the beads-dsib regression guard: UpdateRemote's
// "previous URL %s restored" error must not echo credentials embedded in the
// old remote URL (the enax/lf52 credential-in-error class).
func TestRedactRemoteURL(t *testing.T) {
	const secret = "SUPERSECRETTOKEN"
	leaky := []string{
		"https://user:" + secret + "@github.com/org/repo.git",
		"https://github.com/org/repo.git?token=" + secret,
		"https://" + secret + "@github.com/org/repo.git",
		"user:" + secret + "@github.com/org/repo.git", // no scheme -> opaque/path creds
	}
	for _, in := range leaky {
		got := redactRemoteURL(in)
		if strings.Contains(got, secret) {
			t.Errorf("redactRemoteURL(%q) = %q — still leaks the secret", in, got)
		}
	}

	// Credential-free URLs pass through unchanged so the error stays useful.
	for _, in := range []string{
		"https://doltremoteapi.dolthub.com/org/repo",
		"dolthub://org/repo",
	} {
		if got := redactRemoteURL(in); got != in {
			t.Errorf("redactRemoteURL(%q) = %q — credential-free URL must be unchanged", in, got)
		}
	}

	// Empty stays empty (no spurious placeholder).
	if got := redactRemoteURL(""); got != "" {
		t.Errorf("redactRemoteURL(\"\") = %q, want empty", got)
	}
}
