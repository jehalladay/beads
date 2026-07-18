//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// bdLintExpectFail runs "bd lint ..." expecting a nonzero exit and returns the
// combined output.
func bdLintExpectFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"lint"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd lint %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdLintExpectOK runs "bd lint ..." expecting a zero exit and returns the
// combined output.
func bdLintExpectOK(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"lint"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected bd lint %s to succeed, but failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestEmbeddedLintPartialExitCode covers beads-p3y5: `bd lint <id...>` accepts
// multiple ids and its args loop `continue`s past unresolvable ones, printing
// "Issue not found: <id>" to stderr, then every terminal return is rc=0. So
// `bd lint <valid> <ghost>` silently returned success while a requested id was
// missing — inconsistent with the single-command intent of a lint gate and
// with the rest of the partial-failure exit-code class (label/show/dep list/
// todo done/mol burn). It must exit non-zero when any id fails to resolve,
// while still linting the issues that were found (partial lint preserved).
func TestEmbeddedLintPartialExitCode(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "p3")

	// Create lint-CLEAN tasks (they carry the required "## Acceptance Criteria"
	// section) so a passing lint returns rc=0. This isolates the id-resolution
	// failure exit code from the by-design "lint gate fails on warnings" exit —
	// the defect is that a clean lint masks a missing id and returns rc=0.
	const clean = "## Acceptance Criteria\n- works"
	a := bdCreate(t, bd, dir, "lint valid a", "--type", "task", "-d", clean)
	b := bdCreate(t, bd, dir, "lint valid b", "--type", "task", "-d", clean)

	// No regression: single valid id and all-valid multi both exit zero.
	t.Run("single_valid_exits_zero", func(t *testing.T) {
		bdLintExpectOK(t, bd, dir, a.ID)
	})
	t.Run("multi_all_valid_exits_zero", func(t *testing.T) {
		bdLintExpectOK(t, bd, dir, a.ID, b.ID)
	})

	// The bug: all-bogus historically exited zero (unconditional SilentExit).
	t.Run("multi_all_bogus_exits_nonzero", func(t *testing.T) {
		bdLintExpectFail(t, bd, dir, "p3-ghost-a", "p3-ghost-b")
	})

	// The bug: valid + ghost must exit non-zero, but the valid issue is still
	// linted (partial lint preserved).
	t.Run("multi_partial_exits_nonzero_still_lints_valid", func(t *testing.T) {
		out := bdLintExpectFail(t, bd, dir, a.ID, "p3-ghost")
		if !strings.Contains(out, "p3-ghost") {
			t.Errorf("expected the missing id p3-ghost to be reported, got:\n%s", out)
		}
	})

	// The --json path: valid + ghost must exit non-zero, and stdout must still
	// carry a parseable lint object (partial results preserved).
	t.Run("multi_partial_json_exits_nonzero_still_emits_object", func(t *testing.T) {
		cmd := exec.Command(bd, "lint", a.ID, "p3-ghost", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Fatalf("expected bd lint %s p3-ghost --json to fail, but succeeded\nstdout:\n%s", a.ID, stdout.String())
		}
		s := strings.TrimSpace(stdout.String())
		start := strings.IndexByte(s, '{')
		if start < 0 {
			t.Fatalf("expected a JSON object on stdout carrying lint results, got:\n%s", s)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(s[start:]), &obj); jerr != nil {
			t.Fatalf("stdout is not a parseable JSON object: %v\n%s", jerr, s)
		}
		if _, ok := obj["total"]; !ok {
			t.Errorf("expected the lint JSON object (with a 'total' field) on stdout, got: %v", obj)
		}
	})
}
