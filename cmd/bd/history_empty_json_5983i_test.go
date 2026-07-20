//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedHistoryEmptyJSONArray_5983i: bd history <id> --json on an issue
// with no version history must emit [] not null (beads-5983i, guib/tamf class).
// A wisp (ephemeral, not in the versioned issues table) has no dolt_history_issues
// rows, so History() returns empty → the len==0 --json leg fires.
func TestEmbeddedHistoryEmptyJSONArray_5983i(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "he")
	wisp := bdCreate(t, bd, dir, "ephemeral no-history", "--ephemeral")

	cmd := exec.Command(bd, "history", wisp.ID, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	// find the JSON payload
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		t.Fatalf("no JSON in history output: %s", s)
	}
	body := s[start:]
	if strings.Contains(body, "null") && !strings.Contains(body, "[]") {
		t.Errorf("bd history --json on empty-history emitted null, want [] (beads-5983i): %s", body)
	}
	// It must parse as a (possibly empty) JSON array, not null.
	var arr []json.RawMessage
	if jerr := json.Unmarshal([]byte(body), &arr); jerr != nil {
		// could be wrapped in an envelope; accept if it contains "[]"
		if !strings.Contains(body, "[]") {
			t.Errorf("history --json empty not a JSON array (beads-5983i): %v\n%s", jerr, body)
		}
	}
}
