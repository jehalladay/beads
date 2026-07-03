package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// release-audit.sh is the "L10 release-audit gate" (beads-nsa, HALF 2 of
// beads-r06.12). It automates the 6-criterion checklist every
// release-gates/*.md file in this repo already asserts BY HAND into a scripted,
// CI-enforceable audit:
//
//	1. Review PASS present            (recorded — bd show marker)
//	2. Acceptance criteria met        (recorded — bd show marker)
//	3. Tests pass on release branch   (mechanical — runs --test-cmd)
//	4. No HIGH-sev findings open       (recorded — bd show marker absence)
//	5. Final branch clean              (mechanical — git status --porcelain)
//	6. Branch diverges cleanly         (mechanical — cherry-pick onto base)
//
// The script emits a machine-readable PASS/FAIL summary and generates a
// release-gates/<bead>-gate.md stub. These tests exec the script directly
// against a throwaway git repo + a fake `bd`, so they need no Dolt, no network,
// and no Go coverage tooling — the same pattern as coverage_ratchet_test.go.

// runReleaseAudit execs scripts/ci/release-audit.sh from within `workdir` (a
// real git repo under test) with a fake `bd` on PATH, returning combined output
// and exit code.
func runReleaseAudit(t *testing.T, workdir, fakeBinDir string, args ...string) (string, int) {
	t.Helper()
	repo := sourceRepoRoot(t)
	full := append([]string{filepath.Join(repo, "scripts", "ci", "release-audit.sh")}, args...)
	cmd := exec.Command("bash", full...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "PATH="+fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("failed to run release-audit.sh %v: %v\n%s", args, err, out)
		}
	}
	return string(out), code
}

// git runs a git command inside dir and fails the test on error.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// newAuditRepo builds a throwaway git repo with:
//   - a `main` branch (the base) carrying base.txt
//   - a clean feature branch that adds feature.txt (diverges cleanly from main)
//
// It returns the repo path. Callers can then create dirty/conflicting states.
func newAuditRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitCmd(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-q", "-m", "base")
	// Clean feature branch: a new file, no overlap with base → cherry-picks clean.
	gitCmd(t, repo, "checkout", "-q", "-b", "release/feat")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-q", "-m", "feature work")
	return repo
}

