//go:build cgo

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedPourAttachTypeValidate is the beads-u6suf teeth
// (EARLY-VALIDATION-PARITY class, twin of ev8m/0oj6e): `bd mol bond --type`
// validates the bond type up front (mol_bond.go runMolBond: reject unless
// sequential|parallel|conditional, BEFORE any mutation), but `bd pour
// --attach-type` did NOT — pour.go read the flag (attachType) and passed it
// unvalidated into the shared bondProtoMolWithSubgraph switch (mol_bond.go),
// whose `default:` arm maps ANY unknown value to DepParentChild (parallel). So
// a typo'd --attach-type (e.g. "sequentail") was silently accepted and the
// attachment bonded as parallel/parent-child instead of the intended
// sequential (blocks) — wrong dependency semantics with no signal, on a
// command that has already mutated (spawnMolecule created the root before the
// attach loop).
//
// The fix adds the same up-front guard as mol bond right after the flag read
// in runPour (before spawnMolecule), so an invalid --attach-type is rejected
// with "invalid attach-type '<v>', must be: sequential, parallel, or
// conditional" and NOTHING is mutated. This drives the real embedded `bd pour`
// subprocess against two persisted protos.
//
// Mutation-verify: delete the guard and the invalid-attach-type subtest goes
// RED (pour resolves the protos, spawns the molecule, and bonds the attachment
// on the parent-child DEFAULT arm — exiting 0 with "Poured mol" instead of
// rejecting the typo).
func TestEmbeddedPourAttachTypeValidate(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// (1) The bug: an invalid --attach-type must be rejected up front, exactly
	//     like `bd mol bond --type`. The protos are real (cooked+persisted) so
	//     that with the guard REMOVED the command would otherwise succeed with a
	//     wrong (parent-child) bond — that is what makes this a true RED probe.
	t.Run("invalid_attach_type_rejected_up_front", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "uva")
		root := cookPourProto(t, bd, dir, "uva-root")
		att := cookPourProto(t, bd, dir, "uva-att")

		out, err := bdRunWithFlockRetry(t, bd, dir,
			"mol", "pour", root, "--attach", att, "--attach-type", "sequentail")
		combined := string(out)
		if err == nil {
			t.Fatalf("beads-u6suf: pour with a typo'd --attach-type unexpectedly SUCCEEDED "+
				"(must reject up front like `bd mol bond --type`); output:\n%s", combined)
		}
		if !strings.Contains(combined, "invalid attach-type") {
			t.Errorf("beads-u6suf: expected the up-front 'invalid attach-type' guard message "+
				"(idiom-matched to mol bond), got:\n%s", combined)
		}
	})

	// (2) Positive control: a valid --attach-type is accepted (the guard must
	//     not over-reject the three legal values).
	t.Run("valid_attach_type_accepted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "uvb")
		root := cookPourProto(t, bd, dir, "uvb-root")
		att := cookPourProto(t, bd, dir, "uvb-att")

		out, err := bdRunWithFlockRetry(t, bd, dir,
			"mol", "pour", root, "--attach", att, "--attach-type", "sequential")
		if err != nil {
			t.Fatalf("beads-u6suf: pour with a valid --attach-type sequential failed "+
				"(guard must accept legal values): %v\n%s", err, string(out))
		}
		if !strings.Contains(string(out), "Poured mol") {
			t.Errorf("beads-u6suf: expected a successful pour ('Poured mol') for a valid "+
				"--attach-type, got:\n%s", string(out))
		}
	})

	// (3) Default is valid: bare pour --attach (no --attach-type) uses the flag
	//     default "sequential", which the guard must accept.
	t.Run("default_attach_type_accepted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "uvc")
		root := cookPourProto(t, bd, dir, "uvc-root")
		att := cookPourProto(t, bd, dir, "uvc-att")

		out, err := bdRunWithFlockRetry(t, bd, dir,
			"mol", "pour", root, "--attach", att)
		if err != nil {
			t.Fatalf("beads-u6suf: pour with the default --attach-type failed: %v\n%s",
				err, string(out))
		}
		if !strings.Contains(string(out), "Poured mol") {
			t.Errorf("beads-u6suf: expected a successful pour for the default --attach-type, got:\n%s",
				string(out))
		}
	})
}

// cookPourProto writes a minimal single-step workflow formula named `name` to
// dir and persists it, returning the proto (template) id. `bd cook --persist`
// mints a proto molecule (template label) — exactly the attachable artifact
// `bd pour`/`--attach` require (an --attach target must pass isProto).
func cookPourProto(t *testing.T, bd, dir, name string) string {
	t.Helper()
	formula := fmt.Sprintf(`formula = %q
description = "pour attach-type validation proto"
version = 1
type = "workflow"

[[steps]]
id = "only"
title = "only step"
description = "single step so the proto is valid"
`, name)
	path := filepath.Join(dir, name+".formula.toml")
	if err := os.WriteFile(path, []byte(formula), 0o644); err != nil {
		t.Fatalf("write formula %s: %v", name, err)
	}
	cmd := exec.Command(bd, "cook", path, "--persist", "--force")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd cook --persist %s failed: %v\n%s", name, err, out)
	}
	return name
}
