package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestMolShowParallelJSONNilSlicesAreArrays_wgvo1 proves the `bd mol show
// --parallel --json` path (showMoleculeWithParallel, mol_show.go:457) emits []
// not null for dependencies / variables / bonded_from on a root-only,
// handlebars-free, non-compound molecule — matching the plain showMolecule path
// (beads-1sq7f). This is the sibling emit site 1sq7f missed: it normalized the
// default path's raw map but left showMoleculeWithParallel passing all three
// slices raw, so `--parallel --json` emitted null where `--json` emitted [].
// json-ARRAY nil-slice class (guib/5fv3/036h/4mkg/1sq7f siblings).
//
// Pure-Go: calls the REAL showMoleculeWithParallel (its --json branch needs no
// store) and captures os.Stdout, so reverting the nil->[] normalizations
// genuinely RED-fails this (not a re-implementation mirror).
func TestMolShowParallelJSONNilSlicesAreArrays_wgvo1(t *testing.T) {
	root := &types.Issue{ID: "m-root", Title: "plain root", IssueType: "epic"}
	subgraph := &MoleculeSubgraph{
		Root:     root,
		Issues:   []*types.Issue{root},
		IssueMap: map[string]*types.Issue{root.ID: root},
		// Dependencies nil (no internal deps); root.BondedFrom nil (non-compound);
		// no {{handlebars}} anywhere -> extractAllVariables returns a nil slice.
	}

	got := captureShowMoleculeParallelJSON(t, subgraph)

	for _, nullField := range []string{
		`"dependencies": null`,
		`"variables": null`,
		`"bonded_from": null`,
	} {
		if strings.Contains(got, nullField) {
			t.Errorf("--parallel emitted nil-as-null %q — want [] array contract; json=%s", nullField, got)
		}
	}
	for _, emptyArr := range []string{
		`"dependencies": []`,
		`"variables": []`,
		`"bonded_from": []`,
	} {
		if !strings.Contains(got, emptyArr) {
			t.Errorf("--parallel missing empty-array %q; json=%s", emptyArr, got)
		}
	}

	// Structural sanity: output is valid JSON.
	if !json.Valid([]byte(got)) {
		t.Errorf("showMoleculeWithParallel --json emitted invalid JSON: %s", got)
	}
}

// captureShowMoleculeParallelJSON runs showMoleculeWithParallel with
// jsonOutput=true and returns its stdout. Restores jsonOutput + os.Stdout on
// return.
func captureShowMoleculeParallelJSON(t *testing.T, subgraph *MoleculeSubgraph) string {
	t.Helper()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	runErr := showMoleculeWithParallel(subgraph, nil)

	_ = w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = oldStdout

	if runErr != nil {
		t.Fatalf("showMoleculeWithParallel returned error: %v", runErr)
	}
	return string(out)
}
