//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

// ===== Shared test helpers (used by both update and close tests) =====

// bdUpdate runs "bd update" with the given args and returns stdout.
// Retries on flock contention.
func bdUpdate(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"update"}, args...)
	out, err := bdRunWithFlockRetry(t, bd, dir, fullArgs...)
	if err != nil {
		t.Fatalf("bd update %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// bdUpdateFail runs "bd update" expecting failure.
func bdUpdateFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"update"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd update %s to fail, but it succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

func embeddedCurrentCommit(t *testing.T, beadsDir, database string) string {
	t.Helper()
	store, err := embeddeddolt.Open(t.Context(), beadsDir, database, "main")
	if err != nil {
		t.Fatalf("open embedded store: %v", err)
	}
	defer func() { _ = store.Close() }()

	head, err := store.GetCurrentCommit(t.Context())
	if err != nil {
		t.Fatalf("GetCurrentCommit: %v", err)
	}
	if head == "" {
		t.Fatal("GetCurrentCommit returned empty hash")
	}
	return head
}

// bdShowJSON runs "bd show <id> --json" and returns the raw JSON output.
func bdShowJSON(t *testing.T, bd, dir, id string) string {
	t.Helper()
	cmd := exec.Command(bd, "show", id, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd show %s --json failed: %v\nstdout:\n%s\nstderr:\n%s", id, err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// hasLabel checks if a label is present in the issue's labels.
func hasLabel(issue *types.Issue, label string) bool {
	for _, l := range issue.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// parseShowJSON parses the first JSON object from bd show --json output,
// which may be wrapped in an array or have non-JSON lines before it.
func parseShowJSON(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	start := strings.Index(raw, "{")
	if start < 0 {
		t.Fatalf("no JSON object in output: %s", raw)
	}
	dec := json.NewDecoder(strings.NewReader(raw[start:]))
	var obj json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("parse JSON object: %v\nraw: %s", err, raw[start:])
	}
	return obj
}

// showLabels returns labels from bd show --json output (uses IssueDetails which includes labels).
func showLabels(t *testing.T, bd, dir, id string) []string {
	t.Helper()
	raw := bdShowJSON(t, bd, dir, id)
	obj := parseShowJSON(t, raw)
	var details struct {
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal(obj, &details); err != nil {
		t.Fatalf("parse labels: %v", err)
	}
	return details.Labels
}

// showDeps returns dependency IDs from bd show --json output.
func showDeps(t *testing.T, bd, dir, id string) []struct {
	ID   string `json:"id"`
	Type string `json:"dependency_type"`
} {
	t.Helper()
	raw := bdShowJSON(t, bd, dir, id)
	obj := parseShowJSON(t, raw)
	var details struct {
		Dependencies []struct {
			ID   string `json:"id"`
			Type string `json:"dependency_type"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(obj, &details); err != nil {
		t.Fatalf("parse deps: %v", err)
	}
	return details.Dependencies
}

// ===== Update tests =====

func TestEmbeddedUpdateBatchAutoCommitDoesNotAdvanceHead(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt update tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "ub")
	issue := bdCreate(t, bd, dir, "Batch update")
	before := embeddedCurrentCommit(t, beadsDir, "ub")

	cmd := exec.Command(bd, "--dolt-auto-commit", "batch", "update", issue.ID, "--title", "Deferred batch update")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd --dolt-auto-commit batch update failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	after := embeddedCurrentCommit(t, beadsDir, "ub")
	if after != before {
		t.Fatalf("batch-mode update advanced HEAD; before=%s after=%s", before, after)
	}
}

// TestEmbeddedUpdateBatchMetadataJSONContract proves the --json per-item error
// contract for the metadata merge/edit paths in `bd update <A> <B> ... --json`.
//
// When a batch mixes a good issue (A) with one whose metadata edit fails (B has
// non-object metadata, so applyMetadataEdits/mergeMetadata errors), the failure
// is a PER-ITEM error: it must go to stderr via reportItemError and the batch
// must continue (skip B, keep A). It must NOT `return HandleErrorRespectJSON`
// mid-loop — that writes a per-item error OBJECT to stdout, poisoning the pure
// JSON success payload that downstream tooling parses, and abandons the rest of
// the batch (including A's already-computed success output).
//
// The observable contract: with --json, stdout stays a valid JSON ARRAY of the
// successfully-updated issues (A present, B absent); the per-item failure is on
// stderr, never on stdout. Regression guard for beads-pqma.

// seedNonObjectMetadata writes a non-object (scalar/array) JSON metadata value
// directly to an issue via a raw working-set UPDATE, bypassing the ef2k CLI
// input gate that rejects non-object --metadata. rawJSON must be valid JSON
// (e.g. "42", `[1,2,3]`). The write lands in the same on-disk embedded working
// set the next `bd` subprocess reads, so a subsequent metadata edit sees the
// non-object value and errors as it would on an imported/legacy row (beads-d478).
func seedNonObjectMetadata(t *testing.T, beadsDir, id, rawJSON string) {
	t.Helper()
	dataDir := filepath.Join(beadsDir, "embeddeddolt")
	cfg, err := configfile.Load(beadsDir)
	if err != nil {
		t.Fatalf("load config for metadata seed: %v", err)
	}
	db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), dataDir, cfg.GetDoltDatabase(), "main")
	if err != nil {
		t.Fatalf("OpenSQL for metadata seed: %v", err)
	}
	defer func() {
		if cerr := cleanup(); cerr != nil {
			t.Errorf("cleanup seed db: %v", cerr)
		}
	}()
	if _, err := db.ExecContext(t.Context(),
		"UPDATE issues SET metadata = CAST(? AS JSON) WHERE id = ?", rawJSON, id); err != nil {
		t.Fatalf("seed non-object metadata on %s: %v", id, err)
	}
}

// TestEmbeddedUpdateAllFailedJSONSingleObject proves beads-92tz for update:
// `bd update <nonexistent> --status open --json` must emit EXACTLY ONE JSON
// error object across both streams — on stdout, stderr clean — so a 2>&1
// consumer can json.load it. Previously the per-item reportItemError put a JSON
// object on stderr AND the terminal put one on stdout = two objects.
func TestEmbeddedUpdateAllFailedJSONSingleObject(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt update tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ua")

	cmd := exec.Command(bd, "update", "ua-nope-aaa", "--status", "open", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Errorf("expected non-zero exit for all-nonexistent update, got success\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	out := strings.TrimSpace(stdout.String())
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a single JSON object: %v\nstdout:\n%s", jerr, out)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field on stdout, got: %s", out)
	}
	errStr := strings.TrimSpace(stderr.String())
	if errStr != "" && json.Valid([]byte(errStr)) {
		t.Errorf("stderr must be clean of a competing JSON error object on all-failed --json (beads-92tz); got:\n%s", errStr)
	}
}

func TestEmbeddedUpdateBatchMetadataJSONContract(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt update tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "bm")

	a := bdCreate(t, bd, dir, "Issue A")
	b := bdCreate(t, bd, dir, "Issue B")

	// Give B non-object (but valid-JSON) metadata so the batch metadata edit
	// fails on B: "existing metadata is not a JSON object". We seed it via a
	// raw working-set UPDATE rather than `bd update --metadata 42`, because the
	// ef2k input gate now rejects a non-object --metadata at the CLI ("must be
	// a JSON object"). A non-object metadata row is still reachable in
	// production via bd import's verbatim upsert and pre-ef2k legacy rows, so
	// seeding it directly at the storage layer both reproduces the real state
	// and keeps this test decoupled from the od9b reject-vs-warn import fork
	// (beads-d478).
	seedNonObjectMetadata(t, beadsDir, b.ID, "42")

	// Batch --set-metadata over [A, B] under --json: A succeeds, B fails. The
	// failure must land on stderr and stdout must remain a valid JSON array
	// containing A (and not B).
	cmd := exec.Command(bd, "update", a.ID, b.ID, "--set-metadata", "team=platform", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, _ := runCommandBuffers(t, cmd)

	out := strings.TrimSpace(stdout.String())
	// stdout MUST be a JSON array (the success payload), not a JSON error
	// object — a mid-loop return emits `{"error": ...}` on stdout instead.
	var updated []map[string]any
	if err := json.Unmarshal([]byte(out), &updated); err != nil {
		t.Fatalf("stdout is not a JSON array of updated issues (metadata failure poisoned stdout): %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}

	gotIDs := map[string]bool{}
	for _, iss := range updated {
		if id, ok := iss["id"].(string); ok {
			gotIDs[id] = true
		}
	}
	if !gotIDs[a.ID] {
		t.Errorf("issue A (%s) missing from --json success payload — B's metadata failure aborted the batch and dropped A's output\nstdout:\n%s\nstderr:\n%s",
			a.ID, stdout.String(), stderr.String())
	}
	if gotIDs[b.ID] {
		t.Errorf("issue B (%s) appears in the success payload but its metadata edit should have failed\nstdout:\n%s\nstderr:\n%s",
			b.ID, stdout.String(), stderr.String())
	}
	// The per-item failure must be reported on stderr, never stdout.
	if !strings.Contains(stderr.String(), b.ID) && !strings.Contains(stderr.String(), "metadata") {
		t.Errorf("expected B's metadata failure on stderr; got none\nstderr:\n%s", stderr.String())
	}
}

func TestEmbeddedUpdateRoutedStoreCommitsTargetHead(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt update tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "src")

	targetDir := filepath.Join(dir, "target-repo")
	if err := os.MkdirAll(targetDir, 0750); err != nil {
		t.Fatal(err)
	}
	initGitRepoAt(t, targetDir)
	runBDInit(t, bd, targetDir, "--prefix", "tgt")

	issue := bdCreate(t, bd, targetDir, "Routed target issue")
	route := `{"prefix":"tgt-","path":"target-repo"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".beads", "routes.jsonl"), []byte(route), 0644); err != nil {
		t.Fatalf("write routes.jsonl: %v", err)
	}

	targetBeadsDir := filepath.Join(targetDir, ".beads")
	before := embeddedCurrentCommit(t, targetBeadsDir, "tgt")
	bdUpdate(t, bd, dir, issue.ID, "--title", "Updated through route")
	after := embeddedCurrentCommit(t, targetBeadsDir, "tgt")
	if after == before {
		t.Fatalf("routed update did not advance target HEAD; before=%s after=%s", before, after)
	}

	targetStore := openStore(t, targetBeadsDir, "tgt")
	got, err := targetStore.GetIssue(t.Context(), issue.ID)
	if err != nil {
		t.Fatalf("GetIssue in target: %v", err)
	}
	if got.Title != "Updated through route" {
		t.Fatalf("target title = %q, want routed update title", got.Title)
	}
}

func TestEmbeddedUpdate(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "tu")

	// ===== Field Update Flags =====

	t.Run("update_status", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Status test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "in_progress")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusInProgress {
			t.Errorf("expected status in_progress, got %s", got.Status)
		}
	})

	// beads-gqvu: `bd update --status` must accept case-variant built-in
	// statuses (write sibling of beads-7wrj, which case-folds the read/filter
	// path). Before the fix "IN_PROGRESS" hard-errored "invalid status".
	t.Run("update_status_case_folded", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Status case-fold test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "IN_PROGRESS")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusInProgress {
			t.Errorf("--status IN_PROGRESS should case-fold to in_progress, got %s", got.Status)
		}
	})

	t.Run("update_title", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Old title", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--title", "New title")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Title != "New title" {
			t.Errorf("expected title 'New title', got %q", got.Title)
		}
	})

	t.Run("update_assignee", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Assign test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--assignee", "alice")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Assignee != "alice" {
			t.Errorf("expected assignee alice, got %q", got.Assignee)
		}
	})

	t.Run("update_priority", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Priority test", "--type", "task", "--priority", "3")
		bdUpdate(t, bd, dir, issue.ID, "--priority", "0")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Priority != 0 {
			t.Errorf("expected priority 0, got %d", got.Priority)
		}
	})

	t.Run("update_description", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Desc test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--description", "Updated description")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Description != "Updated description" {
			t.Errorf("expected description 'Updated description', got %q", got.Description)
		}
	})

	t.Run("update_type", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Type test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--type", "bug")
		got := bdShow(t, bd, dir, issue.ID)
		if got.IssueType != "bug" {
			t.Errorf("expected type bug, got %s", got.IssueType)
		}
	})

	t.Run("update_type_custom", func(t *testing.T) {
		// Register "agent" as a custom type via bd config (GH#3030).
		// This writes to Dolt only, NOT to .beads/config.yaml.
		cfgCmd := exec.Command(bd, "config", "set", "types.custom", "agent,spike")
		cfgCmd.Dir = dir
		cfgCmd.Env = bdEnv(dir)
		if out, err := cfgCmd.CombinedOutput(); err != nil {
			t.Fatalf("bd config set types.custom failed: %v\n%s", err, out)
		}

		issue := bdCreate(t, bd, dir, "Custom type update", "--type", "task")
		// Before the fix (GH#3030), this would fail with "invalid issue type"
		// because the CLI-level validation could not read custom types from Dolt.
		bdUpdate(t, bd, dir, issue.ID, "--type", "agent")
		got := bdShow(t, bd, dir, issue.ID)
		if string(got.IssueType) != "agent" {
			t.Errorf("expected type agent, got %s", got.IssueType)
		}
	})

	t.Run("update_type_invalid_rejected", func(t *testing.T) {
		// Verify that truly invalid types are still rejected by the storage layer.
		issue := bdCreate(t, bd, dir, "Invalid type test", "--type", "task")
		out := bdUpdateFail(t, bd, dir, issue.ID, "--type", "banana")
		if !strings.Contains(out, "invalid issue type") {
			t.Errorf("expected 'invalid issue type' error, got: %s", out)
		}
	})

	t.Run("update_design", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Design test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--design", "Design notes here")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Design != "Design notes here" {
			t.Errorf("expected design 'Design notes here', got %q", got.Design)
		}
	})

	t.Run("update_notes", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Notes test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--notes", "Some notes")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Notes != "Some notes" {
			t.Errorf("expected notes 'Some notes', got %q", got.Notes)
		}
	})

	t.Run("update_append_notes", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Append notes test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--notes", "first")
		bdUpdate(t, bd, dir, issue.ID, "--append-notes", "more")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Notes != "first\nmore" {
			t.Errorf("expected notes 'first\\nmore', got %q", got.Notes)
		}
	})

	t.Run("update_notes_and_append_conflict", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Notes conflict", "--type", "task")
		out := bdUpdateFail(t, bd, dir, issue.ID, "--notes", "x", "--append-notes", "y")
		if !strings.Contains(out, "cannot specify both") {
			t.Errorf("expected conflict error, got: %s", out)
		}
	})

	t.Run("update_acceptance", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "AC test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--acceptance", "AC text")
		got := bdShow(t, bd, dir, issue.ID)
		if got.AcceptanceCriteria != "AC text" {
			t.Errorf("expected acceptance_criteria 'AC text', got %q", got.AcceptanceCriteria)
		}
	})

	t.Run("update_external_ref", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "ExtRef test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--external-ref", "gh-42")
		got := bdShow(t, bd, dir, issue.ID)
		if got.ExternalRef == nil || *got.ExternalRef != "gh-42" {
			t.Errorf("expected external_ref 'gh-42', got %v", got.ExternalRef)
		}
	})

	// GH#3902: --external-ref "" must clear to SQL NULL (matching buildCreateIssue's
	// pointer semantics), not write an empty string. Otherwise sync/tracker code
	// that checks ExternalRef == nil silently misclassifies cleared refs as still
	// tracked, and two cleared issues round-trip with different JSON shapes
	// (cleared via CLI emits "external_ref":"" while never-set issues omit the field).
	t.Run("update_external_ref_clear", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "ExtRef clear A", "--type", "task", "--external-ref", "ref-a")
		b := bdCreate(t, bd, dir, "ExtRef clear B", "--type", "task", "--external-ref", "ref-b")

		bdUpdate(t, bd, dir, a.ID, "--external-ref", "")
		// Repeat clear must succeed for a second issue — historical UNIQUE
		// constraint repro from the issue report.
		bdUpdate(t, bd, dir, b.ID, "--external-ref", "")

		gotA := bdShow(t, bd, dir, a.ID)
		gotB := bdShow(t, bd, dir, b.ID)
		if gotA.ExternalRef != nil {
			t.Errorf("expected A.external_ref to be nil after clear, got %q", *gotA.ExternalRef)
		}
		if gotB.ExternalRef != nil {
			t.Errorf("expected B.external_ref to be nil after clear, got %q", *gotB.ExternalRef)
		}

		// JSON output: cleared ref should be omitted via omitempty, not emitted as "".
		rawA := bdShowJSON(t, bd, dir, a.ID)
		if strings.Contains(rawA, `"external_ref"`) {
			t.Errorf("expected external_ref field to be omitted from JSON after clear, got: %s", rawA)
		}
	})

	t.Run("update_spec_id", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "SpecID test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--spec-id", "RFC-007")
		got := bdShow(t, bd, dir, issue.ID)
		if got.SpecID != "RFC-007" {
			t.Errorf("expected spec_id 'RFC-007', got %q", got.SpecID)
		}
	})

	t.Run("update_estimate", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Estimate test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--estimate", "60")
		got := bdShow(t, bd, dir, issue.ID)
		if got.EstimatedMinutes == nil || *got.EstimatedMinutes != 60 {
			t.Errorf("expected estimated_minutes 60, got %v", got.EstimatedMinutes)
		}
	})

	t.Run("update_due", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Due test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--due", "2099-01-15")
		got := bdShow(t, bd, dir, issue.ID)
		if got.DueAt == nil {
			t.Error("expected due_at to be set")
		}
	})

	t.Run("update_due_clear", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Due clear test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--due", "2099-01-15")
		bdUpdate(t, bd, dir, issue.ID, "--due", "")
		got := bdShow(t, bd, dir, issue.ID)
		if got.DueAt != nil {
			t.Error("expected due_at to be cleared")
		}
	})

	t.Run("update_defer", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Defer test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--defer", "2099-01-15")
		got := bdShow(t, bd, dir, issue.ID)
		if got.DeferUntil == nil {
			t.Error("expected defer_until to be set")
		}
		// GH#3233: --defer should also set status=deferred for consistency with `bd defer`
		if string(got.Status) != "deferred" {
			t.Errorf("expected status=deferred, got %q", got.Status)
		}
	})

	t.Run("update_defer_respects_explicit_status", func(t *testing.T) {
		// GH#3233: explicit --status should win over the implicit deferred set by --defer
		issue := bdCreate(t, bd, dir, "Defer+status test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--defer", "2099-01-15", "--status", "in_progress")
		got := bdShow(t, bd, dir, issue.ID)
		if string(got.Status) != "in_progress" {
			t.Errorf("expected explicit status=in_progress to win, got %q", got.Status)
		}
	})

	t.Run("update_defer_clear", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Defer clear test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--defer", "2099-01-15")
		bdUpdate(t, bd, dir, issue.ID, "--defer", "")
		got := bdShow(t, bd, dir, issue.ID)
		if got.DeferUntil != nil {
			t.Error("expected defer_until to be cleared")
		}
		// GH#3233: clearing defer on a deferred issue must restore ready visibility
		if string(got.Status) != "open" {
			t.Errorf("expected status=open after clearing defer, got %q", got.Status)
		}
	})

	t.Run("update_defer_past_date_keeps_status_open", func(t *testing.T) {
		// GH#3233: past-date --defer shouldn't flip status to deferred, because
		// the warning promises the issue "will appear in bd ready immediately".
		issue := bdCreate(t, bd, dir, "Past defer test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--defer", "2000-01-01")
		got := bdShow(t, bd, dir, issue.ID)
		if string(got.Status) == "deferred" {
			t.Errorf("past --defer should not set status=deferred, got %q", got.Status)
		}
	})

	t.Run("update_defer_clear_preserves_non_deferred_status", func(t *testing.T) {
		// GH#3233: clearing defer_until shouldn't clobber a non-deferred status
		// that was set independently (e.g. in_progress).
		issue := bdCreate(t, bd, dir, "Defer clear keep status test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "in_progress")
		bdUpdate(t, bd, dir, issue.ID, "--defer", "")
		got := bdShow(t, bd, dir, issue.ID)
		if string(got.Status) != "in_progress" {
			t.Errorf("expected status=in_progress to be preserved, got %q", got.Status)
		}
	})

	t.Run("update_await_id", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Await test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--await-id", "run-123")
		raw := bdShowJSON(t, bd, dir, issue.ID)
		if !strings.Contains(raw, `"await_id":"run-123"`) && !strings.Contains(raw, `"await_id": "run-123"`) {
			t.Errorf("expected await_id 'run-123' in JSON output, got: %s", raw)
		}
	})

	t.Run("update_multiple_fields", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Multi update", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "in_progress", "--assignee", "bob", "--priority", "1")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusInProgress {
			t.Errorf("expected status in_progress, got %s", got.Status)
		}
		if got.Assignee != "bob" {
			t.Errorf("expected assignee bob, got %q", got.Assignee)
		}
		if got.Priority != 1 {
			t.Errorf("expected priority 1, got %d", got.Priority)
		}
	})

	t.Run("close_via_status_update", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Close via update", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "closed")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusClosed {
			t.Errorf("expected status closed, got %s", got.Status)
		}
		if got.ClosedAt == nil {
			t.Error("expected closed_at to be set")
		}
	})

	t.Run("reopen_via_status_update", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Reopen test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "closed")
		bdUpdate(t, bd, dir, issue.ID, "--status", "open")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusOpen {
			t.Errorf("expected status open, got %s", got.Status)
		}
		if got.ClosedAt != nil {
			t.Error("expected closed_at to be cleared on reopen")
		}
	})

	// ===== Label Flags =====

	t.Run("update_add_label", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Label add test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--add-label", "bug")
		labels := showLabels(t, bd, dir, issue.ID)
		found := false
		for _, l := range labels {
			if l == "bug" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected label 'bug', got %v", labels)
		}
	})

	t.Run("update_add_multiple_labels", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Multi label test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--add-label", "a,b")
		labels := showLabels(t, bd, dir, issue.ID)
		hasA, hasB := false, false
		for _, l := range labels {
			if l == "a" {
				hasA = true
			}
			if l == "b" {
				hasB = true
			}
		}
		if !hasA || !hasB {
			t.Errorf("expected labels [a, b], got %v", labels)
		}
	})

	t.Run("update_remove_label", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Label remove test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--add-label", "bug")
		bdUpdate(t, bd, dir, issue.ID, "--remove-label", "bug")
		labels := showLabels(t, bd, dir, issue.ID)
		for _, l := range labels {
			if l == "bug" {
				t.Errorf("expected label 'bug' to be removed, got %v", labels)
			}
		}
	})

	t.Run("update_set_labels", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Label set test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--add-label", "a,b")
		bdUpdate(t, bd, dir, issue.ID, "--set-labels", "x,y")
		labels := showLabels(t, bd, dir, issue.ID)
		hasX, hasY, hasA := false, false, false
		for _, l := range labels {
			switch l {
			case "x":
				hasX = true
			case "y":
				hasY = true
			case "a":
				hasA = true
			}
		}
		if !hasX || !hasY {
			t.Errorf("expected labels [x, y], got %v", labels)
		}
		if hasA {
			t.Errorf("expected old label 'a' to be replaced, got %v", labels)
		}
	})

	// ===== Metadata Flags =====

	t.Run("update_metadata_json", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Meta test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--metadata", `{"key":"val"}`)
		got := bdShow(t, bd, dir, issue.ID)
		if !strings.Contains(string(got.Metadata), `"key"`) {
			t.Errorf("expected metadata to contain 'key', got %s", got.Metadata)
		}
	})

	t.Run("update_metadata_merge", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Meta merge test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--metadata", `{"a":1}`)
		bdUpdate(t, bd, dir, issue.ID, "--metadata", `{"b":2}`)
		got := bdShow(t, bd, dir, issue.ID)
		meta := string(got.Metadata)
		if !strings.Contains(meta, `"a"`) || !strings.Contains(meta, `"b"`) {
			t.Errorf("expected metadata to contain both a and b, got %s", meta)
		}
	})

	t.Run("update_set_metadata", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Set meta test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--set-metadata", "team=platform")
		got := bdShow(t, bd, dir, issue.ID)
		if !strings.Contains(string(got.Metadata), `"team"`) {
			t.Errorf("expected metadata to contain 'team', got %s", got.Metadata)
		}
	})

	t.Run("update_unset_metadata", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Unset meta test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--set-metadata", "team=platform")
		bdUpdate(t, bd, dir, issue.ID, "--unset-metadata", "team")
		got := bdShow(t, bd, dir, issue.ID)
		if strings.Contains(string(got.Metadata), `"team"`) {
			t.Errorf("expected metadata to NOT contain 'team', got %s", got.Metadata)
		}
	})

	t.Run("update_metadata_and_set_conflict", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Meta conflict", "--type", "task")
		out := bdUpdateFail(t, bd, dir, issue.ID, "--metadata", `{"a":1}`, "--set-metadata", "b=2")
		if !strings.Contains(out, "cannot combine") {
			t.Errorf("expected conflict error, got: %s", out)
		}
	})

	// ===== Claim Flag =====

	t.Run("update_claim", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Claim test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--claim")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Assignee == "" {
			t.Error("expected assignee to be set after claim")
		}
		if got.Status != types.StatusInProgress {
			t.Errorf("expected status in_progress after claim, got %s", got.Status)
		}
	})

	t.Run("update_claim_already_claimed", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Claim fail test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--assignee", "alice")
		out := bdUpdateFail(t, bd, dir, issue.ID, "--claim")
		if !strings.Contains(out, "already claimed") {
			t.Errorf("expected 'already claimed' error, got: %s", out)
		}
	})

	// ===== Parent Reparenting =====

	t.Run("update_parent_set", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "Parent epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "Child issue", "--type", "task")
		bdUpdate(t, bd, dir, child.ID, "--parent", epic.ID)
		deps := showDeps(t, bd, dir, child.ID)
		found := false
		for _, d := range deps {
			if d.ID == epic.ID && d.Type == "parent-child" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected parent-child dep to %s, got %v", epic.ID, deps)
		}
	})

	t.Run("update_parent_change", func(t *testing.T) {
		epic1 := bdCreate(t, bd, dir, "Old parent", "--type", "epic")
		epic2 := bdCreate(t, bd, dir, "New parent", "--type", "epic")
		child := bdCreate(t, bd, dir, "Reparent child", "--type", "task")
		bdUpdate(t, bd, dir, child.ID, "--parent", epic1.ID)
		bdUpdate(t, bd, dir, child.ID, "--parent", epic2.ID)
		deps := showDeps(t, bd, dir, child.ID)
		hasOld, hasNew := false, false
		for _, d := range deps {
			if d.Type == "parent-child" {
				if d.ID == epic1.ID {
					hasOld = true
				}
				if d.ID == epic2.ID {
					hasNew = true
				}
			}
		}
		if hasOld {
			t.Error("expected old parent dep to be removed")
		}
		if !hasNew {
			t.Error("expected new parent dep to exist")
		}
	})

	t.Run("update_parent_remove", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "Remove parent epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "Orphan child", "--type", "task")
		bdUpdate(t, bd, dir, child.ID, "--parent", epic.ID)
		bdUpdate(t, bd, dir, child.ID, "--parent", "")
		deps := showDeps(t, bd, dir, child.ID)
		for _, d := range deps {
			if d.Type == "parent-child" {
				t.Errorf("expected no parent-child dep, got %v", deps)
			}
		}
	})

	// ===== Ephemeral / History Flags =====

	t.Run("update_ephemeral", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Ephemeral test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--ephemeral")
		got := bdShow(t, bd, dir, issue.ID)
		if !got.Ephemeral {
			t.Error("expected ephemeral to be true")
		}
	})

	t.Run("update_persistent", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Persistent test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--ephemeral")
		bdUpdate(t, bd, dir, issue.ID, "--persistent")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Ephemeral {
			t.Error("expected ephemeral to be false after --persistent")
		}
	})

	t.Run("update_no_history", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "NoHistory test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--no-history")
		got := bdShow(t, bd, dir, issue.ID)
		if !got.NoHistory {
			t.Error("expected no_history to be true")
		}
	})

	t.Run("update_history", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "History test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--no-history")
		bdUpdate(t, bd, dir, issue.ID, "--history")
		got := bdShow(t, bd, dir, issue.ID)
		if got.NoHistory {
			t.Error("expected no_history to be false after --history")
		}
	})

	// beads-9ynk: the `pinned` bool (Issue.Pinned — the prune/purge "skips pinned"
	// protection marker, distinct from status="pinned") is accepted by the storage
	// allowed-fields map and round-trips through import/export, but `bd update` had
	// no flag to set it from the CLI, leaving the documented prune/purge protection
	// unreachable. --pinned/--no-pinned must set/clear the bool without forcing the
	// status, and a pinned closed bead must survive prune.
	t.Run("update_pinned", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Pinned test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--pinned")
		got := bdShow(t, bd, dir, issue.ID)
		if !got.Pinned {
			t.Error("expected pinned to be true after --pinned")
		}
		// --pinned is a context marker; it must NOT change the lifecycle status.
		if got.Status != types.StatusOpen {
			t.Errorf("expected status to stay open after --pinned, got %s", got.Status)
		}
	})

	t.Run("update_no_pinned", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "NoPinned test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--pinned")
		bdUpdate(t, bd, dir, issue.ID, "--no-pinned")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Pinned {
			t.Error("expected pinned to be false after --no-pinned")
		}
	})

	t.Run("update_ephemeral_persistent_conflict", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Eph conflict", "--type", "task")
		out := bdUpdateFail(t, bd, dir, issue.ID, "--ephemeral", "--persistent")
		if !strings.Contains(out, "cannot specify both") {
			t.Errorf("expected conflict error, got: %s", out)
		}
	})

	// ===== Session Flag =====

	t.Run("update_status_closed_with_session", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Session test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "closed", "--session", "sess-123")
		got := bdShow(t, bd, dir, issue.ID)
		// Verify the issue is closed (closed_by_session is stored but not
		// included in IssueSelectColumns, so we verify status + closed_at).
		if got.Status != types.StatusClosed {
			t.Errorf("expected status closed, got %s", got.Status)
		}
		if got.ClosedAt == nil {
			t.Error("expected closed_at to be set")
		}
	})

	// ===== Behavioral / Edge Cases =====

	t.Run("update_no_changes", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "No changes test", "--type", "task")
		out := bdUpdate(t, bd, dir, issue.ID)
		if !strings.Contains(out, "No updates specified") {
			t.Errorf("expected 'No updates specified', got: %s", out)
		}
	})

	t.Run("update_invalid_status", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Bad status", "--type", "task")
		bdUpdateFail(t, bd, dir, issue.ID, "--status", "bogus")
	})

	t.Run("update_invalid_priority", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Bad priority", "--type", "task")
		bdUpdateFail(t, bd, dir, issue.ID, "--priority", "-1")
	})

	t.Run("update_invalid_type", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Bad type", "--type", "task")
		bdUpdateFail(t, bd, dir, issue.ID, "--type", "bogus")
	})

	t.Run("update_nonexistent_id", func(t *testing.T) {
		bdUpdateFail(t, bd, dir, "tu-nonexistent999", "--status", "open")
	})

	t.Run("update_json_output", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "JSON test", "--type", "task")
		cmd := exec.Command(bd, "update", issue.ID, "--status", "in_progress", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd update --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		s := stdout.String()
		start := strings.Index(s, "[")
		if start < 0 {
			start = strings.Index(s, "{")
		}
		if start < 0 {
			t.Fatalf("no JSON in output: %s", s)
		}
		if !json.Valid([]byte(s[start:])) {
			t.Errorf("expected valid JSON output, got: %s", s[start:])
		}
	})

	t.Run("update_multiple_ids", func(t *testing.T) {
		issue1 := bdCreate(t, bd, dir, "Multi ID 1", "--type", "task")
		issue2 := bdCreate(t, bd, dir, "Multi ID 2", "--type", "task")
		bdUpdate(t, bd, dir, issue1.ID, issue2.ID, "--status", "in_progress")
		got1 := bdShow(t, bd, dir, issue1.ID)
		got2 := bdShow(t, bd, dir, issue2.ID)
		if got1.Status != types.StatusInProgress {
			t.Errorf("issue1: expected in_progress, got %s", got1.Status)
		}
		if got2.Status != types.StatusInProgress {
			t.Errorf("issue2: expected in_progress, got %s", got2.Status)
		}
	})

	t.Run("update_partial_batch_bad_id_exits_nonzero", func(t *testing.T) {
		// beads-4i20: a multi-id update where one id is bad must exit non-zero
		// (matching bd close/delete) instead of the old rc=0-if-any-succeeded,
		// while still applying the good id (partial-apply is preserved).
		good := bdCreate(t, bd, dir, "Partial batch good", "--type", "task")
		missing := good.ID + "-nope-does-not-exist"
		// bdUpdateFail asserts a non-zero exit code.
		out := bdUpdateFail(t, bd, dir, good.ID, missing, "--priority", "0")
		if !strings.Contains(out, missing) {
			t.Errorf("expected the failed id %q to be reported; got:\n%s", missing, out)
		}
		// The good id must still have been updated (partial-apply preserved).
		got := bdShow(t, bd, dir, good.ID)
		if got.Priority != 0 {
			t.Errorf("good id: expected priority 0 to be applied despite the bad sibling, got P%d", got.Priority)
		}
	})

	t.Run("update_all_ids_good_exits_zero", func(t *testing.T) {
		// Guard the happy path: when every id resolves, the batch still exits 0.
		a := bdCreate(t, bd, dir, "All good A", "--type", "task")
		b := bdCreate(t, bd, dir, "All good B", "--type", "task")
		bdUpdate(t, bd, dir, a.ID, b.ID, "--priority", "1") // bdUpdate fails the test on non-zero exit
		if got := bdShow(t, bd, dir, a.ID); got.Priority != 1 {
			t.Errorf("a: expected priority 1, got P%d", got.Priority)
		}
		if got := bdShow(t, bd, dir, b.ID); got.Priority != 1 {
			t.Errorf("b: expected priority 1, got P%d", got.Priority)
		}
	})

	t.Run("update_append_notes_bad_id_is_atomic", func(t *testing.T) {
		// beads-1d32: --append-notes is NON-IDEMPOTENT — a partial-apply on a
		// mixed valid/invalid batch means the natural retry double-appends.
		// close/delete pre-resolve all ids and bail before any write; update is
		// best-effort-partial (preserved for idempotent flags by beads-4i20),
		// but for --append-notes the batch must be all-or-nothing: a bad sibling
		// id must prevent the good id's note from being written at all, so the
		// user's retry appends exactly once.
		good := bdCreate(t, bd, dir, "Append-notes atomicity", "--type", "task")
		missing := good.ID + "-nope-does-not-exist"

		// First attempt: bad sibling -> must fail AND leave the good id untouched.
		bdUpdateFail(t, bd, dir, good.ID, missing, "--append-notes", "LOG-ENTRY")
		got := bdShow(t, bd, dir, good.ID)
		if strings.Contains(got.Notes, "LOG-ENTRY") {
			t.Fatalf("--append-notes must NOT write the good id when a sibling id is bad "+
				"(non-idempotent partial-apply => double-append on retry); notes=%q", got.Notes)
		}

		// Retry with only the good id: appends exactly once.
		bdUpdate(t, bd, dir, good.ID, "--append-notes", "LOG-ENTRY")
		got = bdShow(t, bd, dir, good.ID)
		if n := strings.Count(got.Notes, "LOG-ENTRY"); n != 1 {
			t.Fatalf("expected exactly one LOG-ENTRY after retry, got %d; notes=%q", n, got.Notes)
		}
	})

	t.Run("update_dolt_commit", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Dolt commit test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "in_progress")

		// Verify a Dolt commit exists by querying dolt_log.
		dataDir := filepath.Join(beadsDir, "embeddeddolt")
		cfg, _ := configfile.Load(beadsDir)
		database := ""
		if cfg != nil {
			database = cfg.GetDoltDatabase()
		}
		db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), dataDir, database, "main")
		if err != nil {
			t.Fatalf("OpenSQL: %v", err)
		}
		defer cleanup()
		var commitCount int
		err = db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM dolt_log").Scan(&commitCount)
		if err != nil {
			t.Fatalf("query dolt_log: %v", err)
		}
		// At minimum: init schema commit + create commit + update commit
		if commitCount < 3 {
			t.Errorf("expected at least 3 dolt commits, got %d", commitCount)
		}
	})

	t.Run("update_description_body_alias", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Body alias test", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--body", "via body flag")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Description != "via body flag" {
			t.Errorf("expected description 'via body flag', got %q", got.Description)
		}
	})

	t.Run("update_description_from_file", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "File desc test", "--type", "task")
		tmpFile := filepath.Join(t.TempDir(), "desc.txt")
		if err := os.WriteFile(tmpFile, []byte("from file"), 0644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		bdUpdate(t, bd, dir, issue.ID, "--body-file", tmpFile)
		got := bdShow(t, bd, dir, issue.ID)
		if got.Description != "from file" {
			t.Errorf("expected description 'from file', got %q", got.Description)
		}
	})
}

