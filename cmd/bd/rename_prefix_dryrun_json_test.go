//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestRenamePrefixDryRunJSONContract_rvmpg is the beads-rvmpg regression, a
// follow-on to beads-qpiw. Three reachable --json paths in
// cmd/bd/rename_prefix.go emitted human plaintext and returned nil WITHOUT any
// JSON object, so a --json consumer got unparseable stdout instead of a result:
//   - dry-run rename (`bd rename-prefix X --dry-run --json`) → "DRY RUN: Would
//     rename ..." text, no JSON
//   - no-issues rename (empty DB) → "No issues to rename..." text, no JSON
//
// The fix suppresses the human prints under --json and emits a structured
// result (with dry_run + planned_renames on the dry-run path). Each subtest
// runs the real binary in embedded mode and asserts stdout is a single clean
// JSON object.
func TestRenamePrefixDryRunJSONContract_rvmpg(t *testing.T) {
	bd := buildEmbeddedBD(t)

	t.Run("dry_run_stdout_is_pure_json", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rva")
		bdCreate(t, bd, dir, "rvmpg seed one", "--type", "task")
		bdCreate(t, bd, dir, "rvmpg seed two", "--type", "task")

		cmd := exec.Command(bd, "rename-prefix", "rvb", "--dry-run", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("`bd rename-prefix rvb --dry-run --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		out := strings.TrimSpace(stdout.String())
		// Must be a single clean JSON object — no leading "DRY RUN:" text.
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("stdout is not pure JSON on `bd rename-prefix --dry-run --json` (human DRY RUN text leaked?): %v\nstdout:\n%s", jerr, out)
		}
		if obj["dry_run"] != true {
			t.Errorf("expected dry_run=true in JSON, got: %s", out)
		}
		if obj["new_prefix"] != "rvb" {
			t.Errorf("expected new_prefix=rvb in JSON, got: %s", out)
		}
		// planned_renames must be present and cover both seeded issues.
		planned, ok := obj["planned_renames"].([]interface{})
		if !ok {
			t.Fatalf("expected planned_renames array in dry-run JSON, got: %s", out)
		}
		if len(planned) != 2 {
			t.Errorf("expected 2 planned_renames, got %d: %s", len(planned), out)
		}
	})

	t.Run("no_issues_stdout_is_pure_json", func(t *testing.T) {
		// Fresh DB with zero issues → the len(issues)==0 path.
		dir, _, _ := bdInit(t, bd, "--prefix", "rvc")

		cmd := exec.Command(bd, "rename-prefix", "rvd", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("`bd rename-prefix rvd --json` (no issues) failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		out := strings.TrimSpace(stdout.String())
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("stdout is not pure JSON on `bd rename-prefix --json` with no issues (human text leaked?): %v\nstdout:\n%s", jerr, out)
		}
		if obj["new_prefix"] != "rvd" {
			t.Errorf("expected new_prefix=rvd in JSON, got: %s", out)
		}
		if obj["issues_count"] != float64(0) {
			t.Errorf("expected issues_count=0 in JSON, got: %s", out)
		}
	})
}
