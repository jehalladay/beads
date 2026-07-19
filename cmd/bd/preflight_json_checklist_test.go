package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// beads-kvmg: `bd preflight --json` WITHOUT --check silently ignored the flag
// and printed the plaintext checklist (exit 0), breaking the --json contract
// (flag-ignore / json-contract class). The checklist path must honor --json.

func TestPreflightChecklistJSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := writePreflightChecklist(&buf, true); err != nil {
		t.Fatalf("writePreflightChecklist(json) error: %v", err)
	}
	out := buf.Bytes()

	var got ChecklistResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\noutput: %s", err, out)
	}
	if len(got.Items) == 0 {
		t.Error("JSON checklist must carry the checklist items")
	}
	if got.Hint == "" {
		t.Error("JSON checklist should carry the 'run --check' hint")
	}
}

func TestPreflightChecklistPlaintextUnchanged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := writePreflightChecklist(&buf, false); err != nil {
		t.Fatalf("writePreflightChecklist(plain) error: %v", err)
	}
	s := buf.String()
	// The human path keeps the familiar checklist header + the run-check hint.
	if !strings.Contains(s, "PR Readiness Checklist:") {
		t.Errorf("plaintext checklist missing header, got: %q", s)
	}
	if !strings.Contains(s, "bd preflight --check") {
		t.Errorf("plaintext checklist missing run-check hint, got: %q", s)
	}
	// Plaintext must NOT be JSON (no leading brace).
	if strings.HasPrefix(strings.TrimSpace(s), "{") {
		t.Errorf("plaintext path must not emit JSON, got: %q", s)
	}
}
