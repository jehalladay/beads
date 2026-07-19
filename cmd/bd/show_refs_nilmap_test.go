//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestShowRefsJSONNoDependentsEmitsArrayNotNull is the end-to-end teeth for the
// `bd show <id> --refs --json` map-value nil-slice contract (beads-d995).
//
// showIssueRefs (show_refs.go) builds allRefs[issueID] = refs where refs comes
// from GetDependentsWithMetadata, which returns a nil []*IssueWithDependencyMetadata
// when the issue has NO dependents. outputJSON(allRefs) then marshals that nil
// map VALUE as `null`, so the common no-refs case emits {"<id>":null,...} while
// an issue WITH a dependent emits {"<id>":[{...}],...}. A consumer doing
// data["<id>"] then iterating hits null on the majority case (jq .[][] errors on
// null; python None is not iterable).
//
// This is the nil-slice→null class (siblings 5fv3/guib/tamf/036h), MAP-VALUE
// variant: the nil slice is a value inside a map rather than a top-level field.
// The fix inits refs to []*types.IssueWithDependencyMetadata{} when nil before
// the map assignment, so the array contract holds for both cases.
func TestShowRefsJSONNoDependentsEmitsArrayNotNull(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rf")

	// An issue with NO dependents — the exact bead repro.
	lonely := bdCreate(t, bd, dir, "No dependents", "--type", "task")

	out := bdShowRaw(t, bd, dir, lonely.ID, "--refs", "--json")
	s := strings.TrimSpace(out)
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		t.Fatalf("no JSON in `show --refs --json` output: %s", s)
	}
	s = s[start:]

	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("`show --refs --json` output is not a JSON object: %v\nraw: %s", err, s)
	}

	raw, ok := m[lonely.ID]
	if !ok {
		t.Fatalf("expected a %q entry in --refs --json map, got: %s", lonely.ID, s)
	}
	if strings.TrimSpace(string(raw)) == "null" {
		t.Fatalf("`show --refs --json` for an issue with no dependents emitted null map value; want [] for a stable array contract (beads-d995)\nraw: %s", s)
	}
	// It must be a JSON array (possibly empty), never null.
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("--refs --json map value is not a JSON array: %v\nvalue: %s", err, raw)
	}
	if len(arr) != 0 {
		t.Errorf("precondition: an issue with no dependents should have 0 refs, got %d", len(arr))
	}
}
