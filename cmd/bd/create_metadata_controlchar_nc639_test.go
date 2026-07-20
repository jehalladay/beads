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

// TestCreateAndImportRejectControlCharMetadata_nc639 is the end-to-end teeth for
// beads-nc639. The metadata column is a Dolt JSON type; a raw control byte
// (e.g. ESC 0x1b) in a metadata string value is accepted on write but re-emits
// unreadable JSON on readback, so a single poisoned row bricks EVERY subsequent
// bd list/show/export repo-wide (data-availability defect / import-DoS via the
// same untrusted-`bd import` vector as beads-i8dsb).
//
// The fix rejects such metadata at the shared create+import write seam
// (PrepareIssueForInsert -> storage.ValidateMetadataReadable). This test drives
// the ACTUAL bd binary to prove (1) create is rejected, (2) import is rejected,
// (3) the repo stays READABLE afterward, and (4) benign unicode metadata still
// round-trips.
func TestCreateAndImportRejectControlCharMetadata_nc639(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd)
	env := bdEnv(dir)

	run := func(args ...string) (string, error) {
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// metadata carrying a raw ESC byte (json.Marshal emits it as the \u001b escape, so the
	// JSON is well-formed but decodes back to a real control byte on ingest).
	escMeta, err := json.Marshal(map[string]any{"k": "x\x1bz"})
	if err != nil {
		t.Fatalf("marshal esc metadata: %v", err)
	}

	// (1) bd create --metadata must reject.
	out, err := run("create", "escbug", "-t", "bug", "-p", "2", "--metadata", string(escMeta))
	if err == nil {
		t.Fatalf("bd create with control-char metadata should fail; output:\n%s", out)
	}
	if !strings.Contains(out, "control character") {
		t.Errorf("create rejection should name the control character; got:\n%s", out)
	}

	// (2) bd import must reject the poisoned row.
	issue := map[string]any{
		"id":         "probe-1",
		"title":      "t",
		"status":     "open",
		"priority":   2,
		"issue_type": "bug",
		"metadata":   map[string]any{"k": "x\x1bz"},
	}
	line, err := json.Marshal(issue)
	if err != nil {
		t.Fatalf("marshal seed issue: %v", err)
	}
	jsonlPath := filepath.Join(beadsDir, "inj.jsonl")
	if err := os.WriteFile(jsonlPath, append(line, '\n'), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	if out, err := run("import", jsonlPath); err == nil {
		t.Fatalf("bd import with control-char metadata should fail; output:\n%s", out)
	}

	// (3) the repo must stay READABLE (the whole point — no bricked JSON column).
	if out, err := run("list"); err != nil {
		t.Fatalf("bd list should still succeed after rejected writes; output:\n%s", out)
	}

	// (4) benign unicode metadata still round-trips.
	benign, err := json.Marshal(map[string]any{"k": "café 🚀 value"})
	if err != nil {
		t.Fatalf("marshal benign metadata: %v", err)
	}
	if out, err := run("create", "okbug", "-t", "bug", "-p", "2", "--metadata", string(benign)); err != nil {
		t.Fatalf("benign metadata create should succeed; output:\n%s", out)
	}
	if out, err := run("list"); err != nil {
		t.Fatalf("bd list should succeed with benign metadata stored; output:\n%s", out)
	}
}
