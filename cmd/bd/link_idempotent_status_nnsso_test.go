//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestLinkIdempotentStatus_nnsso pins the beads-nnsso fix: `bd link A B` on an
// edge that ALREADY exists (identical source, target, and type) must report
// status:"unchanged", NOT status:"added". `bd link` is documented shorthand for
// `bd dep add`, and `dep add` already reports the honest no-op (beads-bwla
// direct / beads-epuz proxied); link had hardcoded {"status":"added"}
// unconditionally, so a duplicate link printed a false "added" while the store
// held a single edge (AddDependency is idempotent).
//
// Mutation check: drop the GetDependencyRecords precheck in link.go's RunE and
// second_link_reports_unchanged goes RED (status:"added" on the 2nd call).
func TestLinkIdempotentStatus_nnsso(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "lk")

	type linkResult struct {
		Status      string `json:"status"`
		IssueID     string `json:"issue_id"`
		DependsOnID string `json:"depends_on_id"`
		Type        string `json:"type"`
	}
	runLinkJSON := func(t *testing.T, args ...string) linkResult {
		t.Helper()
		full := append([]string{"link"}, args...)
		full = append(full, "--json")
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd link %v failed: %v\n%s", args, err, out)
		}
		s := strings.TrimSpace(string(out))
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("no JSON object in link output: %s", s)
		}
		var res linkResult
		if err := json.Unmarshal([]byte(s[start:]), &res); err != nil {
			t.Fatalf("parse link JSON: %v\nstdout: %s", err, s)
		}
		return res
	}

	a := bdCreate(t, bd, dir, "link idempotent A", "--type", "task")
	b := bdCreate(t, bd, dir, "link idempotent B", "--type", "task")

	// First link: a genuine new edge → "added".
	first := runLinkJSON(t, a.ID, b.ID)
	if first.Status != "added" {
		t.Fatalf("first link: expected status added, got %+v", first)
	}
	if first.IssueID != a.ID || first.DependsOnID != b.ID {
		t.Errorf("first link: expected %s->%s, got %+v", a.ID, b.ID, first)
	}

	// Second identical link: idempotent no-op → "unchanged" (this is the fix).
	second := runLinkJSON(t, a.ID, b.ID)
	if second.Status != "unchanged" {
		t.Fatalf("second (duplicate) link: expected status unchanged, got %+v", second)
	}
	if second.IssueID != a.ID || second.DependsOnID != b.ID {
		t.Errorf("second link: expected %s->%s, got %+v", a.ID, b.ID, second)
	}

	// Storage must hold exactly ONE edge (idempotent) — the "unchanged" report
	// reflects reality, not a suppressed duplicate.
	depCmd := exec.Command(bd, "dep", "list", a.ID, "--json")
	depCmd.Dir = dir
	depCmd.Env = bdEnv(dir)
	depOut, err := depCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dep list %s failed: %v\n%s", a.ID, err, depOut)
	}
	ds := strings.TrimSpace(string(depOut))
	dstart := strings.Index(ds, "[")
	if dstart < 0 {
		t.Fatalf("no JSON array in dep list output: %s", ds)
	}
	var edges []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(ds[dstart:]), &edges); err != nil {
		t.Fatalf("parse dep list JSON: %v\n%s", err, ds)
	}
	count := 0
	for _, e := range edges {
		if e.ID == b.ID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 edge %s->%s after 2 links, got %d: %+v", a.ID, b.ID, count, edges)
	}

	// Negative control: a genuinely NEW edge (distinct target) reports "added".
	// (beads enforces one edge per source->target pair regardless of type, so a
	// different --type between the SAME pair is rejected by the store — not an
	// "unchanged" no-op — hence the distinct-target control here.)
	c := bdCreate(t, bd, dir, "link idempotent C", "--type", "task")
	fresh := runLinkJSON(t, a.ID, c.ID)
	if fresh.Status != "added" {
		t.Errorf("fresh distinct edge: expected status added, got %+v", fresh)
	}
}
