//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// beads-qh4jx: the single-force `bd delete <id> --force --json` path diverged
// from the batch/proxied contract in three ways:
//  1. LOAD-BEARING type flip — "deleted" was a bare STRING on single delete but
//     an ARRAY on batch, so a --json consumer parsing d["deleted"] broke between
//     1 and N args.
//  2. Missing keys — deleted_count, labels_removed, events_removed,
//     orphaned_issues were absent on the single path.
//  3. SILENT count-drop — an issue's labels+events ARE removed (ON DELETE
//     CASCADE, migrations 0003/0005) but the single path reported NEITHER, so a
//     consumer/operator saw "0 removed" while data was actually deleted.
//
// End-to-end through the REAL `bd delete` subprocess (embedded/DIRECT, NOT
// proxied). MUTATION-VERIFIED: reverting the delete.go output block regresses
// the parity (deleted becomes a string; labels/events/orphans drop).
func TestEmbeddedDeleteSingleJSONParity_qh4jx(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// decodeDeleteJSON pulls the JSON object out of `bd delete --json` output.
	decodeDeleteJSON := func(t *testing.T, out string) map[string]any {
		t.Helper()
		start := strings.Index(out, "{")
		if start < 0 {
			t.Fatalf("no JSON object in delete output:\n%s", out)
		}
		var m map[string]any
		dec := json.NewDecoder(strings.NewReader(out[start:]))
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("cannot parse delete JSON: %v\n%s", err, out)
		}
		return m
	}

	// The single-force --json output carries the SAME 7-key shape as the batch
	// path: "deleted" is an ARRAY, plus deleted_count / labels_removed /
	// events_removed / orphaned_issues are present — and the label/event counts
	// are NON-ZERO (the silent count-drop is closed).
	t.Run("single_force_json_matches_batch_shape", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "dp")
		target := bdCreate(t, bd, dir, "dp target", "--type", "task", "--labels", "alpha,beta")
		// A dependent that will be orphaned by force-deleting the target.
		dependent := bdCreate(t, bd, dir, "dp dependent", "--type", "task")
		bdDepAdd(t, bd, dir, dependent.ID, target.ID) // dependent blocks-on target
		// Generate events on the target (create + these updates append events).
		bdUpdate(t, bd, dir, target.ID, "--priority", "1")
		bdUpdate(t, bd, dir, target.ID, "--status", "in_progress")

		out := bdDelete(t, bd, dir, target.ID, "--force", "--json")
		m := decodeDeleteJSON(t, out)

		// 1. LOAD-BEARING: "deleted" must be an ARRAY, not a bare string.
		deleted, ok := m["deleted"].([]any)
		if !ok {
			t.Fatalf("`deleted` must be an array (batch-parity), got %T: %v (qh4jx type-flip)", m["deleted"], m["deleted"])
		}
		if len(deleted) != 1 || deleted[0] != target.ID {
			t.Errorf("`deleted` = %v, want [%s]", deleted, target.ID)
		}

		// 2. deleted_count present and = 1.
		if cnt, ok := m["deleted_count"].(float64); !ok || cnt != 1 {
			t.Errorf("deleted_count = %v (%T), want 1 (qh4jx missing key)", m["deleted_count"], m["deleted_count"])
		}

		// 3. SILENT count-drop closed: labels + events reported and NON-ZERO
		//    (two labels added; create + 2 updates generate events).
		labels, ok := m["labels_removed"].(float64)
		if !ok {
			t.Fatalf("labels_removed missing/not a number: %T %v (qh4jx count-drop)", m["labels_removed"], m["labels_removed"])
		}
		if labels != 2 {
			t.Errorf("labels_removed = %v, want 2 (alpha,beta) — silent count-drop", labels)
		}
		events, ok := m["events_removed"].(float64)
		if !ok {
			t.Fatalf("events_removed missing/not a number: %T %v (qh4jx count-drop)", m["events_removed"], m["events_removed"])
		}
		if events < 1 {
			t.Errorf("events_removed = %v, want >= 1 (create + updates emit events) — silent count-drop", events)
		}

		// orphaned_issues present and contains the surviving dependent.
		orphans, ok := m["orphaned_issues"].([]any)
		if !ok {
			t.Fatalf("orphaned_issues missing/not an array: %T %v (qh4jx missing key)", m["orphaned_issues"], m["orphaned_issues"])
		}
		found := false
		for _, o := range orphans {
			if o == dependent.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("orphaned_issues = %v, want to contain the surviving dependent %s", orphans, dependent.ID)
		}

		// dependencies_removed + references_updated remain (regression guard).
		if _, ok := m["dependencies_removed"]; !ok {
			t.Errorf("dependencies_removed key dropped (regression)")
		}
		if _, ok := m["references_updated"]; !ok {
			t.Errorf("references_updated key dropped (regression)")
		}

		// The dependent survives (force orphans, not cascades).
		got := bdShow(t, bd, dir, dependent.ID)
		if got.ID != dependent.ID {
			t.Errorf("dependent %s should survive a force delete of the target", dependent.ID)
		}
	})

	// Cross-check: the BATCH path (2+ args) already emits the array shape; this
	// pins the two paths to the SAME key set so a future single-path change
	// can't silently re-diverge.
	t.Run("single_and_batch_json_key_sets_match", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "dq")

		single := bdCreate(t, bd, dir, "dq single", "--type", "task")
		singleOut := bdDelete(t, bd, dir, single.ID, "--force", "--json")
		singleKeys := keySet(decodeDeleteJSON(t, singleOut))

		b1 := bdCreate(t, bd, dir, "dq b1", "--type", "task")
		b2 := bdCreate(t, bd, dir, "dq b2", "--type", "task")
		batchOut := bdDelete(t, bd, dir, b1.ID, b2.ID, "--force", "--json")
		batchKeys := keySet(decodeDeleteJSON(t, batchOut))

		for k := range batchKeys {
			if !singleKeys[k] {
				t.Errorf("single-delete --json is missing batch key %q (contract divergence — qh4jx)", k)
			}
		}
		for k := range singleKeys {
			if !batchKeys[k] {
				t.Errorf("single-delete --json has extra key %q absent from batch (contract divergence — qh4jx)", k)
			}
		}
	})
}

func keySet(m map[string]any) map[string]bool {
	s := make(map[string]bool, len(m))
	for k := range m {
		s[k] = true
	}
	return s
}
