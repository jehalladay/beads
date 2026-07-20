//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestCloseClaimNextFreshJSON_yt2hi covers beads-yt2hi: `bd close --claim-next
// --json` emitted the PRE-claim snapshot of the newly-claimed issue in the
// `claimed` field (status:open, no assignee/started_at) while the DB row was
// actually in_progress+assigned — a consumer reading claimed.status/assignee to
// confirm the auto-claim landed would wrongly conclude it did not. The fix
// re-fetches the issue after ClaimIssue so `claimed` reflects the persisted
// state.
func TestCloseClaimNextFreshJSON_yt2hi(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cn")

	toClose := bdCreate(t, bd, dir, "yt2hi close", "--type", "task")
	target := bdCreate(t, bd, dir, "yt2hi target", "--type", "task")

	cmd := exec.Command(bd, "close", toClose.ID, "--claim-next", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd close --claim-next --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	s := stdout.String()
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("expected a JSON object, got:\n%s", s)
	}
	var obj struct {
		Claimed *struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			Assignee string `json:"assignee"`
		} `json:"claimed"`
	}
	if err := json.Unmarshal([]byte(s[start:]), &obj); err != nil {
		t.Fatalf("parse --json: %v\nraw:\n%s", err, s[start:])
	}
	if obj.Claimed == nil {
		t.Fatalf("claimed object missing; expected the auto-claimed target %s", target.ID)
	}
	// The claimed object must reflect the POST-claim persisted state, not the
	// stale pre-claim snapshot (beads-yt2hi).
	if obj.Claimed.ID != target.ID {
		t.Errorf("claimed.id = %q, want %q", obj.Claimed.ID, target.ID)
	}
	if obj.Claimed.Status != "in_progress" {
		t.Errorf("claimed.status = %q, want \"in_progress\" (stale pre-claim snapshot, beads-yt2hi)", obj.Claimed.Status)
	}
	if obj.Claimed.Assignee == "" {
		t.Errorf("claimed.assignee is empty — stale pre-claim snapshot (beads-yt2hi); the claim set an assignee")
	}

	// Corroborate against the persisted row via show --json.
	showAssignee := jsonFieldOfShow(t, bd, dir, target.ID, "assignee")
	if showAssignee == "" {
		t.Errorf("persisted assignee empty via show — test setup issue")
	}
	if obj.Claimed.Assignee != showAssignee {
		t.Errorf("claimed.assignee (%q) disagrees with persisted show assignee (%q) — stale snapshot (beads-yt2hi)", obj.Claimed.Assignee, showAssignee)
	}
}