// TestEmbeddedUpdateConcurrent exercises create, update, and list operations
// concurrently to verify EmbeddedDoltStore handles concurrent CLI invocations
// without panics, data corruption, or deadlocks.
func TestEmbeddedUpdateConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "cu")

	const (
		numWorkers      = 10
		issuesPerWorker = 5
	)

	type workerResult struct {
		worker     int
		ids        []string
		listCounts []int
		err        error
	}

	results := make([]workerResult, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			r := workerResult{worker: worker}

			for i := 0; i < issuesPerWorker; i++ {
				// Create an issue.
				title := fmt.Sprintf("w%d-issue-%d", worker, i)
				out, err := bdRunWithFlockRetry(t, bd, dir, "create", "--silent", title)
				if err != nil {
					r.err = fmt.Errorf("create %d: %v\n%s", i, err, out)
					results[worker] = r
					return
				}
				id := strings.TrimSpace(string(out))
				if id == "" {
					r.err = fmt.Errorf("create %d: empty ID", i)
					results[worker] = r
					return
				}
				r.ids = append(r.ids, id)

				// Update: change status to in_progress.
				uCmd := exec.Command(bd, "update", id, "--status", "in_progress")
				uCmd.Dir = dir
				uCmd.Env = bdEnv(dir)
				uOut, err := uCmd.CombinedOutput()
				if err != nil {
					r.err = fmt.Errorf("update status %d: %v\n%s", i, err, uOut)
					results[worker] = r
					return
				}

				// Update: set priority and assignee.
				uCmd2 := exec.Command(bd, "update", id, "--priority", fmt.Sprintf("%d", worker%4), "--assignee", fmt.Sprintf("agent-%d", worker))
				uCmd2.Dir = dir
				uCmd2.Env = bdEnv(dir)
				uOut2, err := uCmd2.CombinedOutput()
				if err != nil {
					r.err = fmt.Errorf("update fields %d: %v\n%s", i, err, uOut2)
					results[worker] = r
					return
				}

				// Update: add a label.
				uCmd3 := exec.Command(bd, "update", id, "--add-label", fmt.Sprintf("team-%d", worker%3))
				uCmd3.Dir = dir
				uCmd3.Env = bdEnv(dir)
				uOut3, err := uCmd3.CombinedOutput()
				if err != nil {
					r.err = fmt.Errorf("update label %d: %v\n%s", i, err, uOut3)
					results[worker] = r
					return
				}

				// List to verify consistency (interleaved with writes).
				listCmd := exec.Command(bd, "list", "--json", "--limit", "0")
				listCmd.Dir = dir
				listCmd.Env = bdEnv(dir)
				listStdout, listStderr, err := runCommandBuffers(t, listCmd)
				if err != nil {
					r.err = fmt.Errorf("list after update %d: %v\nstdout:\n%s\nstderr:\n%s", i, err, listStdout.String(), listStderr.String())
					results[worker] = r
					return
				}
				s := listStdout.String()
				start := strings.Index(s, "[")
				if start < 0 {
					r.listCounts = append(r.listCounts, 0)
					continue
				}
				var issues []json.RawMessage
				if jsonErr := json.Unmarshal([]byte(s[start:]), &issues); jsonErr != nil {
					r.err = fmt.Errorf("list parse %d: %v\nstdout:\n%s\nstderr:\n%s", i, jsonErr, s, listStderr.String())
					results[worker] = r
					return
				}
				r.listCounts = append(r.listCounts, len(issues))
			}

			results[worker] = r
		}(w)
	}
	wg.Wait()

	// Check for errors and collect IDs.
	allIDs := make(map[string]bool)
	var successes int
	for _, r := range results {
		if r.err != nil {
			if !strings.Contains(r.err.Error(), "one writer at a time") {
				t.Errorf("worker %d failed: %v", r.worker, r.err)
			}
			continue
		}
		successes++
		for _, id := range r.ids {
			if allIDs[id] {
				t.Errorf("duplicate ID %q from worker %d", id, r.worker)
			}
			allIDs[id] = true
		}
	}

	if successes == 0 {
		t.Fatal("all workers failed — expected at least 1 success")
	}

	expectedIDs := successes * issuesPerWorker
	if len(allIDs) != expectedIDs {
		t.Errorf("expected %d unique IDs from %d successful workers, got %d", expectedIDs, successes, len(allIDs))
	}

	// Verify all successfully created issues exist and were updated correctly.
	store := openStore(t, beadsDir, "cu")
	stats, err := store.GetStatistics(t.Context())
	if err != nil {
		t.Fatalf("GetStatistics: %v", err)
	}
	if stats.TotalIssues < len(allIDs) {
		t.Errorf("expected at least %d issues in DB, got %d", len(allIDs), stats.TotalIssues)
	}

	// Spot-check: every issue should be in_progress with an assignee.
	for id := range allIDs {
		issue, err := store.GetIssue(t.Context(), id)
		if err != nil {
			t.Errorf("GetIssue(%s): %v", id, err)
			continue
		}
		if issue.Status != types.StatusInProgress {
			t.Errorf("issue %s: expected status in_progress, got %s", id, issue.Status)
		}
		if issue.Assignee == "" {
			t.Errorf("issue %s: expected assignee to be set", id)
		}
	}

	// Verify list counts were monotonically non-decreasing per worker.
	for _, r := range results {
		if r.err != nil {
			continue
		}
		for i := 1; i < len(r.listCounts); i++ {
			if r.listCounts[i] < r.listCounts[i-1] {
				t.Errorf("worker %d: list count decreased from %d to %d at step %d",
					r.worker, r.listCounts[i-1], r.listCounts[i], i)
			}
		}
	}

	t.Logf("created and updated %d issues across %d/%d successful workers, %d in DB",
		len(allIDs), successes, numWorkers, stats.TotalIssues)
}

