//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedLabelBatchDedup_hzg2y is the proxied-server twin of beads-hzg2y.
// The proxied label add/remove path (applyLabelBatchProxied) diverges the OTHER
// way from the direct path when the same id is repeated in one batch, because it
// re-reads each occurrence's labels via a short-lived UOW BEFORE the single write
// commits:
//
//   - `bd label add X X foo`: the 1st occurrence sees foo absent → status:"added";
//     the 2nd occurrence STILL sees foo absent (the add hasn't committed a
//     visible read yet) → a duplicate status:"added" as well. Deduping the ids at
//     the command entry collapses this to exactly one result.
//   - `bd label remove X X foo` (X carries foo): both occurrences see foo present
//     → two status:"removed"; but if partitioning ever saw the 2nd as
//     already-gone it would mark X a never-present removal → spurious RC1. Dedup
//     removes both hazards, giving one removed result at RC0.
//
// The uniqueStrings(issueIDs) dedup lives at the SHARED command entry (label.go),
// before the usesProxiedServer() branch, so it fixes both paths at once. This
// test asserts proxied parity with the direct teeth.
func TestProxiedLabelBatchDedup_hzg2y(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	type labelResult struct {
		Status  string `json:"status"`
		IssueID string `json:"issue_id"`
		Label   string `json:"label"`
	}
	parseLabelResults := func(t *testing.T, out []byte) []labelResult {
		t.Helper()
		s := strings.TrimSpace(string(out))
		start := strings.Index(s, "[")
		if start < 0 {
			t.Fatalf("no JSON array in output: %s", s)
		}
		var res []labelResult
		if err := json.Unmarshal([]byte(s[start:]), &res); err != nil {
			t.Fatalf("parse label results JSON: %v\nstdout: %s", err, s)
		}
		return res
	}

	t.Run("proxied_add_repeated_id_counts_once", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pzadd")
		a := bdProxiedCreate(t, bd, p.dir, "Proxied add dup", "--type", "task")
		out, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, a.ID, "pdupid", "--json")
		if err != nil {
			t.Fatalf("proxied label add dup failed: %v\n%s", err, out)
		}
		res := parseLabelResults(t, out)
		if len(res) != 1 {
			t.Fatalf("expected exactly 1 result for a repeated id, got %d: %+v", len(res), res)
		}
		if res[0].Status != "added" || res[0].IssueID != a.ID {
			t.Errorf("expected {added,%s}, got %+v", a.ID, res[0])
		}
	})

	t.Run("proxied_remove_repeated_id_reports_once_rc0", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pzrm")
		a := bdProxiedCreate(t, bd, p.dir, "Proxied remove dup", "--type", "task", "--label", "prmdup")
		out, err := bdProxiedRun(t, bd, p.dir, "label", "remove", a.ID, a.ID, "prmdup", "--json")
		if err != nil {
			t.Fatalf("proxied label remove dup should exit RC0, got err: %v\n%s", err, out)
		}
		res := parseLabelResults(t, out)
		if len(res) != 1 {
			t.Fatalf("expected exactly 1 removed result for a repeated id, got %d: %+v", len(res), res)
		}
		if res[0].Status != "removed" || res[0].IssueID != a.ID {
			t.Errorf("expected {removed,%s}, got %+v", a.ID, res[0])
		}
	})
}
