//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// bdTag runs `bd tag <id> <label>` and returns combined output, asserting
// success (rc=0). A no-op duplicate tag is still rc=0 (idempotent), so this
// helper is correct for both the real-add and duplicate cases.
func bdTag(t *testing.T, bd, dir, id, label string) string {
	t.Helper()
	cmd := exec.Command(bd, "tag", id, label)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd tag %s %s failed: %v\n%s", id, label, err, out)
	}
	return string(out)
}

// TestEmbeddedLabelNoOpHonest asserts that adding a label that is already
// present (or removing one that is absent) reports an HONEST no-op instead of
// a false "✓ Added"/"✓ Removed" success line (beads-huu7, option C). The
// operation stays idempotent (rc=0, no duplicate) — matching the deliberate
// label_add_duplicate_idempotent contract — but the message and --json status
// must tell the truth so a scripted/agent consumer is not misled into
// believing a mutation occurred.
func TestEmbeddedLabelNoOpHonest(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ln")

	t.Run("tag_duplicate_reports_no_change_not_added", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "tag dup", "--type", "task")
		// First tag really adds.
		out1 := bdTag(t, bd, dir, issue.ID, "dup")
		if !strings.Contains(out1, "Added") {
			t.Errorf("first tag should report Added: %s", out1)
		}
		// Second tag is a no-op — must NOT claim "Added".
		out2 := bdTag(t, bd, dir, issue.ID, "dup")
		if strings.Contains(out2, "Added") {
			t.Errorf("duplicate tag must not falsely claim 'Added': %s", out2)
		}
		if !strings.Contains(strings.ToLower(out2), "already") && !strings.Contains(strings.ToLower(out2), "no change") {
			t.Errorf("duplicate tag should report the no-op honestly: %s", out2)
		}
	})

	t.Run("label_add_duplicate_reports_no_change", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "label add dup", "--type", "task")
		bdLabel(t, bd, dir, "add", issue.ID, "dup")
		out := bdLabel(t, bd, dir, "add", issue.ID, "dup")
		if strings.Contains(out, "Added") {
			t.Errorf("duplicate label add must not falsely claim 'Added': %s", out)
		}
	})

	t.Run("label_add_duplicate_json_status_unchanged", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "label add dup json", "--type", "task")
		bdLabel(t, bd, dir, "add", issue.ID, "dup")
		s := strings.TrimSpace(bdLabelJSONOutput(t, bd, dir, "add", issue.ID, "dup", "--json"))
		start := strings.Index(s, "[")
		if start < 0 {
			t.Fatalf("no JSON array: %s", s)
		}
		var rows []map[string]interface{}
		if err := json.Unmarshal([]byte(s[start:]), &rows); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, s)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d: %s", len(rows), s)
		}
		if rows[0]["status"] != "unchanged" {
			t.Errorf("duplicate add --json status should be 'unchanged', got %v", rows[0]["status"])
		}
	})

	t.Run("label_remove_absent_reports_no_change", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "label rm absent", "--type", "task")
		out := bdLabel(t, bd, dir, "remove", issue.ID, "neverhad")
		if strings.Contains(out, "Removed") {
			t.Errorf("removing an absent label must not falsely claim 'Removed': %s", out)
		}
	})

	t.Run("real_add_still_reports_added", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "real add", "--type", "task")
		out := bdLabel(t, bd, dir, "add", issue.ID, "fresh")
		if !strings.Contains(out, "Added") {
			t.Errorf("a real add should still report Added: %s", out)
		}
	})
}
