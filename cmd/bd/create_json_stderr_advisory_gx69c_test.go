//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestCreateJSONStderrAdvisory_gx69c is the beads-gx69c teeth (8lqh json-error
// contract, Direction-2 interleaved-stderr class; sibling of beads-8zed6).
//
// runCreate emits the created-issue JSON envelope on stdout under --json
// (create.go:827), but two pure human-hint advisories wrote to os.Stderr
// guarded only by `!silent && !debug.IsQuiet()` (NOT !jsonOutput):
//
//   - create.go:134 test-issue-in-production warning (isTestIssue(title) &&
//     >=5 existing issues)
//   - create.go:276 defer-date-in-past warning (--defer with a past date)
//
// --json does not set silent/quiet (main.go binds quietFlag to --quiet only),
// so under `bd create ... --json 2>&1 | jq` these plaintext lines interleave
// with the JSON object -> parse failure. Both hints duplicate payload data
// (defer date -> defer_until; test-issue -> title). The fix gates both under
// && !jsonOutput.
//
// Mutation-verify: drop `&& !jsonOutput` from either guard and the matching
// subtest goes RED (the leading `!`/`⚠` advisory trips json.Unmarshal on the
// combined 2>&1 stream).
func TestCreateJSONStderrAdvisory_gx69c(t *testing.T) {
	bd := buildEmbeddedBD(t)

	// (1) --defer with a PAST date + --json: the 2>&1 stream must parse as a
	//     single JSON object. Positive control below proves the branch is
	//     reachable, so this can't false-green.
	t.Run("defer_past_json_is_pure", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gxa")
		stdout, stderr := runCreateGx69c(t, bd, dir,
			"create", "defer-in-past json probe", "--type", "task",
			"--defer", "2020-01-01", "--json")
		assertCombinedIsJSONObject(t, "defer-in-past", stdout, stderr)
	})

	// (2) Positive control: WITHOUT --json a past --defer still prints the
	//     advisory to stderr. Proves the defer-past branch is genuinely
	//     reachable with this setup — otherwise subtest (1) would pass vacuously.
	t.Run("defer_past_plain_keeps_advisory", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gxb")
		stdout, stderr := runCreateGx69c(t, bd, dir,
			"create", "defer-in-past plain probe", "--type", "task",
			"--defer", "2020-01-01")
		combined := stdout + stderr
		if !strings.Contains(combined, "is in the past") {
			t.Errorf("beads-gx69c: plain (non-json) create with a past --defer must still "+
				"print the past-defer advisory to stderr (fix suppresses only under --json); "+
				"stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	// (3) test-issue-in-production + --json: needs >=5 existing issues before
	//     the warning fires (create.go:135 stats.TotalIssues >= 5). Seed 5
	//     non-test issues, then create a "test-..." titled issue with --json;
	//     the 2>&1 stream must parse as a single JSON object.
	t.Run("test_issue_prod_json_is_pure", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gxc")
		for i := 0; i < 5; i++ {
			_, _ = runCreateGx69c(t, bd, dir,
				"create", fmt.Sprintf("seed issue %d", i), "--type", "task")
		}
		stdout, stderr := runCreateGx69c(t, bd, dir,
			"create", "test-pollution json probe", "--type", "task", "--json")
		assertCombinedIsJSONObject(t, "test-issue-in-prod", stdout, stderr)
	})

	// (4) Positive control: WITHOUT --json the test-issue-in-prod advisory
	//     fires (proves subtest (3) is non-vacuous — the seeded >=5 threshold
	//     is met and the title matches isTestIssue).
	t.Run("test_issue_prod_plain_keeps_advisory", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gxd")
		for i := 0; i < 5; i++ {
			_, _ = runCreateGx69c(t, bd, dir,
				"create", fmt.Sprintf("seed issue %d", i), "--type", "task")
		}
		stdout, stderr := runCreateGx69c(t, bd, dir,
			"create", "test-pollution plain probe", "--type", "task")
		combined := stdout + stderr
		if !strings.Contains(combined, "test issue in production") {
			t.Errorf("beads-gx69c: plain (non-json) create of a test-titled issue with >=5 "+
				"existing issues must still print the test-issue-in-prod advisory to stderr "+
				"(fix suppresses only under --json); stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})
}

// assertCombinedIsJSONObject asserts the combined (2>&1) create stream parses as
// a single JSON object carrying the created-issue 'id' — i.e. no plaintext
// advisory leaked ahead of / after the JSON envelope.
func assertCombinedIsJSONObject(t *testing.T, label, stdout, stderr string) {
	t.Helper()
	combined := strings.TrimSpace(stdout + stderr)
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(combined), &obj); jerr != nil {
		t.Fatalf("beads-gx69c: `bd create --json` 2>&1 is NOT a single JSON object "+
			"(%s advisory leaked plaintext to stderr): %v\ncombined:\n%s", label, jerr, combined)
	}
	if _, ok := obj["id"]; !ok {
		t.Errorf("beads-gx69c (%s): parsed JSON lacks the created-issue 'id' field; got: %s", label, combined)
	}
}

// runCreateGx69c runs a bd subprocess capturing stdout and stderr SEPARATELY
// (the defect is a stderr write, so the teeth must see stderr — a stdout-only
// helper would false-green). No flock retry loop is needed here because each
// subtest uses its own bdInit workspace and the embedded harness serializes.
func runCreateGx69c(t *testing.T, bd, dir string, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	so, se, err := runCommandBuffers(t, cmd)
	stdout, stderr = so.String(), se.String()
	if err != nil {
		t.Fatalf("beads-gx69c: `bd %s` failed: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout, stderr
}
