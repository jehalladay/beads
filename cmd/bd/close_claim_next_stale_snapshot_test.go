//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestCloseClaimNextStaleSnapshot_yt2hi is the beads-yt2hi regression. The
// `bd close --claim-next --json` "claimed" object was a PRE-claim snapshot
// (readyIssues[0] / page.Items[0]) that was never re-read after the claim, so
// the store-level ClaimIssue (which returns only error) left it reporting
// status:open, assignee:"", started_at:nil — contradicting the persisted row
// (in_progress + assignee set + started_at set). A consumer reading
// claimed.status / claimed.assignee to confirm the auto-claim landed would
// WRONGLY conclude it did not take.
//
// The fix re-fetches the mutated row (GetIssue) after a successful claim on
// BOTH the direct (close.go) and proxied (close_proxied_server.go) paths.
// This test drives the real binary in embedded mode with an explicit actor so
// the assignee is deterministic, and asserts the claimed object reflects the
// post-claim state.
func TestCloseClaimNextStaleSnapshot_yt2hi(t *testing.T) {
	bd := buildEmbeddedBD(t)

	dir, _, _ := bdInit(t, bd, "--prefix", "yt-")
	toClose := bdCreate(t, bd, dir, "yt2hi close A", "--type", "task", "--priority", "1")
	_ = bdCreate(t, bd, dir, "yt2hi claim target B", "--type", "task", "--priority", "2")

	cmd := exec.Command(bd, "close", toClose.ID, "--claim-next", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd close --claim-next --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("close --claim-next --json must emit a JSON object: %v\nstdout:\n%s", jerr, out)
	}

	claimed, ok := obj["claimed"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a claimed OBJECT (a P2 candidate was available), got: %v", obj["claimed"])
	}

	// The core yt2hi assertions: the claimed object must reflect the PERSISTED
	// post-claim state, not the pre-claim snapshot.
	if got, _ := claimed["status"].(string); got != "in_progress" {
		t.Errorf("claimed.status = %q, want \"in_progress\" (stale pre-claim snapshot leaked?): %s", got, out)
	}
	// The pre-claim snapshot has an empty assignee (omitempty → absent); the
	// claimed row is assigned to the resolved actor. Assert non-empty rather
	// than a fixed value (the actor resolves from config in the embedded env).
	if got, _ := claimed["assignee"].(string); got == "" {
		t.Errorf("claimed.assignee is empty; want the resolved actor (stale pre-claim snapshot leaked?): %s", out)
	}
	// started_at is stamped on the in_progress transition; the pre-claim
	// snapshot has none (omitempty → absent).
	if started, present := claimed["started_at"]; !present || started == nil || started == "" {
		t.Errorf("claimed.started_at missing/empty; want a timestamp from the claim transition: %s", out)
	}
}
