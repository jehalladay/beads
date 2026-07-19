//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// beads-gf0o8: `bd dep list --json` emitted arrays of INCOMPATIBLE object types
// depending on ARG COUNT (a shape-instability crossing DATA MODELS, not just
// container/outcome):
//   - dep list A            → []IssueWithDependencyMetadata (keys: id, title,
//     status, priority, issue_type, dependency_type, ...)
//   - dep list A B (down)   → []types.Dependency EDGE records (keys: issue_id,
//     depends_on_id, type, metadata, ...) via the multi-arg same-store fast path
//   - dep list A B --direction up → back to ISSUE objects (fast path is down-only)
//
// A --json consumer iterating .title breaks on the 2-id call; one iterating
// .depends_on_id breaks on the 1-id call. The issue-object shape is canonical
// (single-arg + --direction up + the human view all produce it). Fix: the JSON
// path must always emit the issue shape — the multi-arg-down edge-record fast
// path is gated to non-JSON output only, so JSON falls through to the canonical
// general (issue-with-metadata) path.
//
// This test builds one issue with two dependencies and asserts that the 1-id
// and 2-id (down) --json calls emit the SAME object schema. It fails before the
// fix (2-id call leaks issue_id/depends_on_id edge keys and lacks id/title).
func TestEmbeddedDepListJSONSchemaFlip_gf0o8(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sf")

	// dep1 and dep2 both depend_on blkA (blkA blocks both). Then:
	//   dep list dep1        → issue objects (dep1's deps: blkA)
	//   dep list dep1 dep2   → down, same-store, multi-arg → fast path pre-fix
	blkA := bdCreate(t, bd, dir, "blocker A", "--type", "task")
	dep1 := bdCreate(t, bd, dir, "depending one", "--type", "task")
	dep2 := bdCreate(t, bd, dir, "depending two", "--type", "task")
	bdDep(t, bd, dir, "add", dep1.ID, blkA.ID)
	bdDep(t, bd, dir, "add", dep2.ID, blkA.ID)

	single := bdDepListJSONArray(t, bd, dir, dep1.ID)
	multi := bdDepListJSONArray(t, bd, dir, dep1.ID, dep2.ID)

	if len(single) == 0 {
		t.Fatalf("single-id dep list --json returned no records")
	}
	if len(multi) == 0 {
		t.Fatalf("multi-id dep list --json returned no records")
	}

	// The canonical shape is the issue-object shape (single-arg produces it).
	// Both calls must emit records carrying the issue keys, and NEITHER may
	// leak the raw edge-record keys.
	assertIssueShape := func(label string, recs []map[string]interface{}) {
		for i, r := range recs {
			if _, ok := r["id"]; !ok {
				t.Errorf("%s[%d]: missing canonical issue key %q; got keys %v", label, i, "id", sortedKeys(r))
			}
			if _, ok := r["title"]; !ok {
				t.Errorf("%s[%d]: missing canonical issue key %q; got keys %v", label, i, "title", sortedKeys(r))
			}
			if _, ok := r["issue_id"]; ok {
				t.Errorf("%s[%d]: leaks raw edge-record key %q; got keys %v", label, i, "issue_id", sortedKeys(r))
			}
			if _, ok := r["depends_on_id"]; ok {
				t.Errorf("%s[%d]: leaks raw edge-record key %q; got keys %v", label, i, "depends_on_id", sortedKeys(r))
			}
		}
	}

	assertIssueShape("single", single)
	assertIssueShape("multi", multi)
}

// bdDepListJSONArray runs `bd dep list <args...> --json` and parses the top-level
// JSON array into a slice of generic objects.
func bdDepListJSONArray(t *testing.T, bd, dir string, args ...string) []map[string]interface{} {
	t.Helper()
	fullArgs := append([]string{"dep", "list"}, args...)
	fullArgs = append(fullArgs, "--json")
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd dep list --json %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	s := bytes.TrimSpace(stdout.Bytes())
	start := bytes.IndexByte(s, '[')
	if start < 0 {
		t.Fatalf("no JSON array in dep list output: %s", s)
	}
	var recs []map[string]interface{}
	if err := json.Unmarshal(s[start:], &recs); err != nil {
		t.Fatalf("parse dep list JSON array: %v\n%s", err, s)
	}
	return recs
}
