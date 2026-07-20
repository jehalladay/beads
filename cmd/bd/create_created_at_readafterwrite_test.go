//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestCreateCreatedAtReadAfterWrite_8ukct is the beads-8ukct regression.
//
// `bd create --json` emitted the created issue's created_at + updated_at at
// NANOSECOND precision (PrepareIssueForInsert stamped the caller's in-memory
// time.Now().UTC() onto the struct, and cmd/bd/create.go emitted that struct
// verbatim without re-reading), but the created_at/updated_at columns are
// DATETIME (second precision). So every subsequent read (bd show/list --json)
// returned the SAME issue second-truncated (rounding up to ~1s) — a spurious
// read-after-write mismatch for any tool that stores the create-emit timestamp
// and later diffs it against a read.
//
// Third member of the read-after-write-emits-unpersisted-value class (siblings
// beads-yt2hi close --claim-next, beads-yreoa comments add). The fix truncates
// created_at/updated_at to time.Second in the single shared PrepareIssueForInsert
// point (covers single-create, batch-create, and promote legs), so the emitted
// struct matches the persisted (and every later-read) value.
//
// This test drives the real binary in embedded mode: it creates an issue with
// --json, captures the emitted created_at/updated_at, then shows the issue with
// --json and asserts they are byte-identical.
func TestCreateCreatedAtReadAfterWrite_8ukct(t *testing.T) {
	bd := buildEmbeddedBD(t)

	dir, _, _ := bdInit(t, bd, "--prefix", "uk-")

	// --- create with --json, capture emitted created_at/updated_at ---
	createCmd := exec.Command(bd, "create", "read-after-write create probe", "--type", "task", "--json")
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
	createCreatedAt, _ := created["created_at"].(string)
	createUpdatedAt, _ := created["updated_at"].(string)
	if id == "" || createCreatedAt == "" || createUpdatedAt == "" {
		t.Fatalf("create-emit missing id/created_at/updated_at: %s", createStr)
	}

	// --- show the issue with --json, read the persisted timestamps ---
	showCmd := exec.Command(bd, "show", id, "--json")
	showCmd.Dir = dir
	showCmd.Env = bdEnv(dir)
	showOut, showErr, err := runCommandBuffers(t, showCmd)
	if err != nil {
		t.Fatalf("`bd show <id> --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, showOut.String(), showErr.String())
	}
	// `bd show --json` emits an array of matching issues.
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
	showCreatedAt, _ := shown["created_at"].(string)
	showUpdatedAt, _ := shown["updated_at"].(string)

	// The core 8ukct assertions: the create-emit timestamps must equal the read
	// of the identical issue. Pre-fix the create-emit is nanosecond precision
	// and the read is second-truncated, so they differ.
	if createCreatedAt != showCreatedAt {
		t.Errorf("read-after-write created_at mismatch (beads-8ukct):\n  create-emit: %q\n  show-read:   %q\n(same issue %s; create-emit must be second-truncated to match the persisted column)", createCreatedAt, showCreatedAt, id)
	}
	if createUpdatedAt != showUpdatedAt {
		t.Errorf("read-after-write updated_at mismatch (beads-8ukct):\n  create-emit: %q\n  show-read:   %q\n(same issue %s)", createUpdatedAt, showUpdatedAt, id)
	}
}
