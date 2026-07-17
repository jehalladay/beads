package gitlab

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestBeadsIssueToGitLabFields_EmptyDescriptionOmitted is the beads-fmb9
// regression: an empty local Description must NOT emit a "description" field,
// so an update PUT cannot wipe a non-empty external issue description.
func TestBeadsIssueToGitLabFields_EmptyDescriptionOmitted(t *testing.T) {
	config := DefaultMappingConfig()
	issue := &types.Issue{
		Title:       "No description",
		Description: "",
		Status:      types.StatusOpen,
	}

	fields := BeadsIssueToGitLabFields(issue, config)

	if _, present := fields["description"]; present {
		t.Fatalf("empty Description must omit \"description\" from update fields, got %v", fields["description"])
	}
	if fields["title"] != "No description" {
		t.Errorf("fields[\"title\"] = %v, want \"No description\"", fields["title"])
	}
}

// TestBeadsIssueToGitLabFields_NonEmptyDescriptionKept guards the non-empty path.
func TestBeadsIssueToGitLabFields_NonEmptyDescriptionKept(t *testing.T) {
	config := DefaultMappingConfig()
	issue := &types.Issue{
		Title:       "Has description",
		Description: "real content",
		Status:      types.StatusOpen,
	}

	fields := BeadsIssueToGitLabFields(issue, config)

	if fields["description"] != "real content" {
		t.Errorf("fields[\"description\"] = %v, want \"real content\"", fields["description"])
	}
}
