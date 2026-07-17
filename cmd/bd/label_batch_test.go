//go:build cgo && integration

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// resetLabelAddFlag clears the repeatable --label StringSlice between in-process
// invocations. The test harness reuses the single rootCmd, and pflag StringSlice
// values accumulate across Execute calls (they append once Changed is set). In a
// real bd invocation this never happens (each run is a fresh process); the reset
// exists only to isolate sequential in-process test calls.
func resetLabelAddFlag(t *testing.T) {
	t.Helper()
	f := labelAddCmd.Flags().Lookup("label")
	if f == nil {
		t.Fatalf("label add is missing the --label flag")
	}
	if sv, ok := f.Value.(pflag.SliceValue); ok {
		_ = sv.Replace([]string{})
	}
	f.Changed = false
}

// TestCLI_LabelAddBatch verifies that `bd label add <id> --label a --label b --label c`
// writes ALL labels in a SINGLE invocation/transaction (beads-9c7). This is the gating
// item for gt prime's delivery-ack storm: mail delivery-ack writes 3 labels/msg and
// needs to do so in one process instead of three separate execs.
func TestCLI_LabelAddBatch(t *testing.T) {
	// Note: Not using t.Parallel() because inProcessMutex serializes execution anyway
	tmpDir := setupCLITestDB(t)

	out := runBDInProcess(t, tmpDir, "create", "Batch label target", "-p", "2", "--json")
	var issue map[string]interface{}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &issue); err != nil {
		t.Fatalf("failed to parse create output: %v\n%s", err, out)
	}
	id := issue["id"].(string)

	// The whole point: three labels in ONE invocation.
	resetLabelAddFlag(t)
	runBDInProcess(t, tmpDir, "label", "add", id, "--label", "delivered", "--label", "acked", "--label", "seen")

	out = runBDInProcess(t, tmpDir, "show", id, "--json")
	var shown []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("failed to parse show output: %v\n%s", err, out)
	}
	labels, _ := shown[0]["labels"].([]interface{})
	got := make(map[string]bool)
	for _, l := range labels {
		got[l.(string)] = true
	}
	for _, want := range []string{"delivered", "acked", "seen"} {
		if !got[want] {
			t.Errorf("expected label %q written by single batch invocation, got labels: %v", want, labels)
		}
	}
	if len(labels) != 3 {
		t.Errorf("expected exactly 3 labels from batch add, got %d: %v", len(labels), labels)
	}
}

// TestCLI_LabelAddBatchWithPositional verifies the positional label and --label flags
// combine (and dedupe) in one invocation, preserving the original grammar.
func TestCLI_LabelAddBatchWithPositional(t *testing.T) {
	tmpDir := setupCLITestDB(t)

	out := runBDInProcess(t, tmpDir, "create", "Mixed label target", "-p", "2", "--json")
	var issue map[string]interface{}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &issue); err != nil {
		t.Fatalf("failed to parse create output: %v\n%s", err, out)
	}
	id := issue["id"].(string)

	// Positional "pos" + flag labels; "pos" duplicated via flag must not double-add.
	resetLabelAddFlag(t)
	runBDInProcess(t, tmpDir, "label", "add", id, "pos", "--label", "flag1", "--label", "pos")

	out = runBDInProcess(t, tmpDir, "show", id, "--json")
	var shown []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("failed to parse show output: %v\n%s", err, out)
	}
	labels, _ := shown[0]["labels"].([]interface{})
	got := make(map[string]bool)
	for _, l := range labels {
		got[l.(string)] = true
	}
	for _, want := range []string{"pos", "flag1"} {
		if !got[want] {
			t.Errorf("expected label %q, got labels: %v", want, labels)
		}
	}
	if len(labels) != 2 {
		t.Errorf("expected exactly 2 labels (deduped), got %d: %v", len(labels), labels)
	}
}

// TestCLI_LabelAddBatchMultipleIssues verifies --label writes every label onto every
// issue when multiple issue IDs are given, all in one invocation.
func TestCLI_LabelAddBatchMultipleIssues(t *testing.T) {
	tmpDir := setupCLITestDB(t)

	mkIssue := func(title string) string {
		out := runBDInProcess(t, tmpDir, "create", title, "-p", "2", "--json")
		var issue map[string]interface{}
		if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &issue); err != nil {
			t.Fatalf("failed to parse create output: %v\n%s", err, out)
		}
		return issue["id"].(string)
	}
	id1 := mkIssue("Batch multi 1")
	id2 := mkIssue("Batch multi 2")

	resetLabelAddFlag(t)
	runBDInProcess(t, tmpDir, "label", "add", id1, id2, "--label", "x", "--label", "y")

	for _, id := range []string{id1, id2} {
		out := runBDInProcess(t, tmpDir, "show", id, "--json")
		var shown []map[string]interface{}
		if err := json.Unmarshal([]byte(out), &shown); err != nil {
			t.Fatalf("failed to parse show output: %v\n%s", err, out)
		}
		labels, _ := shown[0]["labels"].([]interface{})
		if len(labels) != 2 {
			t.Errorf("issue %s: expected 2 labels, got %d: %v", id, len(labels), labels)
		}
	}
}
