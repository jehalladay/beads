//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestRunnableHybridUnknownSubcommandGuards is the end-to-end tooth for
// beads-3l5q. Four parent groups are "Runnable hybrids": they HAVE subcommands
// AND their own RunE (dep, human, metrics, migrate). The shared
// attachUnknownSubcommandGuards deliberately skips Runnable commands
// (subcmd_guard.go: `if !cmd.HasSubCommands() || cmd.Runnable() { return }`), so
// before this fix a typo'd subcommand fell through into the parent's own RunE
// and printed help/status with EXIT 0 — a silent false-success (a typo'd
// `migrate shema` made the user believe a schema migration ran; nothing did).
//
// The fix gives each hybrid RunE its own unknown-positional guard mirroring the
// pure-group guard: a leftover positional (cobra dispatches valid subcommands to
// the child before the parent RunE runs) errors with a non-zero exit, and under
// --json emits a structured {error,schema_version} object on stdout.
//
// This cannot be a pure test: the --json leg depends on the root
// PersistentPreRunE setting the jsonOutput global from --json before the RunE
// fires, so the teeth run real bd as a subprocess.
func TestRunnableHybridUnknownSubcommandGuards(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// One bogus positional per hybrid. None of these is a valid subcommand of
	// its group, and none is a legit direct-invocation form (dep's only direct
	// form needs --blocks; human/metrics/migrate take no positional).
	hybrids := []struct {
		group string
		typo  string
	}{
		{"dep", "totallybogus"},
		{"human", "lst"},      // typo of list
		{"metrics", "of"},     // typo of off
		{"migrate", "shema"},  // typo of schema — worst footgun
	}

	for _, h := range hybrids {
		h := h
		t.Run(h.group+"/plain", func(t *testing.T) {
			cmd := exec.Command(bd, h.group, h.typo)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			if err == nil {
				t.Fatalf("`%s %s` unexpectedly succeeded (exit 0) — a typo'd subcommand must error, not silently print help/status\nstdout:\n%s\nstderr:\n%s",
					h.group, h.typo, stdout.String(), stderr.String())
			}
			combined := stdout.String() + stderr.String()
			if !strings.Contains(combined, "unknown") || !strings.Contains(combined, h.typo) {
				t.Errorf("`%s %s` error should name the unknown subcommand; got stdout=%q stderr=%q",
					h.group, h.typo, stdout.String(), stderr.String())
			}
		})

		t.Run(h.group+"/json", func(t *testing.T) {
			cmd := exec.Command(bd, h.group, h.typo, "--json")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			if err == nil {
				t.Fatalf("`%s %s --json` unexpectedly succeeded (exit 0)\nstdout:\n%s", h.group, h.typo, stdout.String())
			}
			out := strings.TrimSpace(stdout.String())
			if out == "" {
				t.Fatalf("stdout EMPTY on `%s %s --json` — error must be a JSON object on stdout, not plaintext on stderr\nstderr:\n%s",
					h.group, h.typo, stderr.String())
			}
			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout is not a JSON object on `%s %s --json`: %v\nstdout:\n%s", h.group, h.typo, jerr, out)
			}
			msg, ok := obj["error"].(string)
			if !ok {
				if data, dok := obj["data"].(map[string]interface{}); dok {
					msg, ok = data["error"].(string)
				}
			}
			if !ok || msg == "" {
				t.Fatalf("expected a non-empty \"error\" field in `%s %s --json` stdout, got: %s", h.group, h.typo, out)
			}
			if !strings.Contains(msg, "unknown") || !strings.Contains(msg, h.typo) {
				t.Errorf("error message should name the unknown subcommand; got: %q", msg)
			}
		})
	}
}

// TestRunnableHybridLegitInvocationsUnaffected is the parity negative: the bare
// group (help/status) and each hybrid's real direct-invocation form must keep
// working — the guard only rejects a leftover UNKNOWN positional.
func TestRunnableHybridLegitInvocationsUnaffected(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// Bare group invocations: exit 0 (help/status), no error.
	for _, group := range []string{"dep", "human", "metrics", "migrate"} {
		group := group
		t.Run("bare/"+group, func(t *testing.T) {
			cmd := exec.Command(bd, group)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			if err != nil {
				t.Fatalf("bare `%s` must succeed (help/status), got err=%v\nstdout:\n%s\nstderr:\n%s",
					group, err, stdout.String(), stderr.String())
			}
		})
	}

	// A valid subcommand still dispatches to the child (metrics status subcmds
	// are side-effect-light): `metrics example` prints examples, exit 0.
	t.Run("valid-subcommand-dispatches", func(t *testing.T) {
		cmd := exec.Command(bd, "metrics", "example")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("`metrics example` (valid subcommand) must succeed, got err=%v\nstdout:\n%s\nstderr:\n%s",
				err, stdout.String(), stderr.String())
		}
	})
}
