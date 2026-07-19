package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-5fv3: SwarmAnalysis (swarm.go:35/38/39) has three append-only slices —
// ReadyFronts, Warnings, Errors — declared []T with no omitempty and NOT
// initialized where the struct is constructed (analyzeEpicForSwarm :221). A
// nil slice marshals to JSON null, so `bd swarm validate/create --json` emits
// e.g. "errors":null / "ready_fronts":null on the empty case, breaking a
// consumer that expects an array (must special-case null vs []). Contrast:
// getSwarmStatus (:689) explicitly inits Completed/Active/Ready/Blocked to
// []StatusIssue{} and emits [] — proving [] is the intended contract, not
// null. tamf nil-slice class, sibling of guib (diff.go). The fix inits all
// three slices at construction to []T{}.
//
// Faithful teeth: marshal the *actual* analysis returned by analyzeEpicForSwarm
// (the same value outputJSON serializes at swarm.go:201) and assert the raw
// JSON carries "[]" not "null" for each field in a scenario where it is empty.
// A childless epic is the exact bead repro (errors + ready_fronts both empty);
// a clean chain leaves warnings empty.
func TestSwarmAnalysisJSONEmptySlicesAreArrays_5fv3(t *testing.T) {
	ctx := context.Background()
	epic := &types.Issue{ID: "epic", Title: "Epic"}

	hasNull := func(t *testing.T, raw []byte, field string) {
		t.Helper()
		// Match the exact "field":null token the encoder emits for a nil slice.
		if strings.Contains(string(raw), `"`+field+`":null`) {
			t.Errorf("%q serialized to null (nil slice); want [] for a stable --json array contract\nJSON: %s", field, raw)
		}
	}

	t.Run("childless epic: errors + ready_fronts must be [] not null", func(t *testing.T) {
		f := newFakeSwarmStore()
		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		hasNull(t, raw, "errors")
		hasNull(t, raw, "ready_fronts")
	})

	t.Run("clean chain: warnings must be [] not null", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{
			{ID: "a", Title: "a", Status: types.StatusOpen},
			{ID: "b", Title: "b", Status: types.StatusClosed},
		}
		f.depRecords["a"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepParentChild}}
		f.depRecords["b"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "a", Type: types.DepBlocks},
		}
		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		if len(a.Warnings) != 0 {
			t.Fatalf("precondition: clean chain should have no warnings, got %v", a.Warnings)
		}
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		hasNull(t, raw, "warnings")
	})
}
