//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// TestGraphAllJSONContract is the beads-jyaw regression teeth.
//
// `bd graph --all --json` used to `return outputJSON(subgraphs)` on the raw
// []*TemplateSubgraph. TemplateSubgraph (template.go) has no json tags, so the
// output had three contract defects that diverged from the single-issue sibling
// (`bd graph <id> --json`, which builds an explicit lowercase map):
//  1. PascalCase field names (Root/Issues/Dependencies) instead of snake_case.
//  2. Engine-internal fields leaked into the public API (IssueMap, VarDefs,
//     Phase, Pour) — consumers would couple to formula-engine impl details.
//  3. No schema_version (wrapWithSchemaVersion returns bare slices as-is).
//
// The fix builds an explicit {root, issues, dependencies} map per subgraph and
// wraps the list in a {subgraphs: [...]} object so schema_version is attached.
//
// Mutation proof: restore `return outputJSON(subgraphs)` and the leaked-field /
// PascalCase / schema_version assertions fail → RED.
func TestGraphAllJSONContract(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "gr")

	epic := bdCreate(t, bd, dir, "Graph epic", "--type", "epic")
	taskA := bdCreate(t, bd, dir, "Task A")
	taskB := bdCreate(t, bd, dir, "Task B")
	bdDep(t, bd, dir, "add", taskA.ID, epic.ID, "--type", "parent-child")
	bdDep(t, bd, dir, "add", taskB.ID, epic.ID, "--type", "parent-child")

	cmd := exec.Command(bd, "graph", "--all", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("graph --all --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	var root map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &root); err != nil {
		t.Fatalf("stdout is not a single JSON object: %v\nstdout:\n%s", err, stdout.String())
	}

	// (3) schema_version must be present at the top level.
	if _, ok := root["schema_version"]; !ok {
		t.Errorf("missing schema_version at top level; got keys %v", keysOf(root))
	}

	subs, ok := root["subgraphs"].([]interface{})
	if !ok {
		t.Fatalf("expected 'subgraphs' array, got: %v", root)
	}
	if len(subs) == 0 {
		t.Fatalf("expected at least one subgraph, got none: %s", stdout.String())
	}

	for i, s := range subs {
		sg, ok := s.(map[string]interface{})
		if !ok {
			t.Fatalf("subgraph %d is not an object: %v", i, s)
		}
		// (1) snake_case / lowercase public keys only — no PascalCase.
		for _, bad := range []string{"Root", "Issues", "Dependencies"} {
			if _, present := sg[bad]; present {
				t.Errorf("subgraph %d exposes PascalCase key %q (want lowercase)", i, bad)
			}
		}
		// (2) engine-internal fields must NOT leak into the public shape.
		for _, leaked := range []string{"IssueMap", "VarDefs", "Phase", "Pour"} {
			if _, present := sg[leaked]; present {
				t.Errorf("subgraph %d leaks internal field %q", i, leaked)
			}
		}
		// Positive: the public contract keys are present.
		if _, present := sg["root"]; !present {
			t.Errorf("subgraph %d missing 'root'", i)
		}
		if _, present := sg["issues"]; !present {
			t.Errorf("subgraph %d missing 'issues'", i)
		}
		if _, present := sg["dependencies"]; !present {
			t.Errorf("subgraph %d missing 'dependencies'", i)
		}
	}
}

func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
