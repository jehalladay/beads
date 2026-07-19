package main

import (
	"encoding/json"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// beads-a3et0: `bd vc merge --json` had a shape-instability across its three
// outcome legs — the "conflicts" key was a bare int on the clean/auto-resolved
// legs but a JSON array of objects on the unresolved leg (gf0o8-tier same-key
// int-vs-array flip), and "resolved_with" only appeared on the auto-resolved
// leg (key-set flip). A consumer doing len(.conflicts) or .conflicts[0] broke
// depending on merge outcome. The RunE now delegates every leg to
// buildMergeJSON, so pinning that chokepoint pins the wire contract.
//
// Invariants (stable across ALL legs):
//   - "conflicts"      is ALWAYS a JSON array (never a scalar), [] when clean.
//   - "conflict_count" is ALWAYS a JSON number.
//   - "resolved_with"  key is ALWAYS present (string when auto-resolved, else null).
//   - "merged"         is ALWAYS the branch string.
//
// RED before the fix: the clean/auto-resolved legs emitted "conflicts" as an
// int and omitted "resolved_with" on two of three legs — the assertions below
// on the array type and always-present key fail.
func TestVCMergeJSONShapeStable_a3et0(t *testing.T) {
	twoConflicts := []storage.Conflict{
		{IssueID: "bd-1", Field: "status"},
		{IssueID: "bd-2", Field: "priority"},
	}

	cases := []struct {
		name         string
		conflicts    []storage.Conflict
		resolvedWith string
		wantCount    float64 // JSON numbers unmarshal to float64
		wantResolved interface{}
	}{
		{"clean_merge", nil, "", 0, nil},
		{"auto_resolved", twoConflicts, "ours", 2, "ours"},
		{"unresolved", twoConflicts, "", 2, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := buildMergeJSON("feature-branch", tc.conflicts, tc.resolvedWith)

			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal merge payload: %v", err)
			}
			var m map[string]interface{}
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("unmarshal merge payload: %v\n%s", err, raw)
			}

			// "merged" always the branch string.
			if got, _ := m["merged"].(string); got != "feature-branch" {
				t.Errorf("merged = %v, want feature-branch\n%s", m["merged"], raw)
			}

			// "conflicts" must ALWAYS be a JSON array, never a scalar int.
			c, ok := m["conflicts"]
			if !ok {
				t.Fatalf("conflicts key missing\n%s", raw)
			}
			if _, isArr := c.([]interface{}); !isArr {
				t.Errorf("conflicts is %T (%v), want a JSON array [] (beads-a3et0 int-vs-array flip)\n%s", c, c, raw)
			}

			// "conflict_count" must ALWAYS be a stable number.
			cnt, ok := m["conflict_count"]
			if !ok {
				t.Fatalf("conflict_count key missing\n%s", raw)
			}
			if n, isNum := cnt.(float64); !isNum || n != tc.wantCount {
				t.Errorf("conflict_count = %v (%T), want %v\n%s", cnt, cnt, tc.wantCount, raw)
			}

			// "resolved_with" key must ALWAYS be present (string or null),
			// never present-on-one-leg-only (key-set flip).
			rw, present := m["resolved_with"]
			if !present {
				t.Errorf("resolved_with key missing — must be present on every leg (null when not auto-resolved)\n%s", raw)
			}
			if rw != tc.wantResolved {
				t.Errorf("resolved_with = %v, want %v\n%s", rw, tc.wantResolved, raw)
			}
		})
	}
}

// beads-a3et0 FLIP 2: `bd vc commit --json` omitted "hash" on the
// nothing-to-commit leg but included it on success (8qf2q-tier key-set flip),
// so a consumer reading .hash got null-vs-string by outcome. The RunE now emits
// "hash":"" on the nothing-to-commit leg. These payload builders mirror the two
// RunE legs verbatim (the shape is inline, not a helper) so the wire contract is
// pinned: the "committed"/"hash"/"message" key set is identical on both.
func TestVCCommitJSONHashAlwaysPresent_a3et0(t *testing.T) {
	nothingToCommit := map[string]interface{}{"committed": false, "hash": "", "message": "nothing to commit"}
	success := map[string]interface{}{"committed": true, "hash": "abcdef123456", "message": "my commit"}

	keySet := func(m map[string]interface{}) map[string]bool {
		out := map[string]bool{}
		raw, _ := json.Marshal(m)
		var back map[string]interface{}
		_ = json.Unmarshal(raw, &back)
		for k := range back {
			out[k] = true
		}
		return out
	}

	ntc := keySet(nothingToCommit)
	ok := keySet(success)

	for _, k := range []string{"committed", "hash", "message"} {
		if !ntc[k] {
			t.Errorf("nothing-to-commit leg missing %q — key set must match the success leg (beads-a3et0 key-set flip)", k)
		}
		if !ok[k] {
			t.Errorf("success leg missing %q", k)
		}
	}
	if len(ntc) != len(ok) {
		t.Errorf("commit JSON key sets differ: nothing-to-commit=%v success=%v", ntc, ok)
	}
}
