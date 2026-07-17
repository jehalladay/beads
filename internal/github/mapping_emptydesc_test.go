package github

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestBeadsIssueToGitHubFields_EmptyDescriptionOmitsBody is the beads-fmb9
// regression: a local issue with an empty Description must NOT emit a "body"
// field, so an update PATCH cannot wipe a non-empty external issue body.
func TestBeadsIssueToGitHubFields_EmptyDescriptionOmitsBody(t *testing.T) {
	config := DefaultMappingConfig()
	issue := &types.Issue{
		Title:       "No body here",
		Description: "",
		Status:      types.StatusOpen,
	}

	fields := BeadsIssueToGitHubFields(issue, config)

	if _, present := fields["body"]; present {
		t.Fatalf("empty Description must omit \"body\" from update fields, got %v", fields["body"])
	}
	// Title is still sent — only the empty body is guarded.
	if fields["title"] != "No body here" {
		t.Errorf("fields[\"title\"] = %v, want \"No body here\"", fields["title"])
	}
}

// TestBeadsIssueToGitHubFields_NonEmptyDescriptionKeepsBody guards the
// non-empty path so the fmb9 fix does not accidentally suppress real bodies.
func TestBeadsIssueToGitHubFields_NonEmptyDescriptionKeepsBody(t *testing.T) {
	config := DefaultMappingConfig()
	issue := &types.Issue{
		Title:       "Has body",
		Description: "real content",
		Status:      types.StatusOpen,
	}

	fields := BeadsIssueToGitHubFields(issue, config)

	if fields["body"] != "real content" {
		t.Errorf("fields[\"body\"] = %v, want \"real content\"", fields["body"])
	}
}
