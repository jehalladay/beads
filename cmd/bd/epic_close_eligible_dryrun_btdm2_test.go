//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-btdm2: `bd epic close-eligible --json` must report the ACTUAL dryRun arg
// in the emitted dict, not a hardcoded true. The empty-eligible leg
// (epic.go:191) previously passed a literal `true`, so a plain
// `bd epic close-eligible --json` (NO --dry-run) that found nothing eligible
// falsely reported {"dry_run": true}, indistinguishable from a real dry-run.
// The dry_run field must track the invocation: false for a live run, true only
// when --dry-run is set.
func TestEpicCloseEligibleJSONDryRunFlag_btdm2(t *testing.T) {
	prev := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prev })

	ineligible := &types.EpicStatus{
		Epic:             &types.Issue{ID: "ep-1", Title: "Epic One", Status: types.StatusOpen},
		EligibleForClose: false,
	}
	eligible := &types.EpicStatus{
		Epic:             &types.Issue{ID: "ep-2", Title: "Epic Two", Status: types.StatusOpen},
		TotalChildren:    1,
		ClosedChildren:   1,
		EligibleForClose: true,
	}

	dryRunOf := func(t *testing.T, label, out string) bool {
		t.Helper()
		s := strings.TrimSpace(out)
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("%s: expected a JSON object, got: %s", label, s)
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(s[start:]), &obj); err != nil {
			t.Fatalf("%s: not a JSON object: %v\n%s", label, err, s)
		}
		v, ok := obj["dry_run"]
		if !ok {
			t.Fatalf("%s: missing dry_run key; got %v", label, keysOf(obj))
		}
		b, ok := v.(bool)
		if !ok {
			t.Fatalf("%s: dry_run is not a bool: %T (%v)", label, v, v)
		}
		return b
	}

	// (1) THE BUG: nothing eligible, dryRun=false → dry_run MUST be false.
	out1 := captureStdout(t, func() error {
		return renderEpicCloseEligible([]*types.EpicStatus{ineligible}, false, func(string) error { return nil }, nil)
	})
	if dryRunOf(t, "nothing-eligible-live", out1) {
		t.Errorf("nothing-eligible + dryRun=false: dry_run reported true (beads-btdm2 regression) — a live close-eligible with nothing eligible mislabels itself as a dry run")
	}

	// (2) nothing eligible, dryRun=true → dry_run MUST be true (no false negative).
	out2 := captureStdout(t, func() error {
		return renderEpicCloseEligible([]*types.EpicStatus{ineligible}, true, func(string) error { return nil }, nil)
	})
	if !dryRunOf(t, "nothing-eligible-dryrun", out2) {
		t.Errorf("nothing-eligible + dryRun=true: dry_run reported false — should honor the --dry-run flag")
	}

	// (3) eligible + dryRun=true → dry_run MUST be true (the guarded dry-run leg).
	out3 := captureStdout(t, func() error {
		return renderEpicCloseEligible([]*types.EpicStatus{eligible}, true, func(string) error { return nil }, nil)
	})
	if !dryRunOf(t, "eligible-dryrun", out3) {
		t.Errorf("eligible + dryRun=true: dry_run reported false — dry-run leg must report true")
	}

	// (4) eligible + dryRun=false, actually closed → dry_run MUST be false.
	out4 := captureStdout(t, func() error {
		return renderEpicCloseEligible([]*types.EpicStatus{eligible}, false, func(string) error { return nil }, func() error { return nil })
	})
	if dryRunOf(t, "actually-closed", out4) {
		t.Errorf("eligible + dryRun=false (closed): dry_run reported true — a real close must not report dry_run")
	}
}
