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

// TestShowEventSourceFieldsSanitize_k86xm is the sanitize teeth for the
// event/source/federation terminal-escape sink (beads-k86xm — an i8dsb sibling
// axis: i8dsb covered the identity/reference fields Assignee/Owner/ExternalRef/
// CloseReason/SpecID/comment.Author, and 7n9y covers only .Title/.Description).
//
// `bd show --long` renders the EXTENDED DETAILS section
// ("Closed by session:", "Source system:", "Sender:") and the EVENT section
// ("Kind:", "Actor:", "Target:", "Payload:"). Those event/source/federation
// strings are set VERBATIM from an untrusted import: insertIssueRow persists
// source_system/sender/event_kind/actor/target/payload/closed_by_session as-is,
// `bd import` json.Unmarshals the full types.Issue with no control-char
// validation, so a hostile JSONL line can carry OSC/CSI escapes (OSC 52
// clipboard-write, OSC 0 window-title). The print sites previously rendered the
// raw field, injecting control sequences into the operator's terminal.
//
// End-to-end teeth exercising the ACTUAL print path (subprocess, not a re-call
// of the sanitizer — a helper re-call would false-green a print-site
// regression, per the beads-7n9y-gate-check-reason lesson): import a JSONL
// issue whose event/source fields carry escapes, run `bd show --long`, and
// assert stdout carries NO raw ESC/BEL while the visible text and section
// framing survive.
func TestShowEventSourceFieldsSanitize_k86xm(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd)

	// CSI color + OSC 52 clipboard-write (BEL-terminated) + OSC 0 window-title.
	// json.Marshal emits these control bytes as valid \uXXXX escapes, so the
	// JSONL line is well-formed JSON that `bd import`'s json.Unmarshal decodes
	// back into real control bytes in the stored fields.
	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	const osc0 = "\x1b]0;pwned\x07"

	issue := map[string]any{
		"id":                "probe-evt",
		"title":             "clean title",
		"status":            "open",
		"priority":          2,
		"issue_type":        "task",
		"source_system":     "sysA" + osc52 + "sysZ",
		"sender":            "sendA" + osc0 + "sendZ",
		"closed_by_session": "sessA" + osc52 + "sessZ",
		"event_kind":        "kindA" + osc0 + "kindZ",
		"actor":             "actA" + osc52 + "actZ",
		"target":            "tgtA" + osc0 + "tgtZ",
		"payload":           "payA" + csi + osc52 + "payZ",
		"created_at":        "2026-07-20T00:00:00Z",
		"updated_at":        "2026-07-20T00:00:00Z",
	}
	line, err := json.Marshal(issue)
	if err != nil {
		t.Fatalf("marshal seed issue: %v", err)
	}
	jsonlPath := filepath.Join(beadsDir, "inj-evt.jsonl")
	if err := os.WriteFile(jsonlPath, append(line, '\n'), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	if out, err := bdRunWithFlockRetry(t, bd, dir, "import", jsonlPath); err != nil {
		t.Fatalf("bd import failed: %v\n%s", err, out)
	}

	env := bdEnv(dir)
	cmd := exec.Command(bd, "show", "probe-evt", "--long")
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd show --long failed: %v\n%s", err, out)
	}
	got := string(out)

	// No raw terminal-control bytes may reach stdout.
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("bd show --long leaked a raw ESC (0x1b) — event/source fields not sanitized:\n%q", got)
	}
	if strings.ContainsRune(got, '\x07') {
		t.Errorf("bd show --long leaked a raw BEL (0x07) — event/source fields not sanitized:\n%q", got)
	}

	// The visible content + framing must survive sanitization (escapes stripped,
	// visible chars kept). Endpoints of each field prove the value wasn't dropped.
	for _, want := range []string{
		"Source system:", "sysA", "sysZ",
		"Sender:", "sendA", "sendZ",
		"Kind:", "kindA", "kindZ",
		"Actor:", "actA", "actZ",
		"Target:", "tgtA", "tgtZ",
		"Payload:", "payA", "payZ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bd show --long dropped expected visible text %q; output:\n%s", want, got)
		}
	}
}
