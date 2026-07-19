//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestQuickWhitespaceTitleRejected_cra1 is the teeth for beads-cra1.
//
// `bd q`/`bd quick` joined its positional args into a title and fed it straight
// into store.CreateIssue with NO trim/empty validation. types.Validate only
// rejects len(Title)==0, so a whitespace-only title like "   " (len>0) was
// accepted with rc=0, creating a blank-displayed bead — while `bd create "   "`
// and `bd update --title "   "` both reject it ("title cannot be empty").
//
// beads-n5xz added the TrimSpace+empty-reject to create.go/create_input.go/
// update.go but NOT quick.go, leaving this q/quick asymmetry. The fix adds the
// same guard after title := strings.Join(args, " ") and BEFORE the
// usesProxiedServer() split, so one site covers both direct and proxied modes,
// routed through HandleErrorRespectJSON to preserve the --json error contract.
//
// The reject fires before any store access, so it is a deterministic,
// server-free error — the teeth run bd as a subprocess and assert a
// whitespace-only title FAILS (rc!=0) with a parseable JSON error under --json,
// and that a real title still succeeds (no regression).
func TestQuickWhitespaceTitleRejected_cra1(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// Whitespace-only titles (spaces, tabs/newlines) must be rejected on `q`
	// (the quick-capture verb; there is no separate `quick` alias), matching
	// create/update.
	rejectCases := []struct {
		name string
		args []string
	}{
		{"q_spaces_json", []string{"q", "   ", "--json"}},
		{"q_tabs_newlines", []string{"q", "\t \n"}},
	}

	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bd, tc.args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			if err == nil {
				t.Fatalf("`bd %s` unexpectedly SUCCEEDED — a whitespace-only title must be rejected like `bd create \"   \"` (beads-cra1)\nstdout:\n%s", strings.Join(tc.args, " "), stdout.String())
			}

			// Under --json the error must be a parseable JSON object on stdout
			// (HandleErrorRespectJSON), not empty stdout + stderr text.
			hasJSON := false
			for _, a := range tc.args {
				if a == "--json" {
					hasJSON = true
				}
			}
			if hasJSON {
				out := strings.TrimSpace(stdout.String())
				if out == "" {
					t.Fatalf("stdout EMPTY on a failing `bd %s --json` — the error must be a JSON object on stdout (beads-cra1)\nstderr:\n%s", strings.Join(tc.args, " "), stderr.String())
				}
				var obj map[string]interface{}
				if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
					t.Fatalf("stdout not JSON on failing `bd %s`: %v\nstdout:\n%s", strings.Join(tc.args, " "), jerr, out)
				}
				msg, ok := obj["error"].(string)
				if !ok {
					if data, dok := obj["data"].(map[string]interface{}); dok {
						msg, ok = data["error"].(string)
					}
				}
				if !ok || msg == "" {
					t.Errorf("expected non-empty \"error\" in failing `bd %s` stdout, got: %s", strings.Join(tc.args, " "), out)
				}
			}
		})
	}

	// Regression: a real title still succeeds and yields an id.
	t.Run("real_title_ok", func(t *testing.T) {
		cmd := exec.Command(bd, "q", "a genuine title")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("`bd q \"a genuine title\"` failed: %v\nstderr:\n%s", err, stderr.String())
		}
		if strings.TrimSpace(stdout.String()) == "" {
			t.Fatalf("expected an issue id on stdout for a valid title, got empty")
		}
	})

	// A padded-but-nonempty title should be trimmed and accepted (not stored
	// verbatim), matching create.go's TrimSpace behavior.
	t.Run("padded_title_trimmed_ok", func(t *testing.T) {
		cmd := exec.Command(bd, "q", "  spaced title  ")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("`bd q \"  spaced title  \"` failed: %v\nstderr:\n%s", err, stderr.String())
		}
		if strings.TrimSpace(stdout.String()) == "" {
			t.Fatalf("expected an issue id on stdout for a padded-but-nonempty title, got empty")
		}
	})
}
