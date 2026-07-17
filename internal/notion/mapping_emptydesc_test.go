package notion

import (
	"testing"
)

// TestBuildPageProperties_EmptyDescriptionOmitted is the beads-fmb9
// regression: an empty Description must NOT emit the Description property, so
// an UpdatePage cannot wipe a non-empty external page body.
func TestBuildPageProperties_EmptyDescriptionOmitted(t *testing.T) {
	pushIssue := &PushIssue{
		ID:          "bd-1",
		Title:       "No description",
		Description: "",
		Status:      "open",
		Priority:    "P2",
	}

	props := BuildPageProperties(pushIssue)

	if _, present := props[PropertyDescription]; present {
		t.Fatalf("empty Description must omit %q property, got %v", PropertyDescription, props[PropertyDescription])
	}
	// Title is still emitted — only the empty description is guarded.
	if _, present := props[PropertyTitle]; !present {
		t.Errorf("%q property must still be present", PropertyTitle)
	}
}

// TestBuildPageProperties_NonEmptyDescriptionKept guards the non-empty path.
func TestBuildPageProperties_NonEmptyDescriptionKept(t *testing.T) {
	pushIssue := &PushIssue{
		ID:          "bd-1",
		Title:       "Has description",
		Description: "real content",
		Status:      "open",
		Priority:    "P2",
	}

	props := BuildPageProperties(pushIssue)

	if _, present := props[PropertyDescription]; !present {
		t.Fatalf("non-empty Description must emit %q property", PropertyDescription)
	}
}
