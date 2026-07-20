//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// beads-c3igh: `bd rename <old> <foreign-prefix-id>` accepted an ID whose
// prefix is not the DB's issue_prefix (nor an allowed prefix), silently
// creating an off-prefix, effectively-unroutable bead — even though rename's
// own help says "The new ID must use a valid prefix for this database" and
// `bd create --id <foreign>` rejects it via validation.ValidateIDPrefixAllowed.
// rename.go only validated the ID *format* (a raw ^[a-z]+-...$ regex, any
// prefix passes) and never checked the DB prefix. The fix mirrors create: after
// the format check, resolve the DB prefix + allowed prefixes and call
// ValidateIDPrefixAllowed, with a --force escape hatch (parity with create).
//
// These teeth run bd as a subprocess (the defect is in cobra RunE) and assert
// the rename to a foreign prefix now FAILS, while a same-prefix rename and a
// --force foreign rename still succeed.

// TestRenameForeignPrefix_rejected covers the DIRECT (non-proxied) path.
func TestRenameForeignPrefix_rejected(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bd")

	// Seed an issue to rename.
	seed := exec.Command(bd, "create", "--silent", "Seed issue")
	seed.Dir = dir
	seed.Env = bdEnv(dir)
	sout, serr, err := runCommandBuffers(t, seed)
	if err != nil {
		t.Fatalf("seed create failed: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	oldID := strings.TrimSpace(sout.String())
	if oldID == "" {
		t.Fatalf("seed create produced no id\nstdout:\n%s\nstderr:\n%s", sout.String(), serr.String())
	}

	// Rename to a FOREIGN prefix — must be rejected (mirrors create --id).
	foreign := exec.Command(bd, "rename", oldID, "wrongpfx-abc")
	foreign.Dir = dir
	foreign.Env = bdEnv(dir)
	fout, ferr, ferrErr := runCommandBuffers(t, foreign)
	if ferrErr == nil {
		t.Fatalf("rename to foreign prefix wrongpfx-abc unexpectedly SUCCEEDED (should reject off-prefix id)\nstdout:\n%s\nstderr:\n%s", fout.String(), ferr.String())
	}

	// A --force foreign rename is the deliberate-override escape hatch (parity
	// with create --force) and must still succeed.
	force := exec.Command(bd, "rename", oldID, "wrongpfx-abc", "--force")
	force.Dir = dir
	force.Env = bdEnv(dir)
	xout, xerr, xerrErr := runCommandBuffers(t, force)
	if xerrErr != nil {
		t.Fatalf("rename --force to foreign prefix should succeed (deliberate override), got error: %v\nstdout:\n%s\nstderr:\n%s", xerrErr, xout.String(), xerr.String())
	}
}

// TestRenameSamePrefix_allowed guards against over-rejection: renaming to a
// valid same-prefix id must still work.
func TestRenameSamePrefix_allowed(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bd")

	seed := exec.Command(bd, "create", "--silent", "Seed issue 2")
	seed.Dir = dir
	seed.Env = bdEnv(dir)
	sout, serr, err := runCommandBuffers(t, seed)
	if err != nil {
		t.Fatalf("seed create failed: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	oldID := strings.TrimSpace(sout.String())

	same := exec.Command(bd, "rename", oldID, "bd-renamed-ok")
	same.Dir = dir
	same.Env = bdEnv(dir)
	out, errBuf, rErr := runCommandBuffers(t, same)
	if rErr != nil {
		t.Fatalf("rename to a valid same-prefix id should succeed, got error: %v\nstdout:\n%s\nstderr:\n%s", rErr, out.String(), errBuf.String())
	}
}
