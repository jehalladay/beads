//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedCreateRejectsProvidesLabel_o70m1 pins the beads-o70m1 write-time
// reservation of the 'provides:' cross-project capability family at the CREATE
// authoring seams (end-to-end against the real bd binary).
//
// 'provides:<cap>' marks a single-provider cross-project capability; `bd ship`
// is the only sanctioned way to apply it (it enforces the closed-requirement +
// single-provider invariants before stamping the label via the storage layer).
// `bd label add` already rejected a hand-set provides: (label.go), but the
// create seams did not, so `bd create --labels provides:x` and
// `bd create --graph {nodes:[{labels:[provides:x]}]}` both minted an OPEN bead
// carrying the capability at RC=0 — bypassing both invariants. This exercises
// the guard through the compiled binary on both create axes.
//
// MUTATION-VERIFIED: reverting the providesLabelError call in create.go /
// graph_apply.go makes the reject sub-tests go RED (create succeeds, RC=0).
func TestEmbeddedCreateRejectsProvidesLabel_o70m1(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt create tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	t.Run("create_--labels_provides_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pv")
		out := bdCreateFail(t, bd, dir, "provides cap issue", "--labels", "provides:mycap")
		if !strings.Contains(out, "provides:") || !strings.Contains(out, "bd ship") {
			t.Errorf("reject message should mention 'provides:' and the 'bd ship' hint, got: %s", out)
		}
	})

	t.Run("create_--label_alias_provides_rejected", func(t *testing.T) {
		// The --label alias flows into the same label loop as --labels.
		dir, _, _ := bdInit(t, bd, "--prefix", "pa")
		out := bdCreateFail(t, bd, dir, "provides via alias", "--label", "provides:aliascap")
		if !strings.Contains(out, "provides:") {
			t.Errorf("reject message should mention 'provides:', got: %s", out)
		}
	})

	t.Run("create_graph_node_provides_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pg")
		plan := `{
			"nodes": [
				{"key": "root", "title": "Graph provides", "type": "task", "labels": ["provides:viacg"]}
			]
		}`
		planFile := filepath.Join(dir, "provides-plan.json")
		if err := os.WriteFile(planFile, []byte(plan), 0o600); err != nil {
			t.Fatalf("write graph plan: %v", err)
		}
		cmd := exec.Command(bd, "create", "--json", "--graph", planFile)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("bd create --graph with a provides: node label should have failed, got rc0:\n%s", out)
		}
		if !strings.Contains(string(out), "provides:") {
			t.Errorf("graph reject should mention 'provides:', got: %s", out)
		}
	})

	t.Run("ordinary_and_export_labels_still_allowed", func(t *testing.T) {
		// The control: a non-provides label — including ship's own 'export:'
		// INPUT label (which humans DO set by hand before `bd ship`) — must
		// still create normally. Guards against an over-broad reservation.
		dir, _, _ := bdInit(t, bd, "--prefix", "ok")
		issue := bdCreate(t, bd, dir, "exportable issue", "--labels", "export:mycap")
		if issue.ID == "" {
			t.Fatal("expected a created issue for the allowed export: label")
		}
		found := false
		for _, l := range issue.Labels {
			if l == "export:mycap" {
				found = true
			}
			if strings.HasPrefix(l, "provides:") {
				t.Errorf("issue unexpectedly carries a provides: label: %v", issue.Labels)
			}
		}
		if !found {
			t.Errorf("expected the export: label to be preserved, got %v", issue.Labels)
		}
	})
}
