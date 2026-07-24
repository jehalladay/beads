//go:build !windows

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBDForInitPermissionTests returns a bd binary for the permission-repair
// subprocess tests. It delegates to buildBDForInitTests, which (a) honors a
// pre-built binary via BEADS_TEST_BD_BINARY, (b) reuses ../../bd when present,
// and (c) forces CGO_ENABLED=1 (these tests drive `bd init`, which needs an
// embedded-Dolt/CGO build) — all behind a package-wide sync.Once.
//
// Previously this had its own sync.Once + per-suite `go build` that (1) did NOT
// honor BEADS_TEST_BD_BINARY-set prebuilt binaries the way the gate expects,
// (2) omitted CGO_ENABLED=1 (spurious failures when the ambient env is
// CGO_ENABLED=0, e.g. the refinery clone — beads-0xjb), and (3) shelled out a
// redundant full bd build under load, which panicked and pushed the cmd/bd gate
// suite past its timeout (beads-dmpti). Delegating collapses to one shared,
// prebuilt-aware build.
func buildBDForInitPermissionTests(t *testing.T) string {
	t.Helper()
	return buildBDForInitTests(t)
}

// TestInitRepairsPermissiveBeadsDir is the init-path regression test for
// GH#3391: a pre-existing .beads/ directory with permissive bits
// (e.g. 0755 from a permissive umask) must be repaired to 0700 during
// bd init.
//
// The test creates a real git repo with a pre-existing .beads/ at 0755,
// runs bd init, and asserts that permissions are repaired. The init may
// fail later (e.g. no Dolt server), but the permission fix happens early
// enough that the assertion is valid regardless of exit code.
func TestInitRepairsPermissiveBeadsDir(t *testing.T) {
	bdBin := buildBDForInitPermissionTests(t)

	repoDir := newGitRepo(t)

	beadsDir := filepath.Join(repoDir, ".beads")
	if err := os.Mkdir(beadsDir, 0755); err != nil {
		t.Fatalf("failed to create .beads: %v", err)
	}
	if err := os.Chmod(beadsDir, 0755); err != nil {
		t.Fatalf("failed to chmod .beads: %v", err)
	}

	// Verify starting permissions.
	info, err := os.Stat(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got&0007 == 0 {
		t.Fatalf("precondition failed: .beads should have world bits, got %04o", got)
	}

	cmd := exec.Command(bdBin, "init", "--prefix", "bd",
		"--non-interactive", "--skip-hooks", "--skip-agents")
	cmd.Dir = repoDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// We don't check the exit code — init may fail later for reasons
	// unrelated to permissions (no Dolt, etc.). The permission fix runs
	// before database creation.
	_ = cmd.Run()

	// Assert: permissions must be repaired to the same mode bd uses when
	// creating .beads/ itself.
	info, err = os.Stat(beadsDir)
	if err != nil {
		t.Fatalf("Stat(.beads) after init: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0700 {
		t.Errorf(".beads permissions after init = %04o, want 0700", perm)
	}

	// Assert: the fix was announced on stderr.
	if !strings.Contains(stderr.String(), "Fixed .beads permissions to 0700") {
		t.Errorf("expected permission-fix message on stderr, got:\n%s", stderr.String())
	}
}

// TestInitPreservesSecureBeadsDir verifies that bd init does NOT touch a
// .beads/ directory that already has secure permissions (0700).
func TestInitPreservesSecureBeadsDir(t *testing.T) {
	bdBin := buildBDForInitPermissionTests(t)

	repoDir := newGitRepo(t)

	beadsDir := filepath.Join(repoDir, ".beads")
	if err := os.Mkdir(beadsDir, 0700); err != nil {
		t.Fatalf("failed to create .beads: %v", err)
	}

	cmd := exec.Command(bdBin, "init", "--prefix", "bd",
		"--non-interactive", "--skip-hooks", "--skip-agents")
	cmd.Dir = repoDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()

	// Permissions should remain 0700.
	info, err := os.Stat(beadsDir)
	if err != nil {
		t.Fatalf("Stat(.beads) after init: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0700 {
		t.Errorf(".beads permissions after init = %04o, want 0700", perm)
	}

	// No fix message expected.
	if strings.Contains(stderr.String(), "Fixed .beads permissions") {
		t.Errorf("unexpected permission-fix message for already-secure .beads/:\n%s", stderr.String())
	}
}
