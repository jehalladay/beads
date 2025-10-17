package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/sqlite"
	"github.com/steveyegge/beads/internal/types"
)

func TestOutputJSON(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Test data
	testData := map[string]interface{}{
		"id":    "bd-1",
		"title": "Test Issue",
		"count": 42,
	}

	// Call outputJSON
	outputJSON(testData)

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify it's valid JSON
	var result map[string]interface{}
	err := json.Unmarshal([]byte(output), &result)
	if err != nil {
		t.Fatalf("outputJSON did not produce valid JSON: %v", err)
	}

	// Verify content
	if result["id"] != "bd-1" {
		t.Errorf("Expected id 'bd-1', got '%v'", result["id"])
	}
	if result["title"] != "Test Issue" {
		t.Errorf("Expected title 'Test Issue', got '%v'", result["title"])
	}
	// Note: JSON numbers are float64
	if result["count"] != float64(42) {
		t.Errorf("Expected count 42, got %v", result["count"])
	}
}

func TestOutputJSONArray(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Test data - array of issues
	testData := []map[string]string{
		{"id": "bd-1", "title": "First"},
		{"id": "bd-2", "title": "Second"},
	}

	// Call outputJSON
	outputJSON(testData)

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify it's valid JSON array
	var result []map[string]string
	err := json.Unmarshal([]byte(output), &result)
	if err != nil {
		t.Fatalf("outputJSON did not produce valid JSON array: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(result))
	}
}

func TestPrintCollisionReport(t *testing.T) {
	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Create collision data
	result := &sqlite.CollisionResult{
		ExactMatches: []string{"bd-1", "bd-2"},
		NewIssues:    []string{"bd-3", "bd-4", "bd-5"},
		Collisions: []*sqlite.CollisionDetail{
			{
				ID: "bd-6",
				IncomingIssue: &types.Issue{
					ID:    "bd-6",
					Title: "Test Issue 6",
				},
				ConflictingFields: []string{"title", "priority"},
			},
			{
				ID: "bd-7",
				IncomingIssue: &types.Issue{
					ID:    "bd-7",
					Title: "Test Issue 7",
				},
				ConflictingFields: []string{"description"},
			},
		},
	}

	// Call printCollisionReport
	printCollisionReport(result)

	// Restore stderr
	w.Close()
	os.Stderr = oldStderr

	// Read output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify output contains expected sections
	if !strings.Contains(output, "Collision Detection Report") {
		t.Errorf("Expected report header. Got: %s", output)
	}
	if !strings.Contains(output, "Exact matches (idempotent): 2") {
		t.Errorf("Expected exact matches count. Got: %s", output)
	}
	if !strings.Contains(output, "New issues: 3") {
		t.Errorf("Expected new issues count. Got: %s", output)
	}
	if !strings.Contains(output, "COLLISIONS DETECTED: 2") {
		t.Errorf("Expected collisions count. Got: %s", output)
	}
	if !strings.Contains(output, "bd-6") {
		t.Errorf("Expected first collision ID. Got: %s", output)
	}
	// The field names are printed directly, not in brackets
	if !strings.Contains(output, "title") || !strings.Contains(output, "priority") {
		t.Errorf("Expected conflicting fields for bd-6. Got: %s", output)
	}
}

func TestPrintCollisionReportNoCollisions(t *testing.T) {
	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Create data with no collisions
	result := &sqlite.CollisionResult{
		ExactMatches: []string{"bd-1", "bd-2", "bd-3"},
		NewIssues:    []string{"bd-4"},
		Collisions:   []*sqlite.CollisionDetail{},
	}

	// Call printCollisionReport
	printCollisionReport(result)

	// Restore stderr
	w.Close()
	os.Stderr = oldStderr

	// Read output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify output shows no collisions
	if !strings.Contains(output, "COLLISIONS DETECTED: 0") {
		t.Error("Expected 0 collisions")
	}
	if strings.Contains(output, "Colliding issues:") {
		t.Error("Should not show colliding issues section when there are none")
	}
}

func TestPrintRemappingReport(t *testing.T) {
	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Create remapping data
	remapping := map[string]string{
		"bd-10": "bd-100",
		"bd-20": "bd-200",
		"bd-30": "bd-300",
	}
	collisions := []*sqlite.CollisionDetail{
		{ID: "bd-10", ReferenceScore: 5},
		{ID: "bd-20", ReferenceScore: 0},
		{ID: "bd-30", ReferenceScore: 12},
	}

	// Call printRemappingReport
	printRemappingReport(remapping, collisions)

	// Restore stderr
	w.Close()
	os.Stderr = oldStderr

	// Read output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify output contains expected information
	if !strings.Contains(output, "Remapping Report") {
		t.Errorf("Expected report title. Got: %s", output)
	}
	if !strings.Contains(output, "bd-10 → bd-100") {
		t.Error("Expected first remapping")
	}
	if !strings.Contains(output, "refs: 5") {
		t.Error("Expected reference count for bd-10")
	}
	if !strings.Contains(output, "bd-20 → bd-200") {
		t.Error("Expected second remapping")
	}
	if !strings.Contains(output, "refs: 0") {
		t.Error("Expected 0 references for bd-20")
	}
	if !strings.Contains(output, "bd-30 → bd-300") {
		t.Error("Expected third remapping")
	}
	if !strings.Contains(output, "refs: 12") {
		t.Error("Expected reference count for bd-30")
	}
}

func TestPrintRemappingReportEmpty(t *testing.T) {
	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Empty remapping
	remapping := map[string]string{}
	collisions := []*sqlite.CollisionDetail{}

	// Call printRemappingReport
	printRemappingReport(remapping, collisions)

	// Restore stderr
	w.Close()
	os.Stderr = oldStderr

	// Read output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Should still have header
	if !strings.Contains(output, "Remapping Report") {
		t.Errorf("Expected report title even with no remappings. Got: %s", output)
	}
}

func TestPrintRemappingReportOrdering(t *testing.T) {
	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Create remapping with different reference scores
	// Ordering is by reference score (ascending)
	remapping := map[string]string{
		"bd-2":   "bd-200",
		"bd-10":  "bd-100",
		"bd-100": "bd-1000",
	}
	collisions := []*sqlite.CollisionDetail{
		{ID: "bd-2", ReferenceScore: 10},  // highest refs
		{ID: "bd-10", ReferenceScore: 5},  // medium refs
		{ID: "bd-100", ReferenceScore: 1}, // lowest refs
	}

	// Call printRemappingReport
	printRemappingReport(remapping, collisions)

	// Restore stderr
	w.Close()
	os.Stderr = oldStderr

	// Read output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Find positions of each remapping in output
	pos2 := strings.Index(output, "bd-2 →")
	pos10 := strings.Index(output, "bd-10 →")
	pos100 := strings.Index(output, "bd-100 →")

	// Verify ordering by reference score (ascending): bd-100 (1 ref) < bd-10 (5 refs) < bd-2 (10 refs)
	if pos2 == -1 || pos10 == -1 || pos100 == -1 {
		t.Fatalf("Missing remappings in output: %s", output)
	}
	if !(pos100 < pos10 && pos10 < pos2) {
		t.Errorf("Remappings not in reference score order. Got: %s", output)
	}
}

// Note: createIssuesFromMarkdown is tested via cmd/bd/markdown_test.go which has
// comprehensive tests for the markdown parsing functionality. We don't duplicate
// those tests here since they require full DB setup.
