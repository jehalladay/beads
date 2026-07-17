package linear

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestFieldMapperIssueToTracker_EmptyDescriptionOmitted is the beads-fmb9
// regression: an empty local Description must NOT emit a "description" field,
// so a Linear update cannot wipe a non-empty external issue description.
func TestFieldMapperIssueToTracker_EmptyDescriptionOmitted(t *testing.T) {
	m := &linearFieldMapper{config: DefaultMappingConfig()}
	issue := &types.Issue{
		Title:       "No description",
		Description: "",
		Priority:    1,
	}

	updates := m.IssueToTracker(issue)

	if _, present := updates["description"]; present {
		t.Fatalf("empty Description must omit \"description\" from updates, got %v", updates["description"])
	}
	if updates["title"] != "No description" {
		t.Errorf("updates[\"title\"] = %v, want \"No description\"", updates["title"])
	}
}

// TestFieldMapperIssueToTracker_NonEmptyDescriptionKept guards the non-empty path.
func TestFieldMapperIssueToTracker_NonEmptyDescriptionKept(t *testing.T) {
	m := &linearFieldMapper{config: DefaultMappingConfig()}
	issue := &types.Issue{
		Title:       "Has description",
		Description: "real content",
		Priority:    1,
	}

	updates := m.IssueToTracker(issue)

	if updates["description"] != "real content" {
		t.Errorf("updates[\"description\"] = %v, want \"real content\"", updates["description"])
	}
}
