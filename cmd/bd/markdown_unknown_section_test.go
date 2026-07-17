package main

import (
	"strings"
	"testing"
)

// TestProcessIssueSection_UnknownSectionWarns verifies beads-1cey: an
// unrecognized "### Section" header (a typo like "Priorty" or an unsupported
// section like "Estimate") must emit a warning instead of silently dropping
// the whole section's content — matching the priority/type warning behavior.
func TestProcessIssueSection_UnknownSectionWarns(t *testing.T) {
	t.Run("unknown section warns", func(t *testing.T) {
		issue := &IssueTemplate{Title: "Some Issue"}
		out := captureStderr(t, func() {
			processIssueSection(issue, "Priorty", "1") // typo of Priority
		})
		if !strings.Contains(out, "Priorty") {
			t.Errorf("expected a warning naming the unknown section 'Priorty', got: %q", out)
		}
		if !strings.Contains(strings.ToLower(out), "section") {
			t.Errorf("expected the warning to mention 'section', got: %q", out)
		}
	})

	t.Run("recognized section does not warn", func(t *testing.T) {
		issue := &IssueTemplate{Title: "Some Issue"}
		out := captureStderr(t, func() {
			processIssueSection(issue, "description", "hello")
		})
		if out != "" {
			t.Errorf("expected no warning for a recognized section, got: %q", out)
		}
		if issue.Description != "hello" {
			t.Errorf("expected description to be set, got %q", issue.Description)
		}
	})
}
