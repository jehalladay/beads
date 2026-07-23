//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// bdPurge runs "bd purge" with the given args and returns stdout.
func bdPurge(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"purge"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd purge %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdPurgeFail runs "bd purge" expecting failure.
func bdPurgeFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"purge"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd purge %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// createAndCloseEphemeral creates an ephemeral issue and closes it.
func createAndCloseEphemeral(t *testing.T, bd, dir, title string) string {
	t.Helper()
	issue := bdCreate(t, bd, dir, title, "--ephemeral")
	cmd := exec.Command(bd, "close", issue.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("close %s failed: %v\n%s", issue.ID, err, out)
	}
	return issue.ID
}

func TestEmbeddedPurge(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// ===== Nothing to Purge =====

	t.Run("purge_nothing", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pn")
		// No ephemeral issues — preview should show nothing
		cmd := exec.Command(bd, "purge")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, _ := cmd.CombinedOutput()
		_ = out // Should not crash, regardless of exit code
	})

	// ===== Preview (no flags) =====

	t.Run("purge_preview", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pp")
		createAndCloseEphemeral(t, bd, dir, "Purge preview 1")
		createAndCloseEphemeral(t, bd, dir, "Purge preview 2")

		// Without --force, should show preview and fail
		bdPurgeFail(t, bd, dir)
	})

	// ===== Dry Run =====

	t.Run("purge_dry_run", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pd")
		createAndCloseEphemeral(t, bd, dir, "Purge dry-run 1")

		out := bdPurge(t, bd, dir, "--dry-run")
		if len(strings.TrimSpace(out)) == 0 {
			t.Error("expected non-empty dry-run output")
		}
	})

	// ===== Force =====

	t.Run("purge_force", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pf")
		createAndCloseEphemeral(t, bd, dir, "Purge force 1")
		createAndCloseEphemeral(t, bd, dir, "Purge force 2")

		out := bdPurge(t, bd, dir, "--force")
		if !strings.Contains(out, "Purged") {
			t.Errorf("expected 'Purged' in output: %s", out)
		}
	})

	// ===== Older Than =====

	t.Run("purge_older_than", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "po")
		createAndCloseEphemeral(t, bd, dir, "Purge older-than 1")

		// --older-than 1d means closed more than 1 day ago
		out := bdPurge(t, bd, dir, "--older-than", "1d", "--force")
		_ = out // Should succeed (may find 0 matches since just closed)
	})

	// ===== Pattern =====

	t.Run("purge_pattern", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pt")
		createAndCloseEphemeral(t, bd, dir, "Purge pattern test")

		// Pattern matching — use prefix wildcard
		out := bdPurge(t, bd, dir, "--pattern", "pt-*", "--force")
		_ = out // Should succeed or find no matches
	})

	// beads-cpss: a MALFORMED glob (e.g. an unclosed bracket) must fail loud,
	// not silently match nothing. filepath.Match returns ErrBadPattern for
	// "[invalid"; purge previously discarded that error (`ok, _ :=`) so a typo'd
	// --pattern reported "No beads to purge" with rc=0 — a footgun on a
	// destructive command that masks the user's mistake.
	t.Run("purge_malformed_pattern_fails_loud", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pm")
		createAndCloseEphemeral(t, bd, dir, "Purge bad pattern test")

		out := bdPurgeFail(t, bd, dir, "--pattern", "[invalid", "--dry-run")
		if !strings.Contains(strings.ToLower(out), "pattern") {
			t.Errorf("expected a malformed-pattern error mentioning 'pattern', got: %q", out)
		}
	})

	// beads-hbn3: the require-filter guard (bd prune with neither --older-than nor
	// --pattern; requireFilter is true for prune, false for purge) previously
	// called HandleErrorWithHint, which writes plain text to STDERR even under
	// --json → a --json consumer got no JSON error object on stdout (8lqh
	// json-error-contract, EMPTY-stdout shape). The fix uses
	// HandleErrorWithHintRespectJSON so the error object lands on STDOUT. The
	// guard is in the shared runPurgeOrPrune, so purge's confirm-required guard
	// (the :232 site) is fixed by the same swap.
	t.Run("prune_requirefilter_json_error_on_stdout", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pj")
		cmd := exec.Command(bd, "prune", "--json") // no --older-than / --pattern → require-filter guard
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("expected prune --json (no filter) to fail; stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &obj); jerr != nil {
			t.Errorf("prune --json guard error should emit a JSON object on STDOUT, got stdout=%q (stderr=%q): %v", stdout.String(), stderr.String(), jerr)
		} else if _, ok := obj["error"]; !ok {
			t.Errorf("JSON error object missing 'error' key: %v", obj)
		}
	})
}

