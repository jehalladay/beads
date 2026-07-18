package linear

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestTruncateTitle_CapsLongTitle is the teeth for beads-exaq: Linear rejects an
// issue title longer than maxTitleLength characters, but a beads title may be up
// to 500. The PUSHED copy must be capped (rune-aware, with a visible ellipsis)
// while the local title is left untouched — mirroring the github/gitlab/jira/ado
// guards. Completes the SCM per-field-cap class across all 5 push-capable
// providers.
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
// through byte-identical (no needless ellipsis).
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

// TestIssueToTracker_TitleCapped confirms the update push path caps the title
// (fieldmapper.go IssueToTracker sets updates["title"] = issue.Title) while
// leaving the local beads title untouched.
func TestIssueToTracker_TitleCapped(t *testing.T) {
	m := &linearFieldMapper{config: DefaultMappingConfig()}
	issue := &types.Issue{Title: strings.Repeat("x", 300)}
	updates := m.IssueToTracker(issue)
	title, _ := updates["title"].(string)
	if n := len([]rune(title)); n != maxTitleLength {
		t.Errorf("update-path title is %d runes, want %d (uncapped push is rejected)", n, maxTitleLength)
	}
	// The local issue title must be left untouched.
	if len([]rune(issue.Title)) != 300 {
		t.Errorf("local issue.Title was mutated: %d runes", len([]rune(issue.Title)))
	}
}
