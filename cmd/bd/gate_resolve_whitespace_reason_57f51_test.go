//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedGateResolveWhitespaceReason_57f51 proves a whitespace-only
// `bd gate resolve --reason "   "` collapses to no-reason instead of storing a
// blank close_reason / leaking a blank into the --json doc / printing a
// dangling "Reason:  " line. in93a stored-blank-reason class (close/mol
// squash/reopen/todo siblings); gate resolve is the optional-no-default member.
//
// RED without the state.go/gate.go TrimSpace guard: the --json doc carries
// "reason":"   " (whitespace verbatim) and the plaintext path prints
// "  Reason:   ".
func TestEmbeddedGateResolveWhitespaceReason_57f51(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded-dolt e2e")
	}
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "gw")
	// Register "gate" as a custom type so `bd gate create` / createGate work.
	st := openStore(t, beadsDir, "gw")
	if err := st.SetConfig(t.Context(), "types.custom", `["gate"]`); err != nil {
		t.Fatalf("SetConfig types.custom: %v", err)
	}
	st.Close()

	t.Run("whitespace_reason_collapses_in_json", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Whitespace reason gate")
		out := bdGate(t, bd, dir, "resolve", gate.ID, "--reason", "   ", "--json")
		s := strings.TrimSpace(out)
		if !strings.HasPrefix(s, "{") {
			t.Fatalf("expected a JSON object, got:\n%s", out)
		}
		// The reason value in the JSON doc must be empty, not the whitespace blob.
		if strings.Contains(out, `"reason": "   "`) || strings.Contains(out, `"reason":"   "`) {
			t.Errorf("whitespace-only --reason leaked into --json doc: %s", out)
		}
	})

	t.Run("whitespace_reason_no_dangling_label_plaintext", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Whitespace reason gate plaintext")
		out := bdGate(t, bd, dir, "resolve", gate.ID, "--reason", "   ")
		if strings.Contains(out, "Reason:") {
			t.Errorf("whitespace-only --reason printed a dangling 'Reason:' label: %q", out)
		}
	})

	t.Run("genuine_reason_still_shown", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Genuine reason gate")
		out := bdGate(t, bd, dir, "resolve", gate.ID, "--reason", "CI passed")
		if !strings.Contains(out, "CI passed") {
			t.Errorf("genuine --reason must survive verbatim, got: %q", out)
		}
	})

	// bd set-state --reason is the second in93a-class member (state.go:221):
	// a whitespace-only reason must NOT append a dangling "\n\nReason:   " to the
	// recorded event description; a genuine reason must survive verbatim.
	t.Run("set_state_whitespace_reason_no_dangling_label", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "set-state whitespace reason", "--type", "task")
		eventID := setStateEventID(t, bd, dir, issue.ID, "risk=high", "   ")
		ev := bdShow(t, bd, dir, eventID)
		if strings.Contains(ev.Description, "Reason:") {
			t.Errorf("whitespace-only --reason left a dangling 'Reason:' label in event %s desc: %q", eventID, ev.Description)
		}
	})

	t.Run("set_state_genuine_reason_recorded", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "set-state genuine reason", "--type", "task")
		eventID := setStateEventID(t, bd, dir, issue.ID, "risk=low", "audited clean")
		ev := bdShow(t, bd, dir, eventID)
		if !strings.Contains(ev.Description, "Reason: audited clean") {
			t.Errorf("genuine --reason must be recorded in event %s desc, got: %q", eventID, ev.Description)
		}
	})

	// bd gate create --reason is the third gate-family member (gate.go:322):
	// a whitespace-only reason must NOT append a dangling "\n\nReason:   " to the
	// created gate's description.
	t.Run("gate_create_whitespace_reason_no_dangling_label", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "gate create whitespace target", "--type", "task")
		gate := gateCreateJSON(t, bd, dir, task.ID, "   ")
		if strings.Contains(gate.Description, "Reason:") {
			t.Errorf("whitespace-only --reason left a dangling 'Reason:' label in gate %s desc: %q", gate.ID, gate.Description)
		}
	})

	t.Run("gate_create_genuine_reason_recorded", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "gate create genuine target", "--type", "task")
		gate := gateCreateJSON(t, bd, dir, task.ID, "needs design review")
		if !strings.Contains(gate.Description, "Reason: needs design review") {
			t.Errorf("genuine --reason must be recorded in gate %s desc, got: %q", gate.ID, gate.Description)
		}
	})
}

// gateCreateJSON runs `bd gate create --blocks <target> --reason <reason> --json`
// and returns the created gate issue.
func gateCreateJSON(t *testing.T, bd, dir, target, reason string) *types.Issue {
	t.Helper()
	cmd := exec.Command(bd, "gate", "create", "--blocks", target, "--reason", reason, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd gate create --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("no JSON in gate create output: %s", s)
	}
	var gate types.Issue
	if e := json.Unmarshal([]byte(s[start:]), &gate); e != nil {
		t.Fatalf("parse gate create JSON: %v\n%s", e, s)
	}
	return &gate
}

// setStateEventID runs `bd set-state <issue> <dim=val> --reason <reason> --json`
// and returns the created event's id.
func setStateEventID(t *testing.T, bd, dir, issueID, spec, reason string) string {
	t.Helper()
	cmd := exec.Command(bd, "set-state", issueID, spec, "--reason", reason, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd set-state --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	var obj map[string]interface{}
	if e := json.Unmarshal(stdout.Bytes(), &obj); e != nil {
		t.Fatalf("set-state --json not parseable: %v\n%s", e, stdout.String())
	}
	id, _ := obj["event_id"].(string)
	if id == "" {
		t.Fatalf("set-state --json missing event_id: %v", obj)
	}
	return id
}