// TestPurgePruneJSONPreviewNoPreamble pins the beads-nx26e fix: the residual
// leg of beads-hbn3. hbn3 routed the preview *error object* through RespectJSON
// (JSON error on stdout under --json), but the human-facing "Found N … to <cmd>"
// (+ "Skipping N pinned") preamble at the confirm-required (!force) site stayed
// unguarded — so under --json it printed non-JSON text on STDOUT *before* the
// JSON error envelope, breaking jq/parsers. This is distinct from the hbn3
// require-filter guard test above (that path has no matches, so it never reaches
// the preamble): here there ARE closed ephemeral beads, so preview finds
// matches and would emit the leaking preamble.
//
// Mutation check: drop the `if !jsonOutput` guard around the preamble in
// purge.go and both subtests go RED (stdout no longer parses as a single JSON
// object because it is preceded by the "Found N …" line).
func TestPurgePruneJSONPreviewNoPreamble(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// assertSingleJSONObject runs `bd <verb> --json <extra...>` expecting the
	// preview (confirm-required) error path, and asserts stdout is exactly one
	// parseable JSON object carrying the "error" key — with no preamble leaking
	// ahead of it.
	assertSingleJSONObject := func(t *testing.T, dir, verb string, extra ...string) {
		t.Helper()
		args := append([]string{verb, "--json"}, extra...)
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("expected %s --json preview to fail (confirm required); stdout=%q stderr=%q", verb, stdout.String(), stderr.String())
		}
		trimmed := strings.TrimSpace(stdout.String())
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(trimmed), &obj); jerr != nil {
			t.Fatalf("%s --json preview stdout must be a single JSON object (no 'Found N …' preamble), got stdout=%q (stderr=%q): %v", verb, stdout.String(), stderr.String(), jerr)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("%s --json preview JSON object missing 'error' key: %v", verb, obj)
		}
	}

	t.Run("purge_json_preview_no_preamble", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "jp")
		createAndCloseEphemeral(t, bd, dir, "nx26e purge preview 1")
		createAndCloseEphemeral(t, bd, dir, "nx26e purge preview 2")
		assertSingleJSONObject(t, dir, "purge")
	})

	t.Run("prune_json_preview_no_preamble", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "jr")
		// prune targets closed NON-ephemeral beads (unlike purge), so create
		// regular beads and close them to give prune matches.
		for _, title := range []string{"nx26e prune preview 1", "nx26e prune preview 2"} {
			issue := bdCreate(t, bd, dir, title)
			cmd := exec.Command(bd, "close", issue.ID)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("close %s failed: %v\n%s", issue.ID, err, out)
			}
		}
		// prune requires a filter; --pattern selects the matches so we reach the
		// same confirm-required preamble site (shared runPurgeOrPrune).
		assertSingleJSONObject(t, dir, "prune", "--pattern", "jr-*")
	})
}

// TestEmbeddedPurgeConcurrent exercises purge --dry-run concurrently.
func TestEmbeddedPurgeConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "px")

	createAndCloseEphemeral(t, bd, dir, "Purge concurrent 1")
	createAndCloseEphemeral(t, bd, dir, "Purge concurrent 2")

	const numWorkers = 8
	type workerResult struct {
		worker int
		err    error
	}
	results := make([]workerResult, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			r := workerResult{worker: worker}
			cmd := exec.Command(bd, "purge", "--dry-run")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("purge --dry-run (worker %d): %v\n%s", worker, err, out)
			}
			results[worker] = r
		}(w)
	}
	wg.Wait()
	for _, r := range results {
		if r.err != nil && !strings.Contains(r.err.Error(), "one writer at a time") {
			t.Errorf("worker %d failed: %v", r.worker, r.err)
		}
	}
}
