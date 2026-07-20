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

// TestListAssigneeSanitize_i8dsb is the sanitize teeth for the list-view
// Assignee sink (beads-i8dsb, 7n9y sibling axis). Both `bd list --flat`
// (formatIssueCompact -> " @<assignee>") and `bd list --long` (formatIssueLong
// -> "Assignee: <assignee>") render the assignee, which can carry OSC/CSI
// escapes from an untrusted import. The print sites previously rendered it raw.
// (The default tree/pretty view does not render assignee and returns early; only
// --flat disables the tree view, so both sinks require --flat — plain --long is
// still overridden by the default tree format.)
//
// End-to-end through the real print path (subprocess): import a JSONL issue
// whose assignee carries escapes, run both list views, and assert stdout has NO
// raw ESC/BEL while the visible username survives.
func TestListAssigneeSanitize_i8dsb(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd)

	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	evilAssignee := "evilA" + csi + osc52 + "userZ"

	issue := map[string]any{
		"id":         "probe-1",
		"title":      "clean title",
		"assignee":   evilAssignee,
		"status":     "open",
		"priority":   2,
		"issue_type": "task",
		"created_at": "2026-07-20T00:00:00Z",
		"updated_at": "2026-07-20T00:00:00Z",
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

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"flat", []string{"list", "--flat"}},
		{"flat-long", []string{"list", "--flat", "--long"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bd, tc.args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("bd %s failed: %v\n%s", strings.Join(tc.args, " "), err, out)
			}
			got := string(out)
			if strings.ContainsRune(got, '\x1b') {
				t.Errorf("bd %s leaked a raw ESC (0x1b) — assignee not sanitized:\n%q", strings.Join(tc.args, " "), got)
			}
			if strings.ContainsRune(got, '\x07') {
				t.Errorf("bd %s leaked a raw BEL (0x07) — assignee not sanitized:\n%q", strings.Join(tc.args, " "), got)
			}
			// The visible username must survive.
			for _, want := range []string{"evilA", "userZ"} {
				if !strings.Contains(got, want) {
					t.Errorf("bd %s dropped expected visible text %q; output:\n%s", strings.Join(tc.args, " "), want, got)
				}
			}
		})
	}
}
