//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-qos4p: `bd epic close-eligible --json` must emit a STABLE top-level
// shape across all non-error outcomes (nothing-eligible, dry-run,
// actually-closed) — previously it flipped between a bare ARRAY (empty/dry-run)
// and a DICT {closed,count} (actually-closed), so a --json consumer could not
// statically type it. All three now emit {eligible, closed, count, dry_run}.
func TestEpicCloseEligibleJSONShapeStable_qos4p(t *testing.T) {
	prev := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prev })

	eligible := &types.EpicStatus{
		Epic:             &types.Issue{ID: "ep-1", Title: "Epic One", Status: types.StatusOpen},
		TotalChildren:    1,
		ClosedChildren:   1,
		EligibleForClose: true,
	}
	ineligible := &types.EpicStatus{
		Epic:             &types.Issue{ID: "ep-2", Title: "Epic Two", Status: types.StatusOpen},
		EligibleForClose: false,
	}

	wantKeys := []string{"eligible", "closed", "count", "dry_run"}
	assertShape := func(t *testing.T, label, out string) {
		t.Helper()
		s := strings.TrimSpace(out)
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("%s: expected a JSON OBJECT (stable dict shape), got: %s", label, s)
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(s[start:]), &obj); err != nil {
			t.Fatalf("%s: not a JSON object: %v\n%s", label, err, s)
		}
		for _, k := range wantKeys {
			if _, ok := obj[k]; !ok {
				t.Errorf("%s: missing stable key %q (shape-flip regression, beads-qos4p); got keys %v", label, k, keysOf(obj))
			}
		}
	}

	// (1) nothing eligible
	out1 := captureStdout(t, func() error {
		return renderEpicCloseEligible([]*types.EpicStatus{ineligible}, false, func(string) error { return nil }, nil)
	})
	assertShape(t, "nothing-eligible", out1)

	// (2) dry-run with an eligible epic
	out2 := captureStdout(t, func() error {
		return renderEpicCloseEligible([]*types.EpicStatus{eligible}, true, func(string) error { return nil }, nil)
	})
	assertShape(t, "dry-run", out2)

	// (3) actually-closed (closeFn succeeds)
	out3 := captureStdout(t, func() error {
		return renderEpicCloseEligible([]*types.EpicStatus{eligible}, false, func(string) error { return nil }, func() error { return nil })
	})
	assertShape(t, "actually-closed", out3)
}
