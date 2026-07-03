// Package buildpolicy holds executable guards over the beads build/test
// contract. These are plain unit tests (no Dolt, no cgo) that read the
// repo's own build scripts and agent docs and assert the contract they must
// keep. They exist so a tier established once (the -race data-race detector
// tier, the agentic-tdd builder workflow) cannot silently regress.
//
// beads-r06.8 ([P1][C1] agentic-tdd adoption: tests-first/shell-first +
// -race tiers). Written test-first per the very workflow it enforces: this
// file FAILS before the make target / test.sh flag / docs land, and passes
// after.
package buildpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from this test file to the module root (the dir holding
// go.mod). Tests run with the package dir as CWD, so the root is two levels up
// (tests/buildpolicy -> tests -> root), but we locate it robustly rather than
// hard-coding the depth.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod above %s", dir)
		}
		dir = parent
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestPRCoreRunsRaceDetector guards the CI-side race tier: the required fast
// PR contract (scripts/ci/pr-core.sh, wired as `make ci-pr-core`) must run the
// Go race detector. Dropping -race here would silently disable data-race
// detection on every PR.
func TestPRCoreRunsRaceDetector(t *testing.T) {
	root := repoRoot(t)
	prCore := readFile(t, root, "scripts/ci/pr-core.sh")
	if !strings.Contains(prCore, "-race") {
		t.Error("scripts/ci/pr-core.sh must run `go test -race` (race detector tier); -race not found")
	}
}

// TestLocalRaceTierExists guards the local-developer race tier. r06.8 requires
// a -race test tier that a builder can run locally (not only in CI). It is
// wired as `make test-race` delegating to `scripts/test.sh --race`.
func TestLocalRaceTierExists(t *testing.T) {
	root := repoRoot(t)

	makefile := readFile(t, root, "Makefile")
	if !strings.Contains(makefile, "test-race:") {
		t.Error("Makefile must define a `test-race:` target (local -race tier for agentic-tdd)")
	}

	testSh := readFile(t, root, "scripts/test.sh")
	// The runner must accept a --race/-race flag and thread -race into the go
	// test command.
	if !strings.Contains(testSh, "-race") {
		t.Error("scripts/test.sh must support running the race detector (-race)")
	}
	if !strings.Contains(testSh, "--race") {
		t.Error("scripts/test.sh must accept a --race flag so `make test-race` can request the tier")
	}
}

// TestMakeHelpDocumentsRaceTier ensures the race tier is discoverable in
// `make help` so builders actually find it. beads' help target is a set of
// hand-maintained `@echo "  make <target> ..."` lines, so we assert an echo
// line mentions `make test-race`.
func TestMakeHelpDocumentsRaceTier(t *testing.T) {
	root := repoRoot(t)
	makefile := readFile(t, root, "Makefile")
	if !strings.Contains(makefile, "make test-race") {
		t.Error("`make help` must list `make test-race` (an @echo line) so builders discover the race tier")
	}
}

// TestAgenticTDDDocumented guards the documented + enforced builder workflow:
// tests-first, shell-first, and the -race tier, in the authoritative agent
// instructions. This is the "documented + enforced" half of the r06.8
// acceptance criteria.
func TestAgenticTDDDocumented(t *testing.T) {
	root := repoRoot(t)
	// Case-insensitive: the concepts appear both as prose headings
	// ("Tests-first", "Shell-first") and inline, so we match on the token.
	instructions := strings.ToLower(readFile(t, root, "AGENT_INSTRUCTIONS.md"))

	required := []string{
		"agentic-tdd", // names the workflow
		"tests-first", // the fail-first discipline
		"shell-first", // exercise at the shell/CLI level first
		"make test-race",
	}
	for _, req := range required {
		if !strings.Contains(instructions, req) {
			t.Errorf("AGENT_INSTRUCTIONS.md must document the agentic-tdd workflow; missing %q", req)
		}
	}
}
