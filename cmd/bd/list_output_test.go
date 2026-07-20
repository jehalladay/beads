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
	// beads-ibud: an arbitrary --format value is a PER-ISSUE Go template rendered
	// against the issue struct, so exported fields (.ID/.Title/...) resolve by name
	// and EVERY issue produces a line — regardless of dependencies. (Edge-oriented
	// output is the 'digraph' preset; see TestOutputFormattedList_DigraphPreset.)
	// Previously all --format values ran the per-edge path, so a documented
	// per-issue template like '{{.ID}}' saw only edge-map keys → "<no value>".
	issues := []*types.Issue{
		{ID: "bd-1", Title: "root", Status: types.StatusOpen},
		{ID: "bd-2", Title: "child", Status: types.StatusOpen},
	}
	deps := map[string][]*types.Dependency{
		"bd-2": {{IssueID: "bd-2", DependsOnID: "bd-1", Type: "blocks"}},
	}
	out := captureStdout(t, func() error {
		return outputFormattedList(issues, deps, "{{.ID}}|{{.Title}}")
	})
	// Both issues render (not just the one with a dependency), with real fields.
	if !strings.Contains(out, "bd-1|root") {
		t.Errorf("custom per-issue template missing bd-1: %q", out)
	}
	if !strings.Contains(out, "bd-2|child") {
		t.Errorf("custom per-issue template missing bd-2: %q", out)
	}
	if strings.Contains(out, "<no value>") {
		t.Errorf("custom per-issue template rendered <no value> (beads-ibud regression): %q", out)
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
	// A template that indexes a scalar issue field fails at execution time (not
	// parse time), exercising the per-issue Execute error branch (beads-ibud:
	// arbitrary --format now renders per-issue against the issue struct).
	issues := []*types.Issue{
		{ID: "bd-1", Title: "root", Status: types.StatusOpen},
		{ID: "bd-2", Title: "child", Status: types.StatusOpen},
	}
	deps := map[string][]*types.Dependency{
		"bd-2": {{IssueID: "bd-2", DependsOnID: "bd-1", Type: "blocks"}},
	}
	err := withSuppressedStdout(t, func() error {
		// index with too many args on a string field is an execution-time error.
		return outputFormattedList(issues, deps, "{{index .Title 1 2}}")
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

// TestPrintJSONTruncationWarn is the teeth for beads-qyoff: bd list --json must
// warn on stderr when the result is truncated, UNCONDITIONALLY (not
// terminal-gated like printTruncationHint) — a JSON consumer piping stdout is
// exactly the case that must still learn results were hidden. Matches bd
// search (beads-uopti) / ready, which warn unconditionally; list was the
// outlier that dropped the signal under a pipe.
func TestPrintJSONTruncationWarn(t *testing.T) {
	capture := func(fn func()) string {
		old := os.Stderr
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		os.Stderr = w
		fn()
		w.Close()
		os.Stderr = old
		buf := make([]byte, 4096)
		n, _ := r.Read(buf)
		return string(buf[:n])
	}

	t.Run("truncated emits warning to non-terminal stderr", func(t *testing.T) {
		// os.Pipe stderr is NOT a terminal — printTruncationHint would suppress
		// here; printJSONTruncationWarn must NOT.
		got := capture(func() { printJSONTruncationWarn(true, 50) })
		if !strings.Contains(got, "more results matched but were hidden by --limit") {
			t.Fatalf("beads-qyoff: expected truncation warning on non-terminal stderr, got %q", got)
		}
	})

	t.Run("not truncated is silent", func(t *testing.T) {
		got := capture(func() { printJSONTruncationWarn(false, 50) })
		if got != "" {
			t.Fatalf("expected no output when not truncated, got %q", got)
		}
	})

	t.Run("zero limit is silent", func(t *testing.T) {
		got := capture(func() { printJSONTruncationWarn(true, 0) })
		if got != "" {
			t.Fatalf("expected no output when effectiveLimit<=0, got %q", got)
		}
	})
}
