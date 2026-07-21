//go:build cgo

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestSetStateRejectsReservedIdentityLabel_rdzwu is the end-to-end regression
// for beads-rdzwu: `bd set-state <id> gt=role` (which builds the label as
// <dimension>:<value> = "gt:role") must reject the reserved gt identity label
// on a non-gt-internal write, matching single `bd create` (create.go:200),
// `bd label add` (label.go:277), and the tag verb (tag.go, beads-0tm9z). These
// labels hide a bead from `bd ready` (beads-wqs discriminator), so a hand-set
// set-state would silently hide real work — the spoof vector beads-3c4g closed
// at write-time. The guard runs at the set-state RunE chokepoint before the
// usesProxiedServer() split, so ONE site covers direct + proxied.
func TestSetStateRejectsReservedIdentityLabel_rdzwu(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sr")

	created := bdCreate(t, bd, dir, "set-state reserved-label target", "--type", "task")

	// gt:agent / gt:role / gt:rig as <dimension>=<value>. Both separators the
	// verb accepts ('=' and ':') build the same reserved label, so exercise
	// both to prove the guard sees the constructed dimension:value form.
	for _, spec := range []string{"gt=agent", "gt=role", "gt=rig", "gt:role"} {
		// GT_INTERNAL must be unset so the guard fires (non-privileged write).
		out, err := runSetStateWithEnv(t, bd, dir, created.ID, spec, false)
		if err == nil {
			t.Errorf("bd set-state %s %q should reject the reserved identity label (spoof vector), got success:\n%s", created.ID, spec, out)
			continue
		}
		if !strings.Contains(out, "gt:") {
			t.Errorf("rejection for %q should name the reserved label; output = %q", spec, out)
		}
	}
}

// TestSetStateReservedLabelAllowedWithGTInternal_rdzwu verifies the guard does
// not break gt's own state stamping: with GT_INTERNAL set, `bd set-state` may
// set a gt: identity dimension, matching the create/label-add/tag gating
// (reserved_labels.go gtInternalWrite bypass).
func TestSetStateReservedLabelAllowedWithGTInternal_rdzwu(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "si")

	created := bdCreate(t, bd, dir, "gt internal set-state target", "--type", "task")

	out, err := runSetStateWithEnv(t, bd, dir, created.ID, "gt=role", true)
	if err != nil {
		t.Fatalf("bd set-state with GT_INTERNAL set should allow a reserved identity dimension, got err: %v\n%s", err, out)
	}
}

// TestSetStateNonReservedDimensionUnaffected_rdzwu verifies the guard does not
// over-reach: an ordinary state dimension still sets normally.
func TestSetStateNonReservedDimensionUnaffected_rdzwu(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sn")

	created := bdCreate(t, bd, dir, "ordinary set-state target", "--type", "task")

	out, err := runSetStateWithEnv(t, bd, dir, created.ID, "patrol=active", false)
	if err != nil {
		t.Fatalf("bd set-state with a non-reserved dimension should succeed, got err: %v\n%s", err, out)
	}
}

// runSetStateWithEnv runs `bd set-state <id> <spec>` with GT_INTERNAL either set
// (gt orchestrator write) or explicitly cleared (human write), returning
// combined stdout+stderr. It mirrors bdRunWithFlockRetry's env but controls
// GT_INTERNAL deterministically instead of inheriting the host's.
func runSetStateWithEnv(t *testing.T, bd, dir, id, spec string, gtInternal bool) (string, error) {
	t.Helper()
	env := bdEnv(dir)
	// Drop any inherited GT_INTERNAL so the reject case is deterministic.
	filtered := env[:0:0]
	for _, e := range env {
		if strings.HasPrefix(e, gtInternalEnv+"=") {
			continue
		}
		filtered = append(filtered, e)
	}
	if gtInternal {
		filtered = append(filtered, gtInternalEnv+"="+gtInternalValue)
	}

	for attempt := 0; attempt < 10; attempt++ {
		cmd := exec.Command(bd, "set-state", id, spec)
		cmd.Dir = dir
		cmd.Env = filtered
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		out := stdout.String() + stderr.String()
		if err == nil {
			return out, nil
		}
		if !isEmbeddedLockOutput(out) {
			return out, err
		}
		t.Logf("bd set-state: flock contention (attempt %d/10), retrying...", attempt+1)
	}
	return "", os.ErrDeadlineExceeded
}
