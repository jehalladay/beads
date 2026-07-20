//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// TestGraphSingleIssueLayoutJSONCasing is the beads-okezz regression teeth.
//
// `bd graph <id> --json` emits {root, issues, layout} where layout is the
// GraphLayout struct. GraphLayout (and its nested GraphNode) had NO json tags,
// so the nested layout sub-object serialized Go field names in PascalCase:
//   layout keys:            Layers, MaxLayer, Nodes, RootID
//   layout.Nodes[<id>] keys: DependsOn, Issue, Layer, Position
// while the sibling top-level issues[] (types.Issue, tagged) is snake_case — a
// jarring mixed-case tree for --json consumers. jyaw fixed the --all path and
// labeled this single-issue path "clean", but only inspected TOP-LEVEL keys;
// the nested layout was never examined and still leaked (non-dup).
//
// Fix: add json tags to GraphLayout + GraphNode (layers/max_layer/nodes/root_id;
// depends_on/issue/layer/position). Mutation proof: drop the tags → the
// PascalCase assertions below fail → RED.
func TestGraphSingleIssueLayoutJSONCasing_okezz(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "gl")

	epic := bdCreate(t, bd, dir, "Layout epic", "--type", "epic")
	task := bdCreate(t, bd, dir, "Layout task")
	bdDep(t, bd, dir, "add", task.ID, epic.ID, "--type", "parent-child")

	cmd := exec.Command(bd, "graph", epic.ID, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("graph <id> --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	var root map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &root); err != nil {
		t.Fatalf("stdout is not a single JSON object: %v\nstdout:\n%s", err, stdout.String())
	}

	layout, ok := root["layout"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'layout' object, got: %v", root["layout"])
	}

	// (1) layout keys must be snake_case — no PascalCase struct names.
	for _, bad := range []string{"Layers", "MaxLayer", "Nodes", "RootID"} {
		if _, present := layout[bad]; present {
			t.Errorf("layout exposes PascalCase key %q (want snake_case)", bad)
		}
	}
	// Positive: the snake_case keys are present.
	for _, want := range []string{"layers", "max_layer", "nodes", "root_id"} {
		if _, present := layout[want]; !present {
			t.Errorf("layout missing snake_case key %q; got %v", want, keysOf(layout))
		}
	}

	// (2) nested layout.nodes[<id>] (GraphNode) must also be snake_case.
	nodes, ok := layout["nodes"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected layout.nodes object, got: %v", layout["nodes"])
	}
	for id, n := range nodes {
		node, ok := n.(map[string]interface{})
		if !ok {
			t.Fatalf("layout.nodes[%s] is not an object: %v", id, n)
		}
		for _, bad := range []string{"DependsOn", "Issue", "Layer", "Position"} {
			if _, present := node[bad]; present {
				t.Errorf("layout.nodes[%s] exposes PascalCase key %q (want snake_case)", id, bad)
			}
		}
		for _, want := range []string{"depends_on", "issue", "layer", "position"} {
			if _, present := node[want]; !present {
				t.Errorf("layout.nodes[%s] missing snake_case key %q; got %v", id, want, keysOf(node))
			}
		}
	}
}
