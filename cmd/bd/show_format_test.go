package main

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// These tests cover the pure show-formatting helpers in show_format.go. In the
// test environment stdout is not a TTY, so ui styling is disabled and output is
// plain text — assertions target the stable structural content (IDs, titles,
// labels, dates), not ANSI styling.

func TestFormatShortIssue(t *testing.T) {
	// Open bug: type badge + title present.
	bug := formatShortIssue(&types.Issue{ID: "bd-1", IssueType: "bug", Priority: 1, Title: "boom", Status: types.StatusOpen})
	for _, want := range []string{"bd-1", "boom", "[bug]"} {
		if !strings.Contains(bug, want) {
			t.Errorf("open bug short line missing %q: %q", want, bug)
		}
	}

	// Open epic: epic badge.
	epic := formatShortIssue(&types.Issue{ID: "bd-e", IssueType: "epic", Priority: 0, Title: "big", Status: types.StatusOpen})
	if !strings.Contains(epic, "[epic]") {
		t.Errorf("epic short line missing [epic]: %q", epic)
	}

	// Plain task: no type badge.
	task := formatShortIssue(&types.Issue{ID: "bd-t", IssueType: "task", Priority: 2, Title: "todo", Status: types.StatusOpen})
	if strings.Contains(task, "[bug]") || strings.Contains(task, "[epic]") {
		t.Errorf("task short line should have no type badge: %q", task)
	}

	// Closed: muted path still carries ID + title + priority.
	closed := formatShortIssue(&types.Issue{ID: "bd-c", IssueType: "task", Priority: 3, Title: "done", Status: types.StatusClosed})
	for _, want := range []string{"bd-c", "done", "P3"} {
		if !strings.Contains(closed, want) {
			t.Errorf("closed short line missing %q: %q", want, closed)
		}
	}
}

func TestFormatIssueHeader(t *testing.T) {
	bug := formatIssueHeader(&types.Issue{ID: "bd-1", IssueType: "bug", Priority: 1, Title: "boom", Status: types.StatusOpen})
	for _, want := range []string{"bd-1", "boom", "[BUG]", "OPEN"} {
		if !strings.Contains(bug, want) {
			t.Errorf("bug header missing %q: %q", want, bug)
		}
	}

	epic := formatIssueHeader(&types.Issue{ID: "bd-e", IssueType: "epic", Priority: 0, Title: "big", Status: types.StatusInProgress})
	if !strings.Contains(epic, "[EPIC]") {
		t.Errorf("epic header missing [EPIC]: %q", epic)
	}

	// Compaction tier emoji appears for level 1/2.
	compacted := formatIssueHeader(&types.Issue{ID: "bd-z", IssueType: "task", Priority: 2, Title: "z", Status: types.StatusOpen, CompactionLevel: 1})
	if !strings.Contains(compacted, "🗜️") {
		t.Errorf("level-1 header missing compaction indicator: %q", compacted)
	}
	compacted2 := formatIssueHeader(&types.Issue{ID: "bd-z2", IssueType: "task", Priority: 2, Title: "z2", Status: types.StatusOpen, CompactionLevel: 2})
	if !strings.Contains(compacted2, "📦") {
		t.Errorf("level-2 header missing compaction indicator: %q", compacted2)
	}
}

func TestFormatIssueMetadata(t *testing.T) {
	started := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	due := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	defer2 := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	ext := "gh-9"
	issue := &types.Issue{
		ID: "bd-1", IssueType: "bug", Title: "t", Status: types.StatusOpen,
		CreatedBy: "team", Assignee: "alice",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC),
		StartedAt: &started, DueAt: &due, DeferUntil: &defer2,
		ExternalRef: &ext, SpecID: "spec-7",
	}
	out := formatIssueMetadata(issue)
	for _, want := range []string{
		"Owner: team", "Assignee: alice", "Type:",
		"Created: 2026-01-01", "Started: 2026-01-03", "Updated: 2026-01-08",
		"Due: 2026-01-10", "Deferred: 2026-01-15",
		"External: gh-9", "Spec: spec-7",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metadata missing %q\n%s", want, out)
		}
	}

	// Closed with a close reason + ephemeral wisp type.
	closed := &types.Issue{
		ID: "bd-c", IssueType: "task", Status: types.StatusClosed,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		CloseReason: "merged", Ephemeral: true, WispType: types.WispType("mail"),
	}
	cout := formatIssueMetadata(closed)
	if !strings.Contains(cout, "Close reason: merged") {
		t.Errorf("closed metadata missing close reason: %s", cout)
	}
	if !strings.Contains(cout, "Wisp type:") || !strings.Contains(cout, "mail") {
		t.Errorf("closed metadata missing wisp type: %s", cout)
	}
}