// TestEmbeddedUpdateStatusCloseGuards verifies that `bd update --status closed`
// enforces the same close-time integrity guards that `bd close` does, and can
// override them the same way with --force (beads-zgku). Before the fix, the
// update-path status write reached the terminal `closed` state at the CLI layer
// without ever hitting the blocked-close (close.go:166) or epic-open-children
// (close.go:145) guards, so a blocked or epic-parent issue could be silently
// closed with rc=0 and no --force — a guard-parity bypass with `bd close`.
func TestEmbeddedUpdateStatusCloseGuards(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tg")

	// Blocked issue: `update --status closed` must refuse without --force.
	t.Run("update_closed_blocked_refuses_without_force", func(t *testing.T) {
		blocker := bdCreate(t, bd, dir, "Update blocker guard", "--type", "task")
		blocked := bdCreate(t, bd, dir, "Update blocked guard", "--type", "task")
		bdDepAdd(t, bd, dir, blocked.ID, blocker.ID)

		// This is the beads-zgku regression: pre-fix it exited rc=0 and closed.
		out := bdUpdateFail(t, bd, dir, blocked.ID, "--status", "closed")
		if !strings.Contains(out, "blocked by open issues") {
			t.Errorf("expected blocked-close guard message, got:\n%s", out)
		}
		got := bdShow(t, bd, dir, blocked.ID)
		if got.Status == types.StatusClosed {
			t.Error("expected blocked issue to remain open on `update --status closed` without --force")
		}
	})

	// Blocked issue with --force closes, matching `bd close --force`.
	t.Run("update_closed_blocked_with_force", func(t *testing.T) {
		blocker := bdCreate(t, bd, dir, "Update blocker force", "--type", "task")
		blocked := bdCreate(t, bd, dir, "Update blocked force", "--type", "task")
		bdDepAdd(t, bd, dir, blocked.ID, blocker.ID)

		bdUpdate(t, bd, dir, blocked.ID, "--status", "closed", "--force")
		got := bdShow(t, bd, dir, blocked.ID)
		if got.Status != types.StatusClosed {
			t.Errorf("expected closed with --force, got %s", got.Status)
		}
	})

	// Epic with open children: `update --status closed` must refuse without --force.
	t.Run("update_closed_epic_open_children_refuses", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "Update epic guard", "--type", "epic")
		child := bdCreate(t, bd, dir, "Update epic child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")

		out := bdUpdateFail(t, bd, dir, epic.ID, "--status", "closed")
		if !strings.Contains(out, "open child issue") {
			t.Errorf("expected epic-open-children guard message, got:\n%s", out)
		}
		got := bdShow(t, bd, dir, epic.ID)
		if got.Status == types.StatusClosed {
			t.Error("expected epic with open children to remain open on `update --status closed` without --force")
		}
		// The child must not be orphaned closed either.
		gotChild := bdShow(t, bd, dir, child.ID)
		if gotChild.Status == types.StatusClosed {
			t.Error("child should remain open")
		}
	})

	// Epic with open children + --force closes.
	t.Run("update_closed_epic_open_children_with_force", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "Update epic force", "--type", "epic")
		child := bdCreate(t, bd, dir, "Update epic child force", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")

		bdUpdate(t, bd, dir, epic.ID, "--status", "closed", "--force")
		got := bdShow(t, bd, dir, epic.ID)
		if got.Status != types.StatusClosed {
			t.Errorf("expected epic closed with --force, got %s", got.Status)
		}
	})

	// A non-blocked, non-epic issue closes normally via update (no regression).
	t.Run("update_closed_unblocked_succeeds", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Update plain close", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "closed")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusClosed {
			t.Errorf("expected plain issue closed via update, got %s", got.Status)
		}
	})

	// Setting a NON-closed status on a blocked issue is unaffected by the guard.
	t.Run("update_nonclosed_status_on_blocked_unaffected", func(t *testing.T) {
		blocker := bdCreate(t, bd, dir, "Blocker np", "--type", "task")
		blocked := bdCreate(t, bd, dir, "Blocked np", "--type", "task")
		bdDepAdd(t, bd, dir, blocked.ID, blocker.ID)

		// in_progress is not a close transition — must succeed.
		bdUpdate(t, bd, dir, blocked.ID, "--status", "in_progress")
		got := bdShow(t, bd, dir, blocked.ID)
		if got.Status != types.StatusInProgress {
			t.Errorf("expected in_progress on blocked issue (not a close), got %s", got.Status)
		}
	})
}

