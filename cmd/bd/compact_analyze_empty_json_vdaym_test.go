//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// beads-vdaym: `bd admin compact --analyze --json` on an empty candidate set
// emitted the bare literal `null` for the "candidates" key (nil slice → json
// null) instead of `[]`, breaking agent consumers that iterate the array. This
// is a direct member of the tamf/5fv3/nqv0/jbwv nil-slice->[] family: the
// populated path already emits an array and total_candidates:0 was already
// correct — only the zero-case container type leaked. Fix = non-nil empty
// slice init at compact.go (`candidates := []Candidate{}`).
//
// End-to-end through the ACTUAL emit site (a fresh db has zero closed issues →
// zero compaction candidates → the empty branch). NOTE: the analyze/--json
// path lives under `bd admin compact` (compact.go compactCmd, registered as a
// subcommand of adminCmd) — the top-level `bd compact` (compact_dolt.go) does
// NOT accept --analyze. This test therefore also exercises the 238gz premise
// (analyze is an admin-compact flag).
func TestEmbeddedCompactAnalyzeEmptyMarshalsArrayNotNull_vdaym(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "vd")

	// Fresh db: no closed issues → GetTier1Candidates returns empty → the
	// --analyze --json path emits the empty candidates container.
	cmd := exec.Command(bd, "admin", "compact", "--analyze", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd admin compact --analyze --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()

	// The literal-null leak is the bug: assert the raw text does not carry it.
	if strings.Contains(out, `"candidates":null`) || strings.Contains(out, `"candidates": null`) {
		t.Errorf("empty compact --analyze --json leaked candidates:null (nil slice):\n%s", out)
	}

	// Structural assertion: candidates must decode to a non-nil (empty) array.
	var parsed struct {
		Candidates []json.RawMessage `json:"candidates"`
		Summary    struct {
			TotalCandidates int `json:"total_candidates"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if parsed.Candidates == nil {
		t.Errorf("candidates decoded to nil (JSON null); want non-nil empty array [], got:\n%s", out)
	}
	if len(parsed.Candidates) != 0 {
		t.Errorf("expected 0 candidates on a fresh db, got %d:\n%s", len(parsed.Candidates), out)
	}
	if parsed.Summary.TotalCandidates != 0 {
		t.Errorf("expected total_candidates 0, got %d", parsed.Summary.TotalCandidates)
	}
}

// beads-238gz: the `bd admin compact` help Examples pointed users at `bd
// compact <flag>` for admin-compact-only flags (--dolt/--analyze/--apply/
// --auto/--stats all live on compactCmd, compact.go), but the top-level `bd
// compact` (compact_dolt.go) rejects them as unknown flags. Assert the help
// text uses the correct `bd admin compact` form.
func TestCompactHelpExamplesUseAdminCompact_238gz(t *testing.T) {
	t.Parallel()

	help := compactCmd.Long
	for _, want := range []string{
		"bd admin compact --analyze --json",
		"bd admin compact --dolt",
		"bd admin compact --auto --dry-run",
		"bd admin compact --stats",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("compact help missing corrected example %q", want)
		}
	}
	// The stale bare `bd compact --<adminflag>` forms must be gone.
	for _, bad := range []string{
		"bd compact --analyze",
		"bd compact --dolt",
		"bd compact --auto",
		"bd compact --stats",
	} {
		if strings.Contains(help, bad) {
			t.Errorf("compact help still contains stale example %q (wrong command)", bad)
		}
	}
}
