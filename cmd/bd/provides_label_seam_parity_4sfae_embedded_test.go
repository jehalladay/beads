//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// beads-4sfae (embedded legs): end-to-end reservation of the 'provides:'
// capability label at the quick / update / tag seams — the mutation + quick-
// create twins of the create/graph guard beads-o70m1 landed. Each of these
// seams already rejects a reserved gt identity label (m22rq/5x84h/0tm9z) but let
// a hand-set provides: through until this fix.
//
// MUTATION-VERIFY: remove the providesLabelError call at quick.go / update.go /
// tag.go → the matching sub-test goes RED (the create/attach succeeds, RC=0).
func TestEmbeddedProvidesLabelSeamParity_4sfae(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)

	// ── quick: `bd q "x" -l provides:cap` must be rejected ──
	t.Run("quick_provides_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pq")
		cmd := exec.Command(bd, "q", "a task", "-l", "provides:qcap")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("beads-4sfae: `bd q ... -l provides:qcap` unexpectedly SUCCEEDED — a hand-set provides: must be rejected like `bd create -l provides:` (o70m1 parity)\n%s", out)
		}
		if !strings.Contains(string(out), "provides:") {
			t.Errorf("quick rejection should name 'provides:'; got:\n%s", out)
		}
	})

	t.Run("quick_normal_label_ok", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pqn")
		id := bdQuick(t, bd, dir, "a task", "-l", "backend")
		if id == "" {
			t.Fatal("beads-4sfae: a non-provides label must still create via quick")
		}
	})

	// ── update --add-label / --set-labels: attaching provides: to an existing
	//    bead must be rejected (mutation twin) ──
	t.Run("update_add_label_provides_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pu")
		iss := bdCreate(t, bd, dir, "add-label provides target")
		out := bdUpdateFail(t, bd, dir, iss.ID, "--add-label", "provides:ucap")
		if !strings.Contains(out, "provides:") {
			t.Errorf("update --add-label rejection should name 'provides:'; got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, iss.ID); hasLabel4sfae(got.Labels, "provides:ucap") {
			t.Errorf("beads-4sfae: provides: must NOT land via `bd update --add-label`; labels=%v", got.Labels)
		}
	})

	t.Run("update_set_labels_provides_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pus")
		iss := bdCreate(t, bd, dir, "set-labels provides target")
		out := bdUpdateFail(t, bd, dir, iss.ID, "--set-labels", "provides:scap")
		if !strings.Contains(out, "provides:") {
			t.Errorf("update --set-labels rejection should name 'provides:'; got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, iss.ID); hasLabel4sfae(got.Labels, "provides:scap") {
			t.Errorf("beads-4sfae: provides: must NOT land via `bd update --set-labels`; labels=%v", got.Labels)
		}
	})

	// ── tag: `bd tag <id> provides:cap` (update --add-label shorthand, own RunE) ──
	t.Run("tag_provides_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pt")
		iss := bdCreate(t, bd, dir, "tag provides target")
		cmd := exec.Command(bd, "tag", iss.ID, "provides:tcap")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("beads-4sfae: `bd tag %s provides:tcap` unexpectedly SUCCEEDED — the tag verb (update --add-label shorthand) must reject a hand-set provides:\n%s", iss.ID, out)
		}
		if !strings.Contains(string(out), "provides:") {
			t.Errorf("tag rejection should name 'provides:'; got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, iss.ID); hasLabel4sfae(got.Labels, "provides:tcap") {
			t.Errorf("beads-4sfae: provides: must NOT land via `bd tag`; labels=%v", got.Labels)
		}
	})
}

func hasLabel4sfae(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}
