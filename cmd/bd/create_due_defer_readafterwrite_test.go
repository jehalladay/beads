//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestCreateDueDeferReadAfterWrite_17n4h is the beads-17n4h regression.
//
// `bd create --due <rel> --defer <rel> --json` emits due_at/defer_until at
// NANOSECOND precision — a relative parse (ParseRelativeTime -> applyDuration ->
// now.Add(...)) preserves the sub-second component of time.Now(), and create.go
// emits the in-memory struct verbatim without re-reading. But the due_at /
// defer_until columns are DATETIME (second precision), so every later read (bd
// show/list --json) returns them second-truncated — a read-after-write mismatch.
//
// Fourth member of the read-after-write-emits-unpersisted-value class (siblings
// beads-8ukct created_at/updated_at, beads-yt2hi close --claim-next, beads-yreoa
// comments add). PrepareIssueForInsert truncated created_at/updated_at but not
// due_at/defer_until; the fix truncates those two at the same shared point.
//
// Drives the real binary: create with --json (relative --due/--defer, which
// carry ns), capture the emitted due_at/defer_until, then show --json and assert
// byte-identical.
func TestCreateDueDeferReadAfterWrite_17n4h(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dd-")

	createCmd := exec.Command(bd, "create", "due/defer read-after-write probe",
		"--type", "task", "--due", "+6h", "--defer", "+3h", "--json")
	createCmd.Dir = dir
	createCmd.Env = bdEnv(dir)
	createOut, createErr, err := runCommandBuffers(t, createCmd)
	if err != nil {
		t.Fatalf("`bd create --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, createOut.String(), createErr.String())
	}
	createStr := strings.TrimSpace(createOut.String())
	brace := strings.Index(createStr, "{")
	if brace < 0 {
		t.Fatalf("no JSON object in create output: %s", createStr)
	}
	var created map[string]interface{}
	if jerr := json.Unmarshal([]byte(createStr[brace:]), &created); jerr != nil {
		t.Fatalf("parse create JSON: %v\n%s", jerr, createStr)
	}
	id, _ := created["id"].(string)
	createDueAt, _ := created["due_at"].(string)
	createDeferUntil, _ := created["defer_until"].(string)
	if id == "" || createDueAt == "" || createDeferUntil == "" {
		t.Fatalf("create-emit missing id/due_at/defer_until: %s", createStr)
	}

	showCmd := exec.Command(bd, "show", id, "--json")
	showCmd.Dir = dir
	showCmd.Env = bdEnv(dir)
	showOut, showErr, err := runCommandBuffers(t, showCmd)
	if err != nil {
		t.Fatalf("`bd show <id> --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, showOut.String(), showErr.String())
	}
	showStr := strings.TrimSpace(showOut.String())
	sbracket := strings.Index(showStr, "[")
	if sbracket < 0 {
		t.Fatalf("no JSON array in show output: %s", showStr)
	}
	var shownList []map[string]interface{}
	if jerr := json.Unmarshal([]byte(showStr[sbracket:]), &shownList); jerr != nil {
		t.Fatalf("parse show JSON: %v\n%s", jerr, showStr)
	}
	if len(shownList) == 0 {
		t.Fatalf("bd show returned no issue for %s: %s", id, showStr)
	}
	shown := shownList[0]
	showDueAt, _ := shown["due_at"].(string)
	showDeferUntil, _ := shown["defer_until"].(string)

	if createDueAt != showDueAt {
		t.Errorf("read-after-write due_at mismatch (beads-17n4h):\n  create-emit: %q\n  show-read:   %q\n(same issue %s; create-emit must be second-truncated to match the persisted column)", createDueAt, showDueAt, id)
	}
	if createDeferUntil != showDeferUntil {
		t.Errorf("read-after-write defer_until mismatch (beads-17n4h):\n  create-emit: %q\n  show-read:   %q\n(same issue %s)", createDeferUntil, showDeferUntil, id)
	}
}
