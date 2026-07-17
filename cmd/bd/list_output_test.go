package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestOutputDotFormat(t *testing.T) {
	issues := []*types.Issue{
		{ID: "bd-1", IssueType: "bug", Priority: 1, Title: "root", Status: types.StatusOpen},
		{ID: "bd-2", IssueType: "task", Priority: 2, Title: "child", Status: types.StatusInProgress},
		{ID: "bd-3", IssueType: "task", Priority: 0, Title: "done", Status: types.StatusClosed},
		{ID: "bd-4", IssueType: "task", Priority: 3, Title: "stuck", Status: types.StatusBlocked},
	}
	deps := map[string][]*types.Dependency{
		"bd-2": {
			{IssueID: "bd-2", DependsOnID: "bd-1", Type: "blocks"},
			{IssueID: "bd-2", DependsOnID: "bd-3", Type: "parent-child"},
			// Edge to a node outside the filtered set is skipped.
			{IssueID: "bd-2", DependsOnID: "bd-999", Type: "related"},
		},
		"bd-4": {
			{IssueID: "bd-4", DependsOnID: "bd-1", Type: "discovered-from"},
			{IssueID: "bd-4", DependsOnID: "bd-3", Type: "related"},
		},
	}

	out := captureStdout(t, func() error { return outputDotFormat(issues, deps) })

	for _, want := range []string{
		"digraph dependencies {",
		"rankdir=TB;",
		`"bd-1"`,
		"lightgray",        // closed fill
		"lightyellow",      // in_progress fill
		"lightcoral",       // blocked fill
		`"bd-2" -> "bd-1"`, // blocks edge
		"color=red",
		"color=blue",  // parent-child
		"color=green", // discovered-from
		"}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dot output missing %q\n---\n%s", want, out)
		}
	}
	// Edge to node outside the set must NOT appear.
	if strings.Contains(out, "bd-999") {
		t.Errorf("dot output should skip edges to unfiltered nodes, got:\n%s", out)
	}
}

func TestOutputFormattedList_DotDelegates(t *testing.T) {
	issues := []*types.Issue{{ID: "bd-1", Title: "x", Status: types.StatusOpen}}
	out := captureStdout(t, func() error { return outputFormattedList(issues, nil, "dot") })
	if !strings.Contains(out, "digraph dependencies {") {
		t.Errorf("format=dot should delegate to dot output, got:\n%s", out)
	}
}

func TestOutputFormattedList_DigraphPreset(t *testing.T) {
	issues := []*types.Issue{
		{ID: "bd-1", Title: "root", Status: types.StatusOpen},
		{ID: "bd-2", Title: "child", Status: types.StatusOpen},
	}
	deps := map[string][]*types.Dependency{
		"bd-2": {{IssueID: "bd-2", DependsOnID: "bd-1", Type: "blocks"}},
	}
	out := captureStdout(t, func() error { return outputFormattedList(issues, deps, "digraph") })
	if !strings.Contains(out, "bd-2 bd-1") {
		t.Errorf("digraph preset output = %q, want 'bd-2 bd-1'", out)
	}
}

func TestOutputFormattedList_CustomTemplate(t *testing.T) {
	issues := []*types.Issue{
		{ID: "bd-1", Title: "root", Status: types.StatusOpen},
		{ID: "bd-2", Title: "child", Status: types.StatusOpen},
	}
	deps := map[string][]*types.Dependency{
		"bd-2": {{IssueID: "bd-2", DependsOnID: "bd-1", Type: "blocks"}},
	}
	out := captureStdout(t, func() error {
		return outputFormattedList(issues, deps, "{{.IssueID}}->{{.DependsOnID}} ({{.Type}})")
	})
	if !strings.Contains(out, "bd-2->bd-1 (blocks)") {
		t.Errorf("custom template output = %q", out)
	}
}

func TestOutputFormattedList_InvalidTemplate(t *testing.T) {
	// Parse error path — must not require stdout capture with error-fatal helper.
	err := withSuppressedStdout(t, func() error {
		return outputFormattedList(nil, nil, "{{.Unclosed")
	})
	if err == nil {
		t.Fatal("expected parse error for malformed template")
	}
	if !strings.Contains(err.Error(), "invalid format template") {
		t.Errorf("error = %v, want 'invalid format template'", err)
	}
}

func TestOutputFormattedList_TemplateExecError(t *testing.T) {
	// A template calling a method with the wrong arg count fails at execution
	// time (not parse time), exercising the Execute error branch.
	issues := []*types.Issue{
		{ID: "bd-1", Title: "root", Status: types.StatusOpen},
		{ID: "bd-2", Title: "child", Status: types.StatusOpen},
	}
	deps := map[string][]*types.Dependency{
		"bd-2": {{IssueID: "bd-2", DependsOnID: "bd-1", Type: "blocks"}},
	}
	err := withSuppressedStdout(t, func() error {
		// index on a struct pointer is an execution-time error.
		return outputFormattedList(issues, deps, "{{index .Issue 1}}")
	})
	if err == nil {
		t.Fatal("expected template execution error")
	}
	if !strings.Contains(err.Error(), "template execution error") {
		t.Errorf("error = %v, want 'template execution error'", err)
	}
}

// withSuppressedStdout runs fn with os.Stdout redirected to the null device and
// returns fn's error. Unlike captureStdout it does not t.Error on a returned
// error, so it can exercise error paths that also print.
func withSuppressedStdout(t *testing.T, fn func() error) error {
	t.Helper()
	stdioMutex.Lock()
	defer stdioMutex.Unlock()

	old := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	os.Stdout = devnull
	defer func() {
		os.Stdout = old
		devnull.Close()
	}()
	return fn()
}