// writeFakeAuditBD installs a fake `bd` whose `bd show <bead>` prints the given
// body. The script parses this body for the recorded-criteria markers (1/2/4).
func writeFakeAuditBD(t *testing.T, showBody string) string {
	t.Helper()
	bin := t.TempDir()
	// The fake echoes a fixed body for `bd show`, and is a no-op for anything
	// else. Using a heredoc keeps the body verbatim (markers included).
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"show\" ]; then\n" +
		"cat <<'BDEOF'\n" +
		showBody + "\n" +
		"BDEOF\n" +
		"exit 0\n" +
		"fi\n" +
		"exit 0\n"
	p := filepath.Join(bin, "bd")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// allGoodShowBody is a bd-show body that satisfies criteria 1, 2, and 4.
const allGoodShowBody = `be-x · Some feature   [CLOSED]
NOTES
  Review: PASS (reviewer-1 concur reviewer-2).
  Acceptance criteria met — all 5 ACs walked.
  No HIGH-severity findings open (one LOW advisory, non-blocking).
`

// TestReleaseAuditAllCriteriaPass is the happy path: a clean feature branch, a
// passing test command, and a bead recording review PASS / AC met / no open
// HIGH findings → overall PASS (exit 0), and a gate stub is generated.
func TestReleaseAuditAllCriteriaPass(t *testing.T) {
	repo := newAuditRepo(t)
	bin := writeFakeAuditBD(t, allGoodShowBody)

	out, code := runReleaseAudit(t, repo, bin,
		"--bead", "be-x",
		"--branch", "release/feat",
		"--base", "main",
		"--test-cmd", "true",
	)
	if code != 0 {
		t.Fatalf("expected overall PASS (exit 0), got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "PASS") {
		t.Fatalf("expected a PASS verdict in output:\n%s", out)
	}
	// The gate stub must have been generated under release-gates/.
	stub := filepath.Join(repo, "release-gates", "be-x-gate.md")
	if _, err := os.Stat(stub); err != nil {
		t.Fatalf("expected generated gate stub at %s: %v\noutput:\n%s", stub, err, out)
	}
}

// TestReleaseAuditJSONVerdict pins the machine-readable contract: --json emits
// a top-level verdict and a per-criterion breakdown.
func TestReleaseAuditJSONVerdict(t *testing.T) {
	repo := newAuditRepo(t)
	bin := writeFakeAuditBD(t, allGoodShowBody)

	out, code := runReleaseAudit(t, repo, bin,
		"--bead", "be-x", "--branch", "release/feat", "--base", "main",
		"--test-cmd", "true", "--json",
	)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d:\n%s", code, out)
	}
	for _, want := range []string{`"verdict"`, `"PASS"`, `"criteria"`, `"bead"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("json output missing %s:\n%s", want, out)
		}
	}
}

// TestReleaseAuditFailsOnTestFailure exercises criterion 3 (mechanical): a
// failing --test-cmd must fail the whole audit.
func TestReleaseAuditFailsOnTestFailure(t *testing.T) {
	repo := newAuditRepo(t)
	bin := writeFakeAuditBD(t, allGoodShowBody)

	out, code := runReleaseAudit(t, repo, bin,
		"--bead", "be-x", "--branch", "release/feat", "--base", "main",
		"--test-cmd", "false",
	)
	if code == 0 {
		t.Fatalf("expected FAIL when the test command fails, got exit 0:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Fatalf("expected a FAIL verdict naming the failure:\n%s", out)
	}
}

// TestReleaseAuditFailsOnDirtyTree exercises criterion 5 (mechanical): an
// uncommitted change in the release branch must fail the audit.
func TestReleaseAuditFailsOnDirtyTree(t *testing.T) {
	repo := newAuditRepo(t)
	bin := writeFakeAuditBD(t, allGoodShowBody)
	// Dirty the working tree.
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("dirty edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runReleaseAudit(t, repo, bin,
		"--bead", "be-x", "--branch", "release/feat", "--base", "main",
		"--test-cmd", "true",
	)
	if code == 0 {
		t.Fatalf("expected FAIL on a dirty tree, got exit 0:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "clean") {
		t.Fatalf("expected the failure to mention the branch is not clean:\n%s", out)
	}
}

// TestReleaseAuditFailsOnConflictingBranch exercises criterion 6 (mechanical):
// a feature branch that edits the same lines as a divergent base must fail to
// cherry-pick cleanly.
func TestReleaseAuditFailsOnConflictingBranch(t *testing.T) {
	repo := newAuditRepo(t)
	bin := writeFakeAuditBD(t, allGoodShowBody)

	// Create a conflicting branch: edit base.txt on a NEW branch off the
	// original base, and advance main to also edit base.txt differently, so a
	// cherry-pick of the branch onto main conflicts.
	gitCmd(t, repo, "checkout", "-q", "main")
	gitCmd(t, repo, "checkout", "-q", "-b", "release/conflict")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("branch change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-q", "-m", "branch edits base")
	// Advance main divergently.
	gitCmd(t, repo, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("main change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-q", "-m", "main diverges")
	gitCmd(t, repo, "checkout", "-q", "release/conflict")

	out, code := runReleaseAudit(t, repo, bin,
		"--bead", "be-x", "--branch", "release/conflict", "--base", "main",
		"--test-cmd", "true",
	)
	if code == 0 {
		t.Fatalf("expected FAIL when the branch does not cherry-pick cleanly, got exit 0:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "conflict") && !strings.Contains(strings.ToLower(out), "diverge") {
		t.Fatalf("expected the failure to mention a merge/cherry-pick conflict:\n%s", out)
	}
	// The audit must not leave the repo mid-cherry-pick.
	if _, err := os.Stat(filepath.Join(repo, ".git", "CHERRY_PICK_HEAD")); err == nil {
		t.Fatalf("release-audit.sh left the repo mid-cherry-pick (CHERRY_PICK_HEAD present)")
	}
}

// TestReleaseAuditFailsOnMissingReviewPass exercises criterion 1 (recorded): a
// bead with no reviewer PASS marker must fail.
func TestReleaseAuditFailsOnMissingReviewPass(t *testing.T) {
	repo := newAuditRepo(t)
	// Body omits the "Review: PASS" marker but keeps 2 and 4 satisfied.
	body := "be-x · feature\nNOTES\n  Acceptance criteria met.\n  No HIGH-severity findings open.\n"
	bin := writeFakeAuditBD(t, body)

	out, code := runReleaseAudit(t, repo, bin,
		"--bead", "be-x", "--branch", "release/feat", "--base", "main",
		"--test-cmd", "true",
	)
	if code == 0 {
		t.Fatalf("expected FAIL when no reviewer PASS is recorded, got exit 0:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "review") {
		t.Fatalf("expected the failure to mention the missing review PASS:\n%s", out)
	}
}

// TestReleaseAuditFailsOnOpenHighFinding exercises criterion 4 (recorded): an
// open HIGH-severity finding recorded on the bead must fail the audit.
func TestReleaseAuditFailsOnOpenHighFinding(t *testing.T) {
	repo := newAuditRepo(t)
	body := "be-x · feature\nNOTES\n  Review: PASS.\n  Acceptance criteria met.\n  HIGH-severity finding OPEN: unbounded load in list path.\n"
	bin := writeFakeAuditBD(t, body)

	out, code := runReleaseAudit(t, repo, bin,
		"--bead", "be-x", "--branch", "release/feat", "--base", "main",
		"--test-cmd", "true",
	)
	if code == 0 {
		t.Fatalf("expected FAIL on an open HIGH-severity finding, got exit 0:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "high") {
		t.Fatalf("expected the failure to mention the open HIGH finding:\n%s", out)
	}
}

// TestReleaseAuditRequiresBead ensures the gate fails closed when its required
// input (the feature/review bead) is missing.
func TestReleaseAuditRequiresBead(t *testing.T) {
	repo := newAuditRepo(t)
	bin := writeFakeAuditBD(t, allGoodShowBody)

	out, code := runReleaseAudit(t, repo, bin, "--branch", "release/feat", "--base", "main", "--test-cmd", "true")
	if code == 0 {
		t.Fatalf("expected usage failure without --bead, got exit 0:\n%s", out)
	}
}
