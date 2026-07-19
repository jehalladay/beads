//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestProxiedServerOrphans proves `bd orphans` is proxied-server-aware
// (beads-ktlo). The direct path builds a doltStoreProvider backed by the global
// `store`, which is NIL in proxiedServerMode → getIssueProviderFn returns
// "no database available" on hub-connected crew. The fix routes through a
// proxiedIssueProvider over the UOW use-cases (clean-mirror — GetOpenIssues via
// IssueUseCase.SearchIssues, GetIssuePrefix via ConfigUseCase.GetConfig).
func TestProxiedServerOrphans(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// gitCommit makes an empty commit with the given message in dir.
	gitCommit := func(t *testing.T, dir, msg string) {
		t.Helper()
		cmd := exec.Command("git", "commit", "--allow-empty", "-m", msg)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit failed: %v\n%s", err, out)
		}
	}

	t.Run("orphans_no_database_unavailable", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "orp")
		// An open issue referenced in a commit message is an "orphan".
		iss := bdProxiedCreate(t, bd, p.dir, "implement thing", "--type", "task")
		gitCommit(t, p.dir, "feat: did it ("+iss.ID+")")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "orphans")
		if err != nil {
			t.Fatalf("bd orphans failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "no database available") {
			t.Fatalf("bd orphans hit 'no database available' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd orphans hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		// The open issue referenced in the commit must be reported as an orphan.
		if !strings.Contains(stdout, iss.ID) {
			t.Errorf("expected orphan %s in output:\n%s", iss.ID, stdout)
		}
	})

	t.Run("orphans_json_lists_orphan", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "orj")
		iss := bdProxiedCreate(t, bd, p.dir, "json orphan", "--type", "bug")
		gitCommit(t, p.dir, "fix: closed in code ("+iss.ID+")")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "orphans", "--json")
		if err != nil {
			t.Fatalf("bd orphans --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "no database available") {
			t.Fatalf("bd orphans --json hit 'no database available':\n%s\n%s", stdout, stderr)
		}
		var orphans []struct {
			IssueID string `json:"issue_id"`
			Status  string `json:"status"`
		}
		if err := json.Unmarshal([]byte(stdout), &orphans); err != nil {
			t.Fatalf("bd orphans --json did not emit valid JSON: %v\nstdout:\n%s", err, stdout)
		}
		found := false
		for _, o := range orphans {
			if o.IssueID == iss.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("expected orphan %s in JSON output:\n%s", iss.ID, stdout)
		}
	})

	t.Run("orphans_none_when_no_commit_ref", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "orn")
		// Open issue that is NOT referenced in any commit → not an orphan.
		bdProxiedCreate(t, bd, p.dir, "unreferenced", "--type", "task")
		gitCommit(t, p.dir, "chore: unrelated commit")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "orphans")
		if err != nil {
			t.Fatalf("bd orphans failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "no database available") {
			t.Fatalf("bd orphans hit 'no database available':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "No orphaned issues found") {
			t.Errorf("expected no orphans, got:\n%s", stdout)
		}
	})
}
