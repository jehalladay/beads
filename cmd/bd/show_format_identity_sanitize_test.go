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

// TestShowIdentityFieldsSanitize_i8dsb is the sanitize teeth for the
// Assignee/Owner/ExternalRef terminal-escape sink (beads-i8dsb — the 7n9y
// sink-class sibling axis; 7n9y itself is scoped to .Title/.Description only).
//
// `bd show` renders the meta line "Owner: <createdBy> · Assignee: <assignee>"
// and "External: <externalRef>". Those identity/reference strings can originate
// from an UNTRUSTED import: `bd import` json.Unmarshals a JSONL issue with no
// control-char validation and normalizeAssignee only trims whitespace, so a
// hostile assignee can carry OSC/CSI escapes (OSC 52 clipboard-write, OSC 0
// window-title). The print sites previously rendered the raw field, injecting
// control sequences into the operator's terminal.
//
// End-to-end teeth exercising the ACTUAL print path (subprocess, not a re-call
// of the sanitizer — a helper re-call would false-green a print-site
// regression): import a JSONL issue whose assignee AND external_ref carry
// escapes, run `bd show`, and assert stdout carries NO raw ESC/BEL while the
// visible text and the "Assignee:"/"External:" framing survive.
func TestShowIdentityFieldsSanitize_i8dsb(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd)

	// CSI color + OSC 52 clipboard-write (BEL-terminated). json.Marshal emits
	// these control bytes as valid \uXXXX escapes, so the JSONL line is
	// well-formed JSON that `bd import`'s json.Unmarshal decodes back into real
	// control bytes in the stored Assignee / ExternalRef.
	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	evilAssignee := "evilA" + csi + osc52 + "userZ"
	evilRef := "external:proj" + osc52 + "cap"

	issue := map[string]any{
		"id":           "probe-1",
		"title":        "clean title",
		"assignee":     evilAssignee,
		"external_ref": evilRef,
		"status":       "open",
		"priority":     2,
		"issue_type":   "task",
		"created_at":   "2026-07-20T00:00:00Z",
		"updated_at":   "2026-07-20T00:00:00Z",
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

	env := bdEnv(dir)
	cmd := exec.Command(bd, "show", "probe-1")
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd show failed: %v\n%s", err, out)
	}
	got := string(out)

	// No raw terminal-control bytes may reach stdout.
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("bd show leaked a raw ESC (0x1b) — Assignee/ExternalRef not sanitized:\n%q", got)
	}
	if strings.ContainsRune(got, '\x07') {
		t.Errorf("bd show leaked a raw BEL (0x07) — Assignee/ExternalRef not sanitized:\n%q", got)
	}

	// The visible content + framing must survive sanitization (we strip escapes,
	// not the whole field).
	for _, want := range []string{"Assignee:", "evilA", "userZ", "External:", "external:proj", "cap"} {
		if !strings.Contains(got, want) {
			t.Errorf("bd show dropped expected visible text %q; output:\n%s", want, got)
		}
	}
}
