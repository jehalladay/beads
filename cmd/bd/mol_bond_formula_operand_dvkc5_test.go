//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedMolBondFormulaOperand is the beads-dvkc5 teeth: `bd mol bond` of
// a FORMULA operand had a two-layer defect that `bd mol pour` did not share.
//
// Layer 1 (resolution gate): resolveOrCookToSubgraph / resolveOrDescribe gated
// formula-cooking behind looksLikeFormulaName(), which accepted ONLY a "mol-"
// prefix, a ".formula" substring, or a path separator. A plain distilled
// formula name (e.g. "dvkc5plainflow") was rejected with "not found" even
// though `bd mol pour` cooks ANY valid formula name unconditionally
// (pour.go:~154). A pour/bond formula-resolution twin-divergence. FIX: drop the
// looksLikeFormulaName pre-gate; let the formula loader (LoadByName) be the
// authority — a name that is neither an issue nor a loadable formula still
// yields the same not-found error.
//
// Layer 2 (bondProtoProto assumed DB-resident protos): even a "mol-" name that
// cleared Layer 1 failed. A formula-cooked root is an in-memory
// *TemplateSubgraph (gt-4v1eo: "no DB storage") that was never CreateIssue'd,
// but bondProtoProto AddDependency'd the compound to protoA.ID/protoB.ID → the
// FK check failed ("issue mol-... not found"). FIX: materialize each cooked
// operand's subgraph into the DB inside the SAME bond transaction (mirroring
// cook's cookPlanTx, with the beads-1zq73/o70m1 reserved-label guard) before
// FK-linking the compound to the now-persisted roots.
//
// This drives the REAL embedded `bd` subprocess, so it exercises the live cook
// → materialize → link path end-to-end (a pure marshal test would false-green).
//
// Mutation-verify:
//   - Restore the looksLikeFormulaName gate in resolveOrCookToSubgraph → the
//     plain-name subtest goes RED ("not found as issue or formula").
//   - Revert bondProtoProto to link protoA.ID/protoB.ID without materializing →
//     BOTH subtests go RED (FK failure: "issue ... not found").
func TestEmbeddedMolBondFormulaOperand(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// bondJSON runs a live `bd mol bond a b --json` and returns the parsed
	// result plus the raw combined output for diagnostics. The explicit
	// --dry-run=false guards against cobra bool-flag bleed is unnecessary for a
	// fresh subprocess, but --json keeps the assertion structural.
	type bondRes struct {
		ResultID   string `json:"result_id"`
		ResultType string `json:"result_type"`
		Spawned    int    `json:"spawned"`
	}
	bondJSON := func(t *testing.T, dir, a, b string) (bondRes, string, error) {
		t.Helper()
		out, err := bdRunWithFlockRetry(t, bd, dir, "mol", "bond", a, b, "--json")
		combined := string(out)
		if err != nil {
			return bondRes{}, combined, err
		}
		start := strings.Index(combined, "{")
		if start < 0 {
			t.Fatalf("no JSON in bond output:\n%s", combined)
		}
		var res bondRes
		if e := json.Unmarshal([]byte(combined[start:]), &res); e != nil {
			t.Fatalf("cannot parse bond JSON: %v\noutput:\n%s", e, combined)
		}
		return res, combined, nil
	}

	// issueExists reports whether `bd show <id> --json` finds the issue — the
	// Layer 2 materialization proof: a cooked operand's root must be a real DB
	// row after bond, else the compound's FK link could not have been created.
	issueExists := func(t *testing.T, dir, id string) bool {
		t.Helper()
		out, err := bdRunWithFlockRetry(t, bd, dir, "show", id, "--json")
		if err != nil {
			return false
		}
		return strings.Contains(string(out), id)
	}

	// (1) LAYER 1: a PLAIN formula name (no "mol-" prefix, no ".formula", no
	//     path separator) must cook — matching `bd mol pour`. On buggy source
	//     looksLikeFormulaName rejects it before cooking → "not found".
	//     Bonding two plain-named cooked formulas also exercises Layer 2
	//     materialization (both operands are in-memory subgraphs).
	t.Run("plain_formula_names_bond_to_compound_proto", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "dva")
		a := writeBondFormula(t, beadsDir, "dvaplainone")
		b := writeBondFormula(t, beadsDir, "dvaplaintwo")

		res, combined, err := bondJSON(t, dir, a, b)
		if err != nil {
			t.Fatalf("beads-dvkc5 L1: bond of two PLAIN-named formulas failed "+
				"(looksLikeFormulaName pre-gate rejected the distilled name that pour cooks fine): %v\n%s",
				err, combined)
		}
		if res.ResultType != "compound_proto" {
			t.Fatalf("beads-dvkc5: result_type=%q, want compound_proto\n%s", res.ResultType, combined)
		}
		// Layer 2: both cooked roots must now be persisted (materialized).
		if !issueExists(t, dir, a) {
			t.Errorf("beads-dvkc5 L2: cooked operand root %q was not materialized into the DB", a)
		}
		if !issueExists(t, dir, b) {
			t.Errorf("beads-dvkc5 L2: cooked operand root %q was not materialized into the DB", b)
		}
		// spawned counts materialized issues (root+step per operand → 4 total).
		if res.Spawned == 0 {
			t.Errorf("beads-dvkc5 L2: spawned=0, expected the cooked subgraphs to be materialized\n%s", combined)
		}
	})

	// (2) LAYER 2 (isolated): a "mol-" prefixed formula clears Layer 1 even on
	//     buggy source, so this subtest isolates the materialization fix. On
	//     buggy source bondProtoProto AddDependency's the compound to the
	//     in-memory (never-created) mol- root → FK failure.
	t.Run("mol_prefixed_formula_materializes_before_link", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "dvb")
		a := writeBondFormula(t, beadsDir, "mol-dvbflow")
		b := writeBondFormula(t, beadsDir, "mol-dvbdeploy")

		res, combined, err := bondJSON(t, dir, a, b)
		if err != nil {
			t.Fatalf("beads-dvkc5 L2: bond of two mol- formulas failed "+
				"(cooked proto root never materialized → AddDependency FK failure): %v\n%s",
				err, combined)
		}
		if res.ResultType != "compound_proto" {
			t.Fatalf("beads-dvkc5: result_type=%q, want compound_proto\n%s", res.ResultType, combined)
		}
		if !issueExists(t, dir, a) || !issueExists(t, dir, b) {
			t.Errorf("beads-dvkc5 L2: cooked mol- operand roots not materialized (a=%q b=%q)", a, b)
		}
	})
}

// writeBondFormula writes a minimal single-step workflow formula named `name`
// to <beadsDir>/formulas so it is discoverable by BARE NAME via the formula
// search path (parser.DefaultSearchPaths includes <beadsDir>/formulas). It is
// NOT persisted (no `bd cook --persist`) — bond must cook it IN-MEMORY, which
// is exactly the path beads-dvkc5 fixes. Returns the formula name, which is
// also the cooked proto root ID.
func writeBondFormula(t *testing.T, beadsDir, name string) string {
	t.Helper()
	formulasDir := filepath.Join(beadsDir, "formulas")
	if err := os.MkdirAll(formulasDir, 0o755); err != nil {
		t.Fatalf("mkdir formulas dir: %v", err)
	}
	body := fmt.Sprintf(`formula = %q
description = "beads-dvkc5 bond formula-operand teeth"
version = 1
type = "workflow"

[[steps]]
id = "only"
title = "only step"
description = "single step so the cooked proto has something to materialize"
`, name)
	path := filepath.Join(formulasDir, name+".formula.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write formula %s: %v", name, err)
	}
	return name
}
