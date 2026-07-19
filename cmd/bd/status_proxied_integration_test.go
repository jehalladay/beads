//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerStatus proves `bd status` / `bd stats` is proxied-server-aware
// (beads-mtgy). The direct RunE reads via the global `store`
// (GetStatistics/SearchIssues/GetReadyWork), which is NIL in proxiedServerMode →
// "storage is nil" on hub-connected crew. The fix routes through the UOW
// IssueUseCase (clean-mirror — all three methods already on IssueUseCase).
func TestProxiedServerStatus(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("status_human_no_storage_nil", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "sth")
		bdProxiedCreate(t, bd, p.dir, "s1", "--type", "bug", "-p", "1")
		bdProxiedCreate(t, bd, p.dir, "s2", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "status", "--no-activity")
		if err != nil {
			t.Fatalf("bd status failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd status hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "Issue Database Status") {
			t.Errorf("expected status overview header:\n%s", stdout)
		}
		if !strings.Contains(stdout, "Total Issues:") {
			t.Errorf("expected Total Issues line:\n%s", stdout)
		}
	})

	t.Run("stats_alias_works", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "sta")
		bdProxiedCreate(t, bd, p.dir, "a1", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "stats", "--no-activity")
		if err != nil {
			t.Fatalf("bd stats (alias) failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd stats alias hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "Issue Database Status") {
			t.Errorf("expected status overview from stats alias:\n%s", stdout)
		}
	})

	t.Run("status_json_summary_counts", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "stj")
		bdProxiedCreate(t, bd, p.dir, "j1", "--type", "bug", "-p", "0")
		bdProxiedCreate(t, bd, p.dir, "j2", "--type", "bug", "-p", "0")
		bdProxiedCreate(t, bd, p.dir, "j3", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "status", "--no-activity", "--json")
		if err != nil {
			t.Fatalf("bd status --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd status --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}

		var out struct {
			Summary struct {
				TotalIssues int `json:"total_issues"`
				OpenIssues  int `json:"open_issues"`
			} `json:"summary"`
		}
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("bd status --json did not emit valid JSON: %v\nstdout:\n%s", err, stdout)
		}
		if out.Summary.TotalIssues != 3 {
			t.Errorf("expected total_issues=3, got %d\n%s", out.Summary.TotalIssues, stdout)
		}
		if out.Summary.OpenIssues != 3 {
			t.Errorf("expected open_issues=3, got %d\n%s", out.Summary.OpenIssues, stdout)
		}
	})

	t.Run("status_assigned_no_storage_nil", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "sas")
		bdProxiedCreate(t, bd, p.dir, "assigned target", "--type", "task")

		// --assigned goes through the SearchIssues/GetReadyWork UOW legs, the
		// second half of the nil-store gap. Just prove it does not blow up.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "status", "--no-activity", "--assigned", "--json")
		if err != nil {
			t.Fatalf("bd status --assigned failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd status --assigned hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		var out struct {
			Summary struct {
				TotalIssues int `json:"total_issues"`
			} `json:"summary"`
		}
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("bd status --assigned --json invalid JSON: %v\n%s", err, stdout)
		}
	})
}
