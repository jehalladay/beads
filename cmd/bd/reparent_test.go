// reparent_test.go - Test that reparented issues don't appear under old parent.

//go:build cgo && integration

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCLI_ReparentDottedIDExcludesOldParent tests that after reparenting a
// dotted-ID child to a new parent, it no longer appears under the old parent
// in `bd list --parent`.
//
// The bug: dotted-ID prefix matching (e.g., "parent.1" matches parent "parent")
// continued to show the child under the old parent even after an explicit
// parent-child dependency reparented it elsewhere.
//
// The fix: explicit parent-child dependencies take precedence over dotted-ID
// prefix matching.
func TestCLI_ReparentDottedIDExcludesOldParent(t *testing.T) {
	if testDoltServerPort == 0 {
		t.Skip("skipping: Dolt test container not available")
	}
	tmpDir := initExecTestDB(t)

	// Create parentA — will get an auto-generated ID like "test-xxxxx"
	parentA := createExecTestIssue(t, tmpDir, "Parent A")

	// Create a dotted-ID child: parentA + ".1" — this triggers prefix matching
	dottedChildID := parentA + ".1"
	child := createExecTestIssueWithID(t, tmpDir, "Dotted Child", dottedChildID)
	if child != dottedChildID {
		t.Fatalf("expected child ID %s, got %s", dottedChildID, child)
	}

	// Create parentB
	parentB := createExecTestIssue(t, tmpDir, "Parent B")

	// Before any deps: dotted child should appear under parentA via prefix match
	assertParentLists(t, tmpDir, parentA, dottedChildID, true,
		"dotted child should appear under parentA via prefix match before reparenting")

	// Reparent: add explicit parent-child dep to parentB
	runBD(t, tmpDir, "dep", "add", dottedChildID, parentB, "--type", "parent-child")

	// After reparenting: child should NOT appear under parentA
	assertParentLists(t, tmpDir, parentA, dottedChildID, false,
		"dotted child should NOT appear under old parent after reparenting to parentB")

	// After reparenting: child SHOULD appear under parentB
	assertParentLists(t, tmpDir, parentB, dottedChildID, true,
		"dotted child should appear under new parent parentB after reparenting")
}

// createExecTestIssueWithID creates an issue with an explicit ID.
func createExecTestIssueWithID(t *testing.T, tmpDir, title, id string) string {
	t.Helper()
	cmd := exec.Command(testBD, "create", title, "-p", "1", "--id", id, "--json")
	cmd.Dir = tmpDir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("create with --id %s failed: %v\n%s", id, err, out)
	}
	jsonStart := strings.Index(string(out), "{")
	if jsonStart < 0 {
		t.Fatalf("No JSON in create output: %s", out)
	}
	var issue map[string]interface{}
	if err := json.Unmarshal(out[jsonStart:], &issue); err != nil {
		t.Fatalf("parse create JSON: %v\n%s", err, out)
	}
	return issue["id"].(string)
}

// runBD runs a bd command and fails the test on error.
func runBD(t *testing.T, tmpDir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(testBD, args...)
	cmd.Dir = tmpDir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// assertParentLists checks whether childID appears in `bd list --parent parentID`.
func assertParentLists(t *testing.T, tmpDir, parentID, childID string, shouldAppear bool, msg string) {
	t.Helper()
	cmd := exec.Command(testBD, "list", "--parent", parentID, "--json")
	cmd.Dir = tmpDir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if shouldAppear {
			t.Fatalf("list --parent %s failed: %v\n%s", parentID, err, out)
		}
		return // empty list may fail; that's fine if we expected absence
	}

	var issues []map[string]interface{}
	if err := json.Unmarshal(out, &issues); err != nil {
		// Might be empty output
		if shouldAppear {
			t.Fatalf("parse list JSON: %v\n%s", err, out)
		}
		return
	}

	found := false
	for _, iss := range issues {
		if iss["id"] == childID {
			found = true
		}
	}
	if shouldAppear && !found {
		t.Errorf("%s (child=%s, parent=%s)", msg, childID, parentID)
	}
	if !shouldAppear && found {
		t.Errorf("%s (child=%s, parent=%s)", msg, childID, parentID)
	}
}

// parentChildCount returns how many parent-child dependencies childID has
// (via `bd dep list <child> --json`), counting only "parent-child" edges.
func parentChildCount(t *testing.T, tmpDir, childID string) int {
	t.Helper()
	cmd := exec.Command(testBD, "dep", "list", childID, "--json")
	cmd.Dir = tmpDir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dep list %s failed: %v\n%s", childID, err, out)
	}
	// dep list --json shapes vary; count parent-child occurrences robustly by
	// scanning the raw JSON for the type string. Each edge serializes its type
	// once, so this counts edges.
	return strings.Count(string(out), "parent-child")
}

// TestCLI_ReparentRemovesAllOldParents is the beads-94ia regression: a child
// with MORE THAN ONE parent-child edge (added directly via `bd dep add ...
// --type parent-child`, which has no single-parent guard) must have ALL prior
// parent edges removed when reparented via `bd update --parent`, not just the
// first. The buggy inline loop in update.go `break`ed after the first removal,
// silently leaving stale parents that corrupt tree/ready-work/blocked-state.
func TestCLI_ReparentRemovesAllOldParents(t *testing.T) {
	if testDoltServerPort == 0 {
		t.Skip("skipping: Dolt test container not available")
	}
	tmpDir := initExecTestDB(t)

	child := createExecTestIssue(t, tmpDir, "child")
	parentA := createExecTestIssue(t, tmpDir, "parent A")
	parentB := createExecTestIssue(t, tmpDir, "parent B")
	parentC := createExecTestIssue(t, tmpDir, "new parent C")

	// Give the child TWO parents directly (no single-parent guard on dep add).
	runBD(t, tmpDir, "dep", "add", child, parentA, "--type", "parent-child")
	runBD(t, tmpDir, "dep", "add", child, parentB, "--type", "parent-child")
	if got := parentChildCount(t, tmpDir, child); got != 2 {
		t.Fatalf("setup: child should have 2 parent-child edges, got %d", got)
	}

	// Reparent to C via `bd update --parent`.
	runBD(t, tmpDir, "update", child, "--parent", parentC)

	// The child must now have exactly ONE parent-child edge (C). The bug left
	// a stale edge behind (count would be 2: the un-removed old parent + C).
	if got := parentChildCount(t, tmpDir, child); got != 1 {
		t.Errorf("after reparent, child should have exactly 1 parent-child edge, got %d", got)
	}
	// It must appear under C and under neither A nor B.
	assertParentLists(t, tmpDir, parentC, child, true,
		"child should appear under new parent C after reparent")
	assertParentLists(t, tmpDir, parentA, child, false,
		"child must NOT remain under old parent A after reparent")
	assertParentLists(t, tmpDir, parentB, child, false,
		"child must NOT remain under old parent B after reparent (beads-94ia stale-parent bug)")
}
