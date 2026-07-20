//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAssigneeListersSanitize_i8dsb is the sanitize teeth for the remaining
// plain-issue Assignee display sinks (beads-i8dsb, 7n9y sibling axis):
//   - query.go:352/378   (bd query <expr> --long / compact @assignee)
//   - ready.go:450       (bd ready --plain verbose)
//   - search.go:496/513  (bd search <term> --long / compact @assignee)
//   - stale.go:143       (bd stale)
//
// issue.Assignee can carry OSC/CSI escapes from an untrusted import (bd import
// does no control-char validation; normalizeAssignee only trims whitespace).
// Each site previously rendered it raw. Teeth exercise the ACTUAL print path per
// command (subprocess) and assert no raw ESC/BEL reaches stdout while the
// visible username survives. create.go:880/create_form.go:442 are covered by a
// create-time --assignee escape below.
func TestAssigneeListersSanitize_i8dsb(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd)

	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	evilAssignee := "evilA" + csi + osc52 + "userZ"

	// Import a plain issue carrying the hostile assignee (query/ready/search/stale
	// all render imported issues).
	issue := map[string]any{
		"id":         "probe-1",
		"title":      "clean title",
		"assignee":   evilAssignee,
		"status":     "open",
		"priority":   2,
		"issue_type": "task",
		"created_at": "2020-01-01T00:00:00Z", // old so `bd stale` picks it up
		"updated_at": "2020-01-01T00:00:00Z",
	}
	line, err := json.Marshal(issue)
	if err != nil {
		t.Fatalf("marshal seed issue: %v", err)
	}
	jsonlPath := filepath.Join(beadsDir, "inj.jsonl")
	if err := os.WriteFile(jsonlPath, append(line, '\n'), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	if out, err := bdRunWithFlockRetry(t, bd, dir, "import", jsonlPath); err != nil {
		t.Fatalf("bd import failed: %v\n%s", err, out)
	}

	assertNoEscape := func(t *testing.T, label string, out []byte) {
		t.Helper()
		got := string(out)
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("%s leaked a raw ESC (0x1b) — assignee not sanitized:\n%q", label, got)
		}
		if strings.ContainsRune(got, '\x07') {
			t.Errorf("%s leaked a raw BEL (0x07) — assignee not sanitized:\n%q", label, got)
		}
		if !strings.Contains(got, "userZ") {
			t.Errorf("%s dropped expected visible username; output:\n%s", label, got)
		}
	}

	run := func(t *testing.T, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}

	t.Run("query-long", func(t *testing.T) {
		assertNoEscape(t, "bd query --long", run(t, "query", "status=open", "--long"))
	})
	t.Run("ready-plain", func(t *testing.T) {
		assertNoEscape(t, "bd ready --plain", run(t, "ready", "--plain"))
	})
	t.Run("search-long", func(t *testing.T) {
		assertNoEscape(t, "bd search --long", run(t, "search", "clean", "--long"))
	})
	t.Run("stale", func(t *testing.T) {
		assertNoEscape(t, "bd stale", run(t, "stale", "--days", "1"))
	})

	// create.go:880 — the create --dry-run summary "Assignee:" line.
	// normalizeAssignee only trims whitespace, so a --assignee with escapes
	// reaches the print raw. (The Assignee summary line lives in the dry-run
	// preview branch, so drive it with --dry-run.)
	t.Run("create-dryrun-summary", func(t *testing.T) {
		out := run(t, "create", "created via flag", "--type", "task", "--assignee", evilAssignee, "--dry-run")
		assertNoEscape(t, "bd create --dry-run --assignee", out)
	})
}
