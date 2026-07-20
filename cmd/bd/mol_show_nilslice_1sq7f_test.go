package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestMolShowJSONNilSlicesAreArrays_1sq7f proves the plain `bd mol show --json`
// path (showMolecule, mol_show.go:66) emits [] not null for dependencies /
// variables / bonded_from on a root-only, handlebars-free, non-compound
// molecule. json-ARRAY nil-slice class (guib/5fv3/036h/4mkg siblings), a
// DISTINCT emit site: 036h fixed internal/formula ExtractVariables +
// mol_distill getVarNames; 4mkg fixed the --parallel ParallelInfo struct; this
// is the plain showMolecule raw-map path they don't touch.
//
// Pure-Go: calls the REAL showMolecule (its --json branch needs no store) and
// captures os.Stdout, so reverting the nil->[] normalizations genuinely RED-fails
// this (it is not a re-implementation mirror).
func TestMolShowJSONNilSlicesAreArrays_1sq7f(t *testing.T) {
	root := &types.Issue{ID: "m-root", Title: "plain root", IssueType: "epic"}
	subgraph := &TemplateSubgraph{
		Root:     root,
		Issues:   []*types.Issue{root},
		IssueMap: map[string]*types.Issue{root.ID: root},
		// Dependencies nil (no internal deps); root.BondedFrom nil (non-compound);
		// no {{handlebars}} anywhere -> extractAllVariables returns a nil slice.
	}

	got := captureShowMoleculeJSON(t, subgraph)

	// The output is schema_version-wrapped; the payload sits under a top-level
	// key. Substring checks are sufficient and robust to the envelope.
	for _, nullField := range []string{
		`"dependencies": null`,
		`"variables": null`,
		`"bonded_from": null`,
	} {
		if strings.Contains(got, nullField) {
			t.Errorf("emitted nil-as-null %q — want [] array contract; json=%s", nullField, got)
		}
	}
	for _, emptyArr := range []string{
		`"dependencies": []`,
		`"variables": []`,
		`"bonded_from": []`,
	} {
		if !strings.Contains(got, emptyArr) {
			t.Errorf("missing empty-array %q; json=%s", emptyArr, got)
		}
	}

	// Structural sanity: output is valid JSON.
	if !json.Valid([]byte(got)) {
		t.Errorf("showMolecule --json emitted invalid JSON: %s", got)
	}
}

// captureShowMoleculeJSON runs showMolecule with jsonOutput=true and returns its
// stdout. Restores jsonOutput + os.Stdout on return.
func captureShowMoleculeJSON(t *testing.T, subgraph *TemplateSubgraph) string {
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

	runErr := showMolecule(subgraph)

	_ = w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = oldStdout

	if runErr != nil {
		t.Fatalf("showMolecule returned error: %v", runErr)
	}
	return string(out)
}
