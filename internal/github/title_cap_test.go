package github

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestTruncateTitle_CapsLongTitle is the teeth for beads-yaum: GitHub's REST
// API 422-rejects an issue title longer than maxTitleLength characters, but a
// beads title may be up to 500. The PUSHED copy must be capped (rune-aware,
// with a visible ellipsis) while the local title is left untouched — mirroring
// the ado/gitlab guards.
func TestTruncateTitle_CapsLongTitle(t *testing.T) {
	long := strings.Repeat("a", 300)
	got := truncateTitle(long)
	if n := len([]rune(got)); n != maxTitleLength {
		t.Errorf("truncated to %d runes, want %d", n, maxTitleLength)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated title has no ellipsis marker: %q...", got[:20])
	}
}

// TestTruncateTitle_ShortTitleUnchanged confirms a title within the cap passes
// through byte-identical (no needless ellipsis, no allocation surprise).
func TestTruncateTitle_ShortTitleUnchanged(t *testing.T) {
	short := "a short title"
	if got := truncateTitle(short); got != short {
		t.Errorf("truncateTitle(%q) = %q, want unchanged", short, got)
	}
	// Exactly at the cap must also be unchanged.
	exact := strings.Repeat("b", maxTitleLength)
	if got := truncateTitle(exact); got != exact {
		t.Errorf("title exactly at cap was altered (len %d)", len([]rune(got)))
	}
}

// TestTruncateTitle_RuneAware confirms a multi-byte rune is never split by the
// cap: an over-cap title of multi-byte runes truncates on a rune boundary.
func TestTruncateTitle_RuneAware(t *testing.T) {
	// 300 multi-byte runes (each 'é' is 2 bytes) — well over the rune cap.
	long := strings.Repeat("é", 300)
	got := truncateTitle(long)
	if n := len([]rune(got)); n != maxTitleLength {
		t.Errorf("truncated to %d runes, want %d", n, maxTitleLength)
	}
	// The result must be valid UTF-8 (no split rune): re-decoding round-trips.
	if string([]rune(got)) != got {
		t.Error("truncation split a multi-byte rune")
	}
}

// TestBeadsIssueToGitHubFields_TitleCapped confirms the update push path caps
// the title (mapping.go sets "title": issue.Title).
func TestBeadsIssueToGitHubFields_TitleCapped(t *testing.T) {
	issue := &types.Issue{Title: strings.Repeat("x", 300)}
	fields := BeadsIssueToGitHubFields(issue, nil)
	title, _ := fields["title"].(string)
	if n := len([]rune(title)); n != maxTitleLength {
		t.Errorf("update-path title is %d runes, want <= %d (uncapped push 422-fails)", n, maxTitleLength)
	}
	// The local issue title must be left untouched.
	if len([]rune(issue.Title)) != 300 {
		t.Errorf("local issue.Title was mutated: %d runes", len([]rune(issue.Title)))
	}
}
