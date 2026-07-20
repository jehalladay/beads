//go:build cgo && integration

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// TestMolBondProtoPredicate proves the live `bd mol bond` dispatch classifies
// operands with the SAME proto predicate the help text, dry-run preview, and
// isProto() use — the template LABEL — not the is_template COLUMN (beads-v8ck8).
//
// ROOT (mol_bond.go L157-158): the live dispatch keyed off
// `issueA.IsTemplate || cookedA`. The is_template column is written ONLY by
// formula-cooked protos (cook.go/molecules.go), NOT by the documented
// `bd create --label template`. So a canonically label-defined proto has
// is_template=NULL → aIsProto=false → the proto-involving cases (proto+proto,
// proto+mol, mol+proto) are skipped and bond falls through to the bondMolMol
// default. Two silent defects: (1) the documented proto paths are unreachable
// for label-defined protos, and (2) the --dry-run preview promises
// "compound proto" while the live run produces "compound_molecule" on the same
// operands — a preview that lies.
//
// The fix aligns L157-158 to `isProto(issueA) || cookedA` (label OR cooked),
// matching help/dry-run/isProto with no read-path change.
func TestMolBondProtoPredicate(t *testing.T) {
	// resetCreateLabelFlag clears the create --label StringSlice between
	// in-process runs. Cobra StringSlice flags accumulate across Execute calls
	// on the shared rootCmd (they append once Changed is set), so a prior
	// `create --label template` would leak the template label onto a later
	// create — silently turning a plain molecule into a proto. Never happens in
	// a real bd process (each run is fresh). Same footgun documented at
	// label_batch_test.go resetLabelAddFlag.
	resetCreateLabelFlag := func(t *testing.T) {
		t.Helper()
		f := createCmd.Flags().Lookup("label")
		if f == nil {
			t.Fatalf("create is missing the --label flag")
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			_ = sv.Replace([]string{})
		}
		f.Changed = false
	}

	// createID creates an issue and returns its assigned ID (from --json).
	createID := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		resetCreateLabelFlag(t)
		out := runBDInProcess(t, dir, append([]string{"create"}, append(args, "--json")...)...)
		start := strings.Index(out, "{")
		if start < 0 {
			t.Fatalf("no JSON in create output:\n%s", out)
		}
		var res struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(out[start:]), &res); err != nil {
			t.Fatalf("cannot parse create JSON: %v\n%s", err, out)
		}
		if res.ID == "" {
			t.Fatalf("create returned empty id:\n%s", out)
		}
		return res.ID
	}

	// bondResult runs a live bond and returns (result_type, spawned). The
	// explicit --dry-run=false guards against cobra bool-flag state bleeding
	// across runBDInProcess calls on the shared rootCmd (a prior --dry-run
	// invocation in the same test binary leaves the flag set otherwise).
	bondResult := func(t *testing.T, dir, a, b string) (string, int) {
		t.Helper()
		out := runBDInProcess(t, dir, "mol", "bond", a, b, "--dry-run=false", "--json")
		start := strings.Index(out, "{")
		if start < 0 {
			t.Fatalf("no JSON in bond output:\n%s", out)
		}
		var res struct {
			ResultType string `json:"result_type"`
			Spawned    int    `json:"spawned"`
		}
		if err := json.Unmarshal([]byte(out[start:]), &res); err != nil {
			t.Fatalf("cannot parse bond JSON: %v\noutput:\n%s", err, out)
		}
		return res.ResultType, res.Spawned
	}

	t.Run("proto_plus_proto_via_label_is_compound_proto", func(t *testing.T) {
		dir := setupCLITestDB(t)
		// Two canonically label-defined protos (is_template=NULL, label=template).
		p := createID(t, dir, "Proto P", "-t", "epic", "--label", "template", "-p", "2")
		q := createID(t, dir, "Proto Q", "-t", "epic", "--label", "template", "-p", "2")

		// Live bond → must reach bondProtoProto → compound_proto.
		got, _ := bondResult(t, dir, p, q)
		if got != "compound_proto" {
			t.Fatalf("bond of two label-defined protos: result_type=%q, want %q "+
				"(dispatch keyed off is_template column, not the template label — beads-v8ck8)",
				got, "compound_proto")
		}
	})

	t.Run("dry_run_matches_live_for_label_protos", func(t *testing.T) {
		dir := setupCLITestDB(t)
		p := createID(t, dir, "Proto P", "-t", "epic", "--label", "template", "-p", "2")
		q := createID(t, dir, "Proto Q", "-t", "epic", "--label", "template", "-p", "2")

		// Dry-run preview promises a compound proto.
		dry := runBDInProcess(t, dir, "mol", "bond", p, q, "--dry-run")
		if !strings.Contains(dry, "Result: compound proto") {
			t.Fatalf("dry-run should preview 'compound proto' for two label protos:\n%s", dry)
		}

		// Live run must not diverge from the preview.
		if got, _ := bondResult(t, dir, p, q); got != "compound_proto" {
			t.Fatalf("dry-run/live divergence: preview said 'compound proto' but live result_type=%q", got)
		}
	})

	t.Run("proto_plus_mol_via_label_spawns_proto", func(t *testing.T) {
		dir := setupCLITestDB(t)
		// A label-defined proto WITH a child step, and a plain molecule.
		// bondProtoMol and bondMolMol both return ResultType=compound_molecule
		// with the mol's ID, so the only observable discriminator between the
		// correct route (bondProtoMol spawns+attaches the proto's subgraph) and
		// the misroute (bondMolMol just cross-links, spawns nothing) is
		// Spawned>0 — which requires the proto to have something to instantiate.
		proto := createID(t, dir, "Proto T", "-t", "epic", "--label", "template", "-p", "2")
		createID(t, dir, "Proto step", "-t", "task", "-p", "2", "--parent", proto)
		// --parent="" explicitly clears the flag: cobra persists the prior
		// --parent value on the shared rootCmd across runBDInProcess calls, which
		// would otherwise make "Real feature" a child of the proto (bonding the
		// proto to its own child → dep-exists error).
		mol := createID(t, dir, "Real feature", "-t", "epic", "-p", "2", "--parent", "")

		_, spawned := bondResult(t, dir, proto, mol)
		if spawned == 0 {
			t.Fatalf("proto+mol bond spawned 0 issues — the label-defined proto was not " +
				"instantiated (misrouted to bondMolMol instead of bondProtoMol; beads-v8ck8)")
		}
	})
}
