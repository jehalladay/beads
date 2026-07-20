//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoDatabaseJSON_ErrorPathEmitsStdoutObject is the error-contract teeth for
// beads-wg8i: the shared pre-dispatch guard in main.go's PersistentPreRunE
// ("no beads database found") is the single most common failure a user or a
// JSON parser hits, yet it emitted plain text on stderr with EMPTY stdout under
// --json — for EVERY command, including the canonical honored-json commands
// (list/show/count) that the beads-rg0c per-command sweep treated as gold. The
// rg0c sweep only touched per-command RunE error paths (compact/audit/formula/
// config/repo/migrate/restore); this guard fires BEFORE RunE, so it was missed.
//
// jsonOutput is fully resolved earlier in the same PersistentPreRunE (from
// --json/--format/config) before the guard runs, so the fix routes the guard
// through the JSON error contract: a structured {error:...} object on stdout.
//
// The defect lives in the pre-dispatch guard + os.Exit(1), so the teeth run bd
// as a subprocess in a database-free directory and assert stdout is a parseable
// JSON object with a non-empty "error" field. Runs across several commands to
// prove the guard is shared (the whole point of the find).
func TestNoDatabaseJSON_ErrorPathEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)

	// A bare, database-free directory. HOME is pointed here too (via bdEnv) so
	// no ancestor .beads and no home-dir DB is discovered — the guard fires
	// deterministically, server-free, before any RunE.
	dir := t.TempDir()

	// Guard the guard: confirm no .beads exists anywhere that could be found.
	if _, err := os.Stat(filepath.Join(dir, ".beads")); err == nil {
		t.Fatalf("test precondition broken: %s already has a .beads dir", dir)
	}

	for _, name := range []string{"list", "count", "statuses", "types"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(bd, name, "--json")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)

			// Expected to FAIL (no database) — err != nil is required.
			if err == nil {
				t.Fatalf("`%s --json` unexpectedly succeeded in a db-free dir\nstdout:\n%s", name, stdout.String())
			}

			out := strings.TrimSpace(stdout.String())
			if out == "" {
				t.Fatalf("stdout is EMPTY on `%s --json` with no database — the 'no beads database found' guard must emit a JSON object on stdout (plain-text stderr breaks parsers)\nstderr:\n%s", name, stderr.String())
			}

			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout is not a JSON object on `%s --json` with no database: %v\nstdout:\n%s", name, jerr, out)
			}
			msg, ok := obj["error"].(string)
			if !ok {
				if data, dok := obj["data"].(map[string]interface{}); dok {
					msg, ok = data["error"].(string)
				}
			}
			if !ok || msg == "" {
				t.Errorf("expected a non-empty \"error\" field in `%s --json` no-database stdout, got: %s", name, out)
			}
		})
	}
}
