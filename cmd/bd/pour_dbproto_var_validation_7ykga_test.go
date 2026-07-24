//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/formula"
	"github.com/steveyegge/beads/internal/types"
)

// TestPourDBProtoEnforcesVarValidation_7ykga pins beads-7ykga: `bd mol pour`
// (and, via the shared loadTemplateSubgraph seam, wisp/bond) enforces the
// formula's enum/pattern/type var constraints on the DB-PROTO path, not just
// the ephemeral formula-name path.
//
// The 8m9o7 gate in pour.go/wisp.go/mol_bond.go only fires
// `if subgraph.VarDefs != nil`. The ephemeral path (resolveAndCookFormulaWithVars
// → cookFormulaToSubgraphWithVars) populates VarDefs, so it validated. But the
// DB-proto path (loadTemplateSubgraph, reached when the first arg resolves to a
// persisted proto ID rather than a formula name) never set VarDefs, so
// out-of-enum / pattern-violating --var values silently landed in durable
// issues. The fix (a) stashes the formula var defs on the root proto Metadata at
// cook time (marshalVarDefsIntoMetadata in collectCookPlan — covers both fresh
// cookFormula and --force paths) and (b) rehydrates them in loadTemplateSubgraph
// (extractVarDefsFromMetadata), so the SAME gate fires on the DB-proto path.
//
// MUTATION-VERIFY: delete the `subgraph.VarDefs = extractVarDefsFromMetadata(...)`
// line in loadTemplateSubgraph (or the marshalVarDefsIntoMetadata call in
// collectCookPlan) and out_of_enum_var_rejected goes RED (pour succeeds, RC=0,
// the bad value lands).
func TestPourDBProtoEnforcesVarValidation_7ykga(t *testing.T) {
	ctx := context.Background()

	origStore := store
	origActive := isStoreActive()
	origRootCtx := rootCtx
	origJSONOutput := jsonOutput
	origActor := actor
	t.Cleanup(func() {
		setStore(origStore)
		setStoreActive(origActive)
		rootCtx = origRootCtx
		jsonOutput = origJSONOutput
		actor = origActor
	})
	rootCtx = ctx
	jsonOutput = false
	actor = "test-actor"

	// enumFormula returns a one-step formula whose "env" var is enum-constrained.
	// It carries a title var so the root proto is a normal molecule.
	enumFormula := func(name string) *formula.Formula {
		return &formula.Formula{
			Formula:     name,
			Description: "7ykga enum var fixture",
			Vars: map[string]*formula.VarDef{
				"env": {Enum: []string{"dev", "prod"}, Required: true},
			},
			Steps: []*formula.Step{
				{ID: "s1", Title: "deploy to {{env}}", Type: "task"},
			},
		}
	}

	// pourProto invokes `bd mol pour <protoID> --var ...` against the DB-proto
	// path by resolving the persisted proto ID (NOT a formula name). Returns the
	// error runPour produced (nil on success) AND the stderr it emitted: the
	// var-validation gate reports via HandleError, which prints the detailed
	// "... not in allowed values ..." message to STDERR and returns a bare
	// &exitError{Code:1} (whose .Error() is only "exit code 1"), so the reason
	// for the rejection lives on stderr, not in the returned error.
	pourProto := func(protoID string, varArgs ...string) (error, string) {
		cmd := &cobra.Command{}
		cmd.Flags().StringArray("var", []string{}, "")
		cmd.Flags().Bool("dry-run", false, "")
		cmd.Flags().String("assignee", "", "")
		cmd.Flags().StringSlice("attach", []string{}, "")
		cmd.Flags().String("attach-type", types.BondTypeSequential, "")
		for _, v := range varArgs {
			_ = cmd.Flags().Set("var", v)
		}
		var runErr error
		stderr := captureStderr(t, func() {
			runErr = runPour(cmd, []string{protoID})
		})
		return runErr, stderr
	}

	freshStore := func(t *testing.T) {
		t.Helper()
		// runPour → ensureStoreActive() only honors the injected global store when
		// isStoreActive() is also true; otherwise it opens a DIFFERENT store from
		// the ambient .beads config, so the cooked proto is not found at pour time
		// (the resolution failure that masqueraded as a fix bug). Activate the
		// injected store the same way the CLI runtime does (setStore+setStoreActive).
		setStore(newTestStore(t, filepath.Join(t.TempDir(), ".beads", "beads.db")))
		setStoreActive(true)
	}

	t.Run("out_of_enum_var_rejected", func(t *testing.T) {
		freshStore(t)
		const protoID = "mol-7ykga-bad"
		// Cook + persist the proto (stashes the enum var def on root Metadata).
		if err := persistCookFormula(ctx, enumFormula(protoID), protoID, false, nil, nil); err != nil {
			t.Fatalf("cook --persist fixture failed: %v", err)
		}
		// Pour the PERSISTED proto (DB-proto path) with an out-of-enum value.
		err, stderr := pourProto(protoID, "env=staging")
		if err == nil {
			t.Fatal("pour of a DB-proto with out-of-enum --var env=staging should FAIL (7ykga), got nil — the DB-proto validation gate is not firing")
		}
		// The gate reports via HandleError → the enum-violation detail is on
		// stderr; the returned error is a bare exitError. Assert the reason so
		// this stays teeth (a generic failure would not name the enum).
		if !strings.Contains(stderr, "not in allowed values") {
			t.Errorf("reject should name the enum violation on stderr, got err=%v stderr=%q", err, stderr)
		}
	})

	t.Run("in_enum_var_still_pours", func(t *testing.T) {
		freshStore(t)
		const protoID = "mol-7ykga-ok"
		if err := persistCookFormula(ctx, enumFormula(protoID), protoID, false, nil, nil); err != nil {
			t.Fatalf("cook --persist fixture failed: %v", err)
		}
		// A valid enum value must still pour cleanly (no over-blocking).
		if err, stderr := pourProto(protoID, "env=prod"); err != nil {
			t.Fatalf("pour of a DB-proto with in-enum --var env=prod must succeed, got: %v stderr=%q", err, stderr)
		}
	})
}