func TestFormatDependencyLine(t *testing.T) {
	// Active epic dependency.
	dep := &types.IssueWithDependencyMetadata{
		Issue: types.Issue{ID: "bd-2", IssueType: "epic", Priority: 1, Title: "child", Status: types.StatusOpen},
	}
	out := formatDependencyLine("→", dep)
	for _, want := range []string{"→", "bd-2", "child", "(EPIC)"} {
		if !strings.Contains(out, want) {
			t.Errorf("dep line missing %q: %q", want, out)
		}
	}

	// Bug type indicator.
	bugDep := &types.IssueWithDependencyMetadata{
		Issue: types.Issue{ID: "bd-b", IssueType: "bug", Priority: 0, Title: "crash", Status: types.StatusOpen},
	}
	if got := formatDependencyLine("→", bugDep); !strings.Contains(got, "(BUG)") {
		t.Errorf("bug dep line missing (BUG): %q", got)
	}

	// Closed dependency: muted branch, still shows ID/title/priority.
	closed := &types.IssueWithDependencyMetadata{
		Issue: types.Issue{ID: "bd-c", IssueType: "task", Priority: 2, Title: "done", Status: types.StatusClosed},
	}
	cout := formatDependencyLine("←", closed)
	for _, want := range []string{"←", "bd-c", "done", "P2"} {
		if !strings.Contains(cout, want) {
			t.Errorf("closed dep line missing %q: %q", want, cout)
		}
	}
}

func TestFormatSimpleDependencyLine(t *testing.T) {
	active := &types.Issue{ID: "bd-2", IssueType: "task", Priority: 1, Title: "work", Status: types.StatusOpen}
	out := formatSimpleDependencyLine("→", active)
	for _, want := range []string{"→", "bd-2", "work"} {
		if !strings.Contains(out, want) {
			t.Errorf("simple dep line missing %q: %q", want, out)
		}
	}

	closed := &types.Issue{ID: "bd-c", IssueType: "task", Priority: 3, Title: "done", Status: types.StatusClosed}
	cout := formatSimpleDependencyLine("←", closed)
	for _, want := range []string{"←", "bd-c", "done", "P3"} {
		if !strings.Contains(cout, want) {
			t.Errorf("closed simple dep line missing %q: %q", want, cout)
		}
	}
}

func TestFormatIssueLongExtras(t *testing.T) {
	// No extra fields → empty string.
	fmtTime := func(tm time.Time) string { return tm.Format("2006-01-02") }
	if got := formatIssueLongExtras(&types.Issue{ID: "bd-1", Status: types.StatusOpen}, fmtTime); got != "" {
		t.Errorf("bare issue should yield no long extras, got %q", got)
	}

	closedAt := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	compactedAt := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	commit := "abc123"
	est := 45
	issue := &types.Issue{
		ID: "bd-1", Status: types.StatusClosed,
		ClosedAt: &closedAt, ClosedBySession: "sess-9", EstimatedMinutes: &est,
		SourceSystem: "gh", Sender: "bob", Ephemeral: true, Pinned: true, IsTemplate: true,
		MolType: "recipe", WorkType: "fix",
		CompactionLevel: 2, CompactedAt: &compactedAt, CompactedAtCommit: &commit, OriginalSize: 2048,
		AwaitType: "gh:run", AwaitID: "release.yml", Timeout: 30 * time.Minute,
		Waiters:       []string{"bd-a", "bd-b"},
		SourceFormula: "reap", SourceLocation: "loc-1",
		BondedFrom: []types.BondRef{{SourceID: "bd-x", BondType: "sequential"}},
		EventKind:  "created", Actor: "carol", Target: "bd-y", Payload: "{}",
	}
	out := formatIssueLongExtras(issue, fmtTime)
	for _, want := range []string{
		"EXTENDED DETAILS", "Closed at: 2026-02-01", "Closed by session: sess-9",
		"Estimated: 45 minutes", "Source system: gh", "Sender: bob",
		"Ephemeral: yes", "Pinned: yes", "Template: yes", "Mol type: recipe", "Work type: fix",
		"COMPACTION", "Level: 2", "Compacted at: 2026-02-02", "Compacted at commit: abc123", "Original size: 2048 bytes",
		"GATE", "Await type: gh:run", "Await ID: release.yml", "Timeout:", "Waiters: bd-a, bd-b",
		"SOURCE TRACING", "Formula: reap", "Location: loc-1",
		"BONDED FROM", "bd-x (sequential)",
		"EVENT", "Kind: created", "Actor: carol", "Target: bd-y", "Payload: {}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("long extras missing %q\n%s", want, out)
		}
	}
}
