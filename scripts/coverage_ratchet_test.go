package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runScript execs a shell script under bash and returns its combined output
// and exit code (non-zero exit is not a test failure — the caller asserts on
// the code).
func runScript(t *testing.T, repo string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("failed to run %v: %v\n%s", args, err, out)
		}
	}
	return string(out), code
}

// coverage-ratchet.sh enforces a ratcheting coverage floor (beads-r06.12, C3):
// CI fails when total coverage regresses below the committed floor, and the
// floor can only be raised (never lowered) via --bump. These tests exec the
// script directly with a temp floor file so they need no Go coverage tooling.

func runCoverageRatchet(t *testing.T, floorFile string, args ...string) (string, int) {
	t.Helper()
	repo := sourceRepoRoot(t)
	full := append([]string{
		filepath.Join(repo, "scripts", "ci", "coverage-ratchet.sh"),
		"--floor-file", floorFile,
	}, args...)
	return runScript(t, repo, full...)
}

func writeFloor(t *testing.T, dir, value string) string {
	t.Helper()
	p := filepath.Join(dir, ".coverage-floor")
	if err := os.WriteFile(p, []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFloor(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestCoverageRatchetPassesWhenAtOrAboveFloor(t *testing.T) {
	floor := writeFloor(t, t.TempDir(), "40.0")

	if out, code := runCoverageRatchet(t, floor, "--total", "40.0"); code != 0 {
		t.Fatalf("expected pass at exactly floor, got exit %d:\n%s", code, out)
	}
	if out, code := runCoverageRatchet(t, floor, "--total", "55.3"); code != 0 {
		t.Fatalf("expected pass above floor, got exit %d:\n%s", code, out)
	}
}

func TestCoverageRatchetFailsBelowFloor(t *testing.T) {
	floor := writeFloor(t, t.TempDir(), "40.0")

	out, code := runCoverageRatchet(t, floor, "--total", "39.9")
	if code == 0 {
		t.Fatalf("expected failure below floor, got exit 0:\n%s", out)
	}
	if !strings.Contains(out, "39.9") || !strings.Contains(out, "40.0") {
		t.Fatalf("failure message should name both measured and floor:\n%s", out)
	}
}

func TestCoverageRatchetBumpRaisesFloor(t *testing.T) {
	dir := t.TempDir()
	floor := writeFloor(t, dir, "40.0")

	out, code := runCoverageRatchet(t, floor, "--total", "47.2", "--bump")
	if code != 0 {
		t.Fatalf("bump above floor should succeed, got exit %d:\n%s", code, out)
	}
	if got := readFloor(t, floor); got != "47.2" {
		t.Fatalf("floor should have been raised to 47.2, got %q", got)
	}
}

func TestCoverageRatchetBumpNeverLowersFloor(t *testing.T) {
	dir := t.TempDir()
	floor := writeFloor(t, dir, "40.0")

	// A --bump run with coverage BELOW the floor must not lower it, and must
	// still fail (a regression is a regression even in bump mode).
	out, code := runCoverageRatchet(t, floor, "--total", "35.0", "--bump")
	if code == 0 {
		t.Fatalf("bump below floor must still fail:\n%s", out)
	}
	if got := readFloor(t, floor); got != "40.0" {
		t.Fatalf("floor must not be lowered by bump; want 40.0 got %q", got)
	}
}

func TestCoverageRatchetBumpEqualKeepsFloor(t *testing.T) {
	dir := t.TempDir()
	floor := writeFloor(t, dir, "40.0")

	out, code := runCoverageRatchet(t, floor, "--total", "40.0", "--bump")
	if code != 0 {
		t.Fatalf("bump at exactly floor should pass, got exit %d:\n%s", code, out)
	}
	if got := readFloor(t, floor); got != "40.0" {
		t.Fatalf("floor should be unchanged at 40.0, got %q", got)
	}
}

func TestCoverageRatchetMissingFloorFileFailsClosed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), ".coverage-floor")

	out, code := runCoverageRatchet(t, missing, "--total", "90.0")
	if code == 0 {
		t.Fatalf("a missing floor file must fail closed (not silently pass):\n%s", out)
	}
}

func TestCoverageRatchetReadsProfile(t *testing.T) {
	dir := t.TempDir()
	floor := writeFloor(t, dir, "40.0")

	// Minimal valid Go coverage profile: two statements, one covered → 50%.
	profile := filepath.Join(dir, "cover.out")
	body := "mode: atomic\n" +
		"example.com/p/f.go:1.1,2.2 1 1\n" +
		"example.com/p/f.go:3.3,4.4 1 0\n"
	if err := os.WriteFile(profile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runCoverageRatchet(t, floor, "--profile", profile)
	// 50% >= 40% floor → pass.
	if code != 0 {
		t.Fatalf("expected profile-derived 50%% to clear 40%% floor, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "50.0") {
		t.Fatalf("expected the computed 50.0%% total in output:\n%s", out)
	}
}
