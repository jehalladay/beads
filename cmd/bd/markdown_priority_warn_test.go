package main

import (
	"strings"
	"testing"
)

// TestProcessIssueSection_InvalidPriorityWarns verifies beads-ljmx: an
// unparseable "### Priority" value must emit a warning (matching the adjacent
// "### Type" behavior) instead of being silently dropped to the default.
func TestProcessIssueSection_InvalidPriorityWarns(t *testing.T) {
	t.Run("invalid priority warns and keeps default", func(t *testing.T) {
		issue := &IssueTemplate{Title: "Some Issue", Priority: 2}
		out := captureStderr(t, func() {
			processIssueSection(issue, "priority", "urgent")
		})
		if !strings.Contains(out, "invalid priority") {
			t.Errorf("expected a warning mentioning 'invalid priority', got: %q", out)
		}
		if !strings.Contains(out, "urgent") {
			t.Errorf("expected the warning to include the offending value 'urgent', got: %q", out)
		}
		if issue.Priority != 2 {
			t.Errorf("expected priority to keep the default 2, got %d", issue.Priority)
		}
	})

	t.Run("valid priority does not warn", func(t *testing.T) {
		issue := &IssueTemplate{Title: "Some Issue", Priority: 2}
		out := captureStderr(t, func() {
			processIssueSection(issue, "priority", "0")
		})
		if out != "" {
			t.Errorf("expected no warning for a valid priority, got: %q", out)
		}
		if issue.Priority != 0 {
			t.Errorf("expected priority to be set to 0, got %d", issue.Priority)
		}
	})
}
