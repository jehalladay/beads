//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestQuickReservedIdentityLabelRejected_m22rq is the teeth for beads-m22rq.
//
// `bd q`/`bd quick` takes a direct human `--labels` flag and mints an issue, but
// was the one create authoring seam the beads-3c4g write-time reservation never
// reached: `bd q "x" -l gt:agent` silently created a bead carrying a reserved
// gt identity label (gt:agent/gt:role/gt:rig) — which the ready discriminator
// (beads-wqs) hides from `bd ready` — while `bd create -l gt:agent` (create.go),
// create-form (beads-1077e), graph create (beads-s13vq), markdown import
// (beads-kvq0v), `bd label add`, and `bd update --add-label` (beads-5x84h) all
// reject it. The reservedIdentityLabelError guard is CLI-layer only (not a
// shared CreateIssue chokepoint), so quick bypassed it entirely.
//
// The fix adds the same guard after labels := GetStringSlice("labels") and
// BEFORE the usesProxiedServer() split, so one site covers both direct and
// proxied modes (same placement as the cra1 trim + n8xi priority guards),
// routed through HandleErrorRespectJSON to keep the --json error contract.
//
// The reject fires before any store access, so it is a deterministic,
// server-free error. MUTATION-VERIFIED: removing the guard loop turns the reject
// assertions RED (`bd q "x" -l gt:agent` succeeds); restoring turns them GREEN.
func TestQuickReservedIdentityLabelRejected_m22rq(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	reserved := []struct {
		name  string
		label string
	}{
		{"gt_agent", "gt:agent"},
		{"gt_role", "gt:role"},
		{"gt_rig", "gt:rig"},
	}

	for _, tc := range reserved {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bd, "q", "a task", "-l", tc.label, "--json")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			if err == nil {
				t.Fatalf("`bd q \"a task\" -l %s` unexpectedly SUCCEEDED — a reserved gt identity label must be rejected like `bd create -l %s` (beads-m22rq)\nstdout:\n%s", tc.label, tc.label, stdout.String())
			}

			// Under --json the error must be a parseable JSON object on stdout
			// (HandleErrorRespectJSON), not empty stdout + stderr text.
			out := strings.TrimSpace(stdout.String())
			if out == "" {
				t.Fatalf("stdout EMPTY on a failing `bd q ... -l %s --json` — the error must be a JSON object on stdout (beads-m22rq)\nstderr:\n%s", tc.label, stderr.String())
			}
			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout not JSON on failing `bd q ... -l %s`: %v\nstdout:\n%s", tc.label, jerr, out)
			}
			msg, ok := obj["error"].(string)
			if !ok {
				if data, dok := obj["data"].(map[string]interface{}); dok {
					msg, ok = data["error"].(string)
				}
			}
			if !ok || !strings.Contains(msg, "reserved gt identity label") {
				t.Errorf("expected a \"reserved gt identity label\" error in failing `bd q ... -l %s` stdout, got: %s", tc.label, out)
			}
		})
	}

	// Regression: a NON-reserved label still succeeds and yields an id — the
	// guard must reject only the gt: identity family, not all labels.
	t.Run("normal_label_ok", func(t *testing.T) {
		cmd := exec.Command(bd, "q", "a task", "-l", "backend")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("`bd q \"a task\" -l backend` failed: %v\nstderr:\n%s", err, stderr.String())
		}
		if strings.TrimSpace(stdout.String()) == "" {
			t.Fatalf("expected an issue id on stdout for a non-reserved label, got empty")
		}
	})
}
