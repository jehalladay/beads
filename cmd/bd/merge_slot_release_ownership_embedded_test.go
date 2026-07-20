//go:build cgo

package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// bdRunAsActor runs a bd command in DIRECT (embedded) mode as a specific
// actor, returning combined stdout+stderr and the run error (non-nil =
// non-zero exit). The actor is passed via the --actor flag (highest
// precedence — it bypasses the config/GT_ROLE/git-user resolution that would
// otherwise collapse every invocation to the ambient crew identity in a
// shared workspace). Crucially, NO --holder flag is passed, so the slot
// commands exercise the default holder-from-actor path that beads-vmwzn fixes.
func bdRunAsActor(t *testing.T, bd, dir, actor string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"--actor", actor}, args...)
	cmd := exec.Command(bd, full...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

// TestMergeSlotReleaseEnforcesOwnershipByDefault_vmwzn is the end-to-end
// regression for beads-vmwzn. `bd merge-slot release` in the normal
// BEADS_ACTOR workflow (no --holder) passed an empty holder to the storage
// layer, whose ownership guard (`holder != "" && meta.Holder != holder`) is
// SKIPPED on empty holder — so any actor could release another actor's slot,
// silently breaking the merge-queue mutual-exclusion primitive. The fix
// mirrors acquire's holder fallback (mergeSlotHolder else BEADS_ACTOR) into
// release so the guard receives the caller's identity and can enforce
// ownership.
//
// The existing merge_slot tests all pass a NON-empty holder, so the default
// (empty-holder) path was untested — this is exactly that gap.
func TestMergeSlotReleaseEnforcesOwnershipByDefault_vmwzn(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ms")

	// The merge slot must exist before it can be acquired.
	if out, err := bdRunAsActor(t, bd, dir, "actorA", "merge-slot", "create"); err != nil {
		t.Fatalf("merge-slot create should succeed: %v\n%s", err, out)
	}

	// actorA acquires the slot (no --holder → holder resolves to actor).
	if out, err := bdRunAsActor(t, bd, dir, "actorA", "merge-slot", "acquire"); err != nil {
		t.Fatalf("actorA acquire should succeed: %v\n%s", err, out)
	}
	// Sanity: the slot must actually be held by actorA (guards against the
	// test collapsing both actors to one ambient identity, which would make
	// the ownership assertion vacuous).
	if out, err := bdRunAsActor(t, bd, dir, "actorA", "merge-slot", "check"); err != nil {
		t.Fatalf("check should succeed: %v\n%s", err, out)
	} else if !strings.Contains(out, "Holder: actorA") {
		t.Fatalf("slot should be held by actorA; got:\n%s", out)
	}

	// actorB attempts to release WITHOUT --holder. Before the fix this wrongly
	// succeeded (empty holder skipped the ownership guard). It must now FAIL
	// with the storage guard's ownership error naming both parties.
	out, err := bdRunAsActor(t, bd, dir, "actorB", "merge-slot", "release")
	if err == nil {
		t.Fatalf("actorB release of actorA's slot must be REJECTED (non-zero exit); got success:\n%s", out)
	}
	if !strings.Contains(out, "slot held by actorA, not actorB") {
		t.Fatalf("release rejection should report 'slot held by actorA, not actorB'; got:\n%s", out)
	}

	// The rightful holder (actorA) must still be able to release its own slot.
	if out, err := bdRunAsActor(t, bd, dir, "actorA", "merge-slot", "release"); err != nil {
		t.Fatalf("actorA release of its own slot should succeed: %v\n%s", err, out)
	}
}