// TestEmbeddedUpdateDemoteEpicGuard verifies the beads-2hkd fix: the epic
// open-child close-guard keys on issue_type==epic at close time, so demoting an
// epic via `bd update --type task` used to let a subsequent close succeed with
// children still open (no --force), reaching a closed-epic-with-open-child state
// — the exact inconsistency the guard family (zgku/1d08) exists to prevent, via a
// different mutation path. The fix enforces the same invariant on the demote
// transition: refuse epic->non-epic while open children remain, overridable with
// --force. Sibling of TestEmbeddedUpdateStatusCloseGuards.
func TestEmbeddedUpdateDemoteEpicGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "td")

	// The core regression: demoting an epic with open children must be refused
	// without --force (pre-fix it succeeded rc=0, opening the close-guard bypass).
	t.Run("demote_epic_with_open_children_refuses_without_force", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "Demote epic guard", "--type", "epic")
		child := bdCreate(t, bd, dir, "Demote epic child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")

		out := bdUpdateFail(t, bd, dir, epic.ID, "--type", "task")
		if !strings.Contains(out, "open child issue") {
			t.Errorf("expected demote-epic-open-children guard message, got:\n%s", out)
		}
		// The epic must remain an epic (demote refused + rolled back).
		got := bdShow(t, bd, dir, epic.ID)
		if got.IssueType != types.TypeEpic {
			t.Errorf("expected epic to remain type=epic after refused demote, got %s", got.IssueType)
		}
	})

	// Full exploit chain proof: once the demote is refused, the epic is still an
	// epic, so the close guard still fires — the bypass is closed end-to-end.
	t.Run("exploit_chain_closed_end_to_end", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "Chain epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "Chain child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")

		// Step 1 (demote) is now refused.
		bdUpdateFail(t, bd, dir, epic.ID, "--type", "task")
		// Step 2 (close) — the epic is still an epic, so the close guard fires.
		out := bdUpdateFail(t, bd, dir, epic.ID, "--status", "closed")
		if !strings.Contains(out, "open child issue") {
			t.Errorf("expected close guard to still fire on the un-demoted epic, got:\n%s", out)
		}
		got := bdShow(t, bd, dir, epic.ID)
		if got.Status == types.StatusClosed {
			t.Error("epic must not be closed while it has an open child (close-guard bypass must stay closed)")
		}
		gotChild := bdShow(t, bd, dir, child.ID)
		if gotChild.Status == types.StatusClosed {
			t.Error("child must remain open")
		}
	})

	// --force overrides the demote guard, matching `bd close --force`.
	t.Run("demote_epic_with_open_children_force_succeeds", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "Demote force epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "Demote force child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")

		bdUpdate(t, bd, dir, epic.ID, "--type", "task", "--force")
		got := bdShow(t, bd, dir, epic.ID)
		if got.IssueType != types.TypeTask {
			t.Errorf("expected epic demoted to task with --force, got %s", got.IssueType)
		}
	})

	// An epic with NO open children demotes freely (guard only fires on open kids).
	t.Run("demote_epic_no_open_children_succeeds", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "Empty epic", "--type", "epic")
		bdUpdate(t, bd, dir, epic.ID, "--type", "task")
		got := bdShow(t, bd, dir, epic.ID)
		if got.IssueType != types.TypeTask {
			t.Errorf("expected childless epic demoted to task, got %s", got.IssueType)
		}
	})

	// An epic whose only child is already CLOSED demotes freely.
	t.Run("demote_epic_closed_children_succeeds", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "Closed-child epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "Closed child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID)

		bdUpdate(t, bd, dir, epic.ID, "--type", "task")
		got := bdShow(t, bd, dir, epic.ID)
		if got.IssueType != types.TypeTask {
			t.Errorf("expected epic with only-closed-children demoted to task, got %s", got.IssueType)
		}
	})

	// A non-epic type change (task -> bug) is never guarded.
	t.Run("non_epic_type_change_unaffected", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Plain type change", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--type", "bug")
		got := bdShow(t, bd, dir, issue.ID)
		if got.IssueType != types.TypeBug {
			t.Errorf("expected task->bug type change, got %s", got.IssueType)
		}
	})

	// Promoting a non-epic to epic is never guarded (only epic->non-epic demote is).
	t.Run("promote_to_epic_unaffected", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Promote me", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--type", "epic")
		got := bdShow(t, bd, dir, issue.ID)
		if got.IssueType != types.TypeEpic {
			t.Errorf("expected task promoted to epic, got %s", got.IssueType)
		}
	})
}
