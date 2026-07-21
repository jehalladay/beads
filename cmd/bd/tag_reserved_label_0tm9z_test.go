//go:build cgo

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestTagRejectsReservedIdentityLabel_0tm9z is the end-to-end regression for
// beads-0tm9z: `bd tag <id> <label>` must reject a reserved gt identity label
// (gt:agent/gt:role/gt:rig) on a non-gt-internal write, matching single
// `bd create` (create.go:200), `bd label add` (label.go:277), and the
// create-input-parity reserved-label family. `bd tag` is documented as
// shorthand for `bd update --add-label` but has its OWN RunE that does NOT route
// through update.go's chokepoint, so a mutation-path guard would not cover it.
// These labels hide a bead from `bd ready` (beads-wqs discriminator), so a
// hand-set tag would silently hide real work — the spoof vector beads-3c4g
// closed at write-time. The guard runs at the tag RunE chokepoint before the
// usesProxiedServer() split, so ONE site covers direct + proxied.
func TestTagRejectsReservedIdentityLabel_0tm9z(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tg")

	created := bdCreate(t, bd, dir, "tag reserved-label target", "--type", "task")

	for _, label := range []string{"gt:agent", "gt:role", "gt:rig"} {
		// GT_INTERNAL must be unset so the guard fires (non-privileged write).
		out, err := runTagWithEnv(t, bd, dir, created.ID, label, false)
		if err == nil {
			t.Errorf("bd tag %s %q should reject the reserved identity label (spoof vector), got success:\n%s", created.ID, label, out)
			continue
		}
		if !strings.Contains(out, label) {
			t.Errorf("rejection for %q should name the reserved label; output = %q", label, out)
		}
		// A rejected tag must not silently claim it added the label.
		if strings.Contains(out, "Added label") {
			t.Errorf("rejected reserved-label tag must NOT print '✓ Added label': %s", out)
		}
	}
}

// TestTagReservedLabelAllowedWithGTInternal_0tm9z verifies the guard does not
// break gt's own registration: with GT_INTERNAL set, `bd tag` may stamp an
// identity label, matching the create/label-add gating (reserved_labels.go
// gtInternalWrite bypass).
func TestTagReservedLabelAllowedWithGTInternal_0tm9z(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ti")

	created := bdCreate(t, bd, dir, "gt internal tag target", "--type", "task")

	out, err := runTagWithEnv(t, bd, dir, created.ID, "gt:role", true)
	if err != nil {
		t.Fatalf("bd tag with GT_INTERNAL set should allow a reserved identity label, got err: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Added label") {
		t.Errorf("GT_INTERNAL tag of a reserved label should succeed with '✓ Added label': %s", out)
	}
}

// runTagWithEnv runs `bd tag <id> <label>` with GT_INTERNAL either set (gt
// orchestrator write) or explicitly cleared (human write), returning combined
// stdout+stderr. It mirrors bdRunWithFlockRetry's env but controls GT_INTERNAL
// deterministically instead of inheriting the host's.
func runTagWithEnv(t *testing.T, bd, dir, id, label string, gtInternal bool) (string, error) {
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
		cmd := exec.Command(bd, "tag", id, label)
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
		t.Logf("bd tag: flock contention (attempt %d/10), retrying...", attempt+1)
	}
	return "", os.ErrDeadlineExceeded
}
