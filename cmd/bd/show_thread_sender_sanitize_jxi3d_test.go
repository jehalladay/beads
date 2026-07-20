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

// TestShowThreadSenderTo_Sanitize_jxi3d is the direct-path sanitize teeth for
// the From/To leg of beads-jxi3d (i8dsb identity-sink axis). `bd show <id>
// --thread` (showMessageThread → show_thread.go:116) printed
// "From: <msg.Sender>  To: <msg.Assignee>" RAW — the Subject one line below was
// covered by beads-s3qhv but the From/To identity line was missed. A message's
// Sender/Assignee can originate from an UNTRUSTED import (`bd import`
// json.Unmarshals a JSONL message with no control-char validation), so a
// hostile identity can carry OSC/CSI escapes.
//
// End-to-end teeth exercising the ACTUAL print path (subprocess, not a re-call
// of the sanitizer — a helper re-call would false-green a print-site
// regression): import a message JSONL whose sender AND assignee carry escapes,
// run `bd show <id> --thread`, assert stdout carries NO raw ESC/BEL while the
// visible identity text and the From:/To: framing survive.
func TestShowThreadSenderTo_Sanitize_jxi3d(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd)

	// CSI color + OSC 52 clipboard-write (BEL-terminated). json.Marshal emits
	// these control bytes as valid \uXXXX escapes, so the JSONL line is
	// well-formed JSON that `bd import`'s json.Unmarshal decodes back into real
	// control bytes in the stored Sender / Assignee.
	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	evilSender := "evilFrom" + csi + osc52 + "userA"
	evilTo := "evilTo" + osc52 + "userB"

	msg := map[string]any{
		"id":         "msg-probe",
		"title":      "clean subject",
		"sender":     evilSender,
		"assignee":   evilTo,
		"status":     "open",
		"priority":   2,
		"issue_type": "message",
		"created_at": "2026-07-20T00:00:00Z",
		"updated_at": "2026-07-20T00:00:00Z",
	}
	line, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal seed message: %v", err)
	}
	jsonlPath := filepath.Join(beadsDir, "inj.jsonl")
	if err := os.WriteFile(jsonlPath, append(line, '\n'), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	if out, err := bdRunWithFlockRetry(t, bd, dir, "import", jsonlPath); err != nil {
		t.Fatalf("bd import failed: %v\n%s", err, out)
	}

	env := bdEnv(dir)
	cmd := exec.Command(bd, "show", "msg-probe", "--thread")
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd show --thread failed: %v\n%s", err, out)
	}
	got := string(out)

	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("bd show --thread leaked a raw ESC (0x1b) — From/To not sanitized (beads-jxi3d):\n%q", got)
	}
	if strings.ContainsRune(got, '\x07') {
		t.Errorf("bd show --thread leaked a raw BEL (0x07) — From/To not sanitized (beads-jxi3d):\n%q", got)
	}
	// Visible identity text + From:/To: framing must survive sanitization.
	for _, want := range []string{"From:", "To:", "evilFromuserA", "evilTouserB"} {
		if !strings.Contains(got, want) {
			t.Errorf("bd show --thread dropped expected visible text %q; output:\n%s", want, got)
		}
	}
}
