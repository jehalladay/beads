//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerLabelPropagate is the teeth for beads-ouxlo: `bd label
// propagate` must WORK in proxied-server mode. Before the fix, propagate
// resolved the parent + searched children + mutated through the direct nil
// global `store` in proxiedServerMode (no usesProxiedServer() routing), so it
// failed "storage is nil" for hub-connected crew — unlike its mutation siblings
// `label add`/`label remove` (routed by beads-aocj). propagate was the 3rd label
// mutation the aocj sweep missed.
func TestProxiedServerLabelPropagate(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	hasLabel := func(labels []string, want string) bool {
		for _, l := range labels {
			if l == want {
				return true
			}
		}
		return false
	}

	t.Run("happy_path_labels_all_children", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lpp1")
		parent := bdProxiedCreate(t, bd, p.dir, "Epic parent", "--type", "epic")
		c1 := bdProxiedCreate(t, bd, p.dir, "Child one", "--type", "task", "--parent", parent.ID)
		c2 := bdProxiedCreate(t, bd, p.dir, "Child two", "--type", "task", "--parent", parent.ID)

		out, err := bdProxiedRun(t, bd, p.dir, "label", "propagate", parent.ID, "branch:feat-x")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied label propagate failed: %v\n%s", err, s)
		}
		// The exact defect: the nil-store fail-loud must NOT appear.
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied label propagate hit the nil-store path (beads-ouxlo): %s", s)
		}
		if !strings.Contains(s, "Propagated label") {
			t.Errorf("expected '✓ Propagated label' output, got: %s", s)
		}

		// Teeth: the label actually landed on BOTH children in the DB.
		got1 := bdProxiedShow(t, bd, p.dir, c1.ID)
		if !hasLabel(got1.Labels, "branch:feat-x") {
			t.Errorf("child %s labels = %v, want to contain branch:feat-x", c1.ID, got1.Labels)
		}
		got2 := bdProxiedShow(t, bd, p.dir, c2.ID)
		if !hasLabel(got2.Labels, "branch:feat-x") {
			t.Errorf("child %s labels = %v, want to contain branch:feat-x", c2.ID, got2.Labels)
		}
	})

	t.Run("json_emits_propagated_entries", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lpp2")
		parent := bdProxiedCreate(t, bd, p.dir, "JSON parent", "--type", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "JSON child", "--type", "task", "--parent", parent.ID)

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "label", "propagate", parent.ID, "team:core", "--json")
		if err != nil {
			t.Fatalf("proxied label propagate --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout, "storage is nil") || strings.Contains(stderr, "storage is nil") {
			t.Fatalf("proxied label propagate --json hit the nil-store path (beads-ouxlo)\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "\"status\": \"propagated\"") && !strings.Contains(stdout, "\"status\":\"propagated\"") {
			t.Errorf("expected JSON status:propagated on stdout, got:\n%s", stdout)
		}
		if !strings.Contains(stdout, child.ID) {
			t.Errorf("expected child id %s in JSON output, got:\n%s", child.ID, stdout)
		}
		got := bdProxiedShow(t, bd, p.dir, child.ID)
		if !hasLabel(got.Labels, "team:core") {
			t.Errorf("child %s labels = %v, want to contain team:core", child.ID, got.Labels)
		}
	})

	t.Run("no_children_is_clean", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lpp3")
		lonely := bdProxiedCreate(t, bd, p.dir, "No children", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "label", "propagate", lonely.ID, "orphan-label")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied label propagate on childless parent should succeed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied label propagate hit the nil-store path (beads-ouxlo): %s", s)
		}
		if !strings.Contains(s, "No children found") {
			t.Errorf("expected 'No children found' for a childless parent, got: %s", s)
		}
	})

	t.Run("provides_label_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lpp4")
		parent := bdProxiedCreate(t, bd, p.dir, "Provides parent", "--type", "epic")
		_ = bdProxiedCreate(t, bd, p.dir, "Provides child", "--type", "task", "--parent", parent.ID)

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "label", "propagate", parent.ID, "provides:cap")
		if err == nil {
			t.Fatalf("proxied label propagate provides: should be rejected; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		combined := stdout + stderr
		if !strings.Contains(combined, "reserved for cross-project capabilities") {
			t.Errorf("expected the provides: rejection message, got stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})
}
