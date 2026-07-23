//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestAuditRecordStdinFieldFlagConflict_7ymzp pins the beads-7ymzp fix: `bd audit
// record` takes its entry from at most ONE source. Explicit --stdin combined with
// any field flag (--kind/--model/--prompt/--response/--issue-id/--tool-name/
// --exit-code/--error) used to silently drop the flags — only the stdin JSON was
// stored, rc0, no warning (dz1t8-class silent-input-drop). The sibling `bd vc
// commit` already rejects --stdin + -m/--message (vc.go:230), and dz1t8 made
// comment/note reject positional + --stdin/--file; audit record was the odd one
// out. The auto-detect leg (piped stdin AND no field flags) must be unaffected.
//
// Mutation check: remove the `if auditRecordStdin { ... Changed(name) ... }` guard
// in audit.go and the *_rejected subtests go RED (the command succeeds rc0 and the
// flag is silently dropped in favor of the stdin JSON).
func TestAuditRecordStdinFieldFlagConflict_7ymzp(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ar")

	// runRecord runs `bd audit record ...args` with the given stdin, returning
	// combined output + whether it exited non-zero (a rejection).
	runRecord := func(t *testing.T, stdin string, args ...string) (string, bool) {
		t.Helper()
		full := append([]string{"audit", "record"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		return string(out), err != nil
	}

	// Each field flag combined with an explicit --stdin must be rejected — a field
	// flag paired with --stdin is a conflicting-input error, not a silent drop.
	fieldFlags := [][]string{
		{"--kind", "fromflag"},
		{"--model", "fromflag-model"},
		{"--prompt", "fromflag-prompt"},
		{"--response", "fromflag-response"},
		{"--issue-id", "ar-1"},
		{"--tool-name", "fromflag-tool"},
		{"--exit-code", "0"},
		{"--error", "fromflag-error"},
	}
	for _, ff := range fieldFlags {
		ff := ff
		t.Run("stdin_plus"+strings.ReplaceAll(ff[0], "-", "_")+"_rejected", func(t *testing.T) {
			args := append([]string{"--stdin"}, ff...)
			out, failed := runRecord(t, `{"kind":"fromstdin","model":"m1"}`, args...)
			if !failed {
				t.Fatalf("bd audit record --stdin %s must be rejected (conflicting input sources), got success:\n%s", strings.Join(ff, " "), out)
			}
			if !strings.Contains(out, "cannot specify both --stdin and field flags") {
				t.Errorf("expected a 'cannot specify both --stdin and field flags' error, got:\n%s", out)
			}
		})
	}

	// Regression: --stdin alone (no field flags) still records from the JSON.
	t.Run("stdin_only_ok", func(t *testing.T) {
		if out, failed := runRecord(t, `{"kind":"tool_call","model":"m1"}`, "--stdin"); failed {
			t.Fatalf("bd audit record --stdin (alone) must succeed, got failure:\n%s", out)
		}
	})

	// Regression: field flags alone (no --stdin) still record from the flags.
	t.Run("flags_only_ok", func(t *testing.T) {
		if out, failed := runRecord(t, "", "--kind", "tool_call", "--model", "m1"); failed {
			t.Fatalf("bd audit record --kind ... (alone) must succeed, got failure:\n%s", out)
		}
	})
}
