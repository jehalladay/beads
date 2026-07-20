//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestCommentCreatedAtReadAfterWrite_yreoa is the beads-yreoa regression.
//
// `bd comment / bd comments add --json` emitted a comment's created_at at
// NANOSECOND precision (the caller's in-memory time.Now().UTC()), but the
// created_at COLUMN is DATETIME (second precision), so every subsequent read
// (`bd comments <id> --json`) returned the SAME comment second-truncated. The
// add-emit and the read of the identical comment therefore disagreed by up to
// ~1s — a spurious read-after-write mismatch for any tool that stores the
// add-emit value and later diffs it against a read.
//
// Root: internal/storage/issueops/ImportIssueCommentInTx returned the caller's
// un-truncated createdAt verbatim in *types.Comment. The fix truncates
// createdAt to time.Second before the INSERT and the return, so the add-emit
// equals the persisted (and thus every later-read) value. This is the single
// shared point for BOTH the direct (comment.go/comments.go) and proxied
// (comment_proxied_server.go via CommentUseCase.AddComment) paths.
//
// This test drives the real binary in embedded mode: it adds a comment with
// --json, captures the emitted created_at, then lists the comments with --json
// and asserts the same comment's created_at is byte-identical to the add-emit.
func TestCommentCreatedAtReadAfterWrite_yreoa(t *testing.T) {
	bd := buildEmbeddedBD(t)

	dir, _, _ := bdInit(t, bd, "--prefix", "yr-")
	issue := bdCreate(t, bd, dir, "yreoa comment target", "--type", "task")

	// --- add a comment with --json, capture the emitted created_at ---
	addCmd := exec.Command(bd, "comments", "add", issue.ID, "read-after-write probe", "--json")
	addCmd.Dir = dir
	addCmd.Env = bdEnv(dir)
	addOut, addErr, err := runCommandBuffers(t, addCmd)
	if err != nil {
		t.Fatalf("`bd comments add --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, addOut.String(), addErr.String())
	}
	addStr := strings.TrimSpace(addOut.String())
	brace := strings.Index(addStr, "{")
	if brace < 0 {
		t.Fatalf("no JSON object in add output: %s", addStr)
	}
	var added map[string]interface{}
	if jerr := json.Unmarshal([]byte(addStr[brace:]), &added); jerr != nil {
		t.Fatalf("parse add JSON: %v\n%s", jerr, addStr)
	}
	addCreatedAt, _ := added["created_at"].(string)
	if addCreatedAt == "" {
		t.Fatalf("add-emit has no created_at: %s", addStr)
	}
	commentID, _ := added["id"].(string)
	if commentID == "" {
		t.Fatalf("add-emit has no id: %s", addStr)
	}

	// --- list the comments with --json, find the same comment ---
	listCmd := exec.Command(bd, "comments", issue.ID, "--json")
	listCmd.Dir = dir
	listCmd.Env = bdEnv(dir)
	listOut, listErr, err := runCommandBuffers(t, listCmd)
	if err != nil {
		t.Fatalf("`bd comments <id> --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, listOut.String(), listErr.String())
	}
	listStr := strings.TrimSpace(listOut.String())
	bracket := strings.Index(listStr, "[")
	if bracket < 0 {
		t.Fatalf("no JSON array in list output: %s", listStr)
	}
	var listed []map[string]interface{}
	if jerr := json.Unmarshal([]byte(listStr[bracket:]), &listed); jerr != nil {
		t.Fatalf("parse list JSON: %v\n%s", jerr, listStr)
	}

	var readCreatedAt string
	found := false
	for _, c := range listed {
		if id, _ := c["id"].(string); id == commentID {
			readCreatedAt, _ = c["created_at"].(string)
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("added comment %s not found in list: %s", commentID, listStr)
	}

	// The core yreoa assertion: the add-emit created_at must equal the read of
	// the identical comment. Pre-fix the add-emit is nanosecond precision and
	// the read is second-truncated, so they differ.
	if addCreatedAt != readCreatedAt {
		t.Errorf("read-after-write created_at mismatch (beads-yreoa):\n  add-emit:  %q\n  list-read: %q\n(same comment %s; add-emit must be second-truncated to match the persisted column)", addCreatedAt, readCreatedAt, commentID)
	}
}
