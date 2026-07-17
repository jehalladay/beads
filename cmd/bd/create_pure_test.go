package main

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-tiat: hermetic tests for pure/output helpers in create.go (verified 0% +
// no test references).

func TestCreateDepsAcceptedTypeList(t *testing.T) {
	got := createDepsAcceptedTypeList()
	// Always includes the two CLI-friendly aliases plus the well-known types,
	// sorted and comma-joined.
	for _, want := range []string{"blocked-by", "depends-on", "blocks", "parent-child"} {
		if !strings.Contains(got, want) {
			t.Errorf("accepted-type list missing %q: %q", want, got)
		}
	}
	// Sorted: verify the comma-separated tokens are in ascending order.
	parts := strings.Split(got, ", ")
	for i := 1; i < len(parts); i++ {
		if parts[i-1] > parts[i] {
			t.Errorf("list not sorted at %d: %v", i, parts)
		}
	}
}

func TestFormatTimeForRPC(t *testing.T) {
	if got := formatTimeForRPC(nil); got != "" {
		t.Errorf("nil time → %q, want empty", got)
	}
	tm := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := formatTimeForRPC(&tm); got != "2026-01-02T03:04:05Z" {
		t.Errorf("formatTimeForRPC = %q, want RFC3339", got)
	}
}

func TestRenderCreateDryRunPreview(t *testing.T) {
	t.Run("full issue prints all sections", func(t *testing.T) {
		issue := &types.Issue{
			ID:          "bd-1",
			Title:       "Do the thing",
			IssueType:   types.TypeBug,
			Priority:    1,
			Status:      types.StatusOpen,
			Assignee:    "alice",
			Description: "some detail",
			EventKind:   "patrol.muted",
		}
		out := captureStdout(t, func() error {
			renderCreateDryRunPreview(issue, []string{"urgent", "backend"}, []string{"bd-2"})
			return nil
		})
		for _, want := range []string{
			"DRY RUN", "bd-1", "Do the thing", "P1", "alice",
			"some detail", "urgent, backend", "bd-2", "patrol.muted",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("dry-run output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("unset ID shows placeholder; empty optionals omitted", func(t *testing.T) {
		issue := &types.Issue{Title: "T", IssueType: types.TypeTask, Priority: 2, Status: types.StatusOpen}
		out := captureStdout(t, func() error {
			renderCreateDryRunPreview(issue, nil, nil)
			return nil
		})
		if !strings.Contains(out, "(will be generated)") {
			t.Errorf("expected ID placeholder, got:\n%s", out)
		}
		// No assignee/labels/deps/description/event lines when those are empty.
		for _, absent := range []string{"Assignee:", "Labels:", "Dependencies:", "Description:", "Event category:"} {
			if strings.Contains(out, absent) {
				t.Errorf("did not expect %q for a minimal issue:\n%s", absent, out)
			}
		}
	})
}
