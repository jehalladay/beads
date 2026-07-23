//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMergeSlotCreateIdempotent_nqp3z pins the beads-nqp3z fix: `bd merge-slot
// create` is idempotent (MergeSlotCreateImpl returns the existing slot without
// writing when one already exists — TestMergeSlotCreateImpl_Idempotent proves 0
// CreateIssue calls on that path), so a 2nd create is a no-op. The verb used to
// unconditionally report "✓ Created merge slot" (text) / JSON with no
// created-vs-existing signal — a false-success on a no-op. The fix pre-reads the
// slot and reports created:true only on a genuine write, created:false on the
// no-op (and "already present" text). Same class as beads-nnsso (bd link
// "added"→"unchanged").
//
// Mutation check: drop the store.GetIssue(slotID) precheck in runMergeSlotCreate
// (hardcode created:true) and second_create_reports_not_created goes RED.
func TestMergeSlotCreateIdempotent_nqp3z(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ms")

	type createResult struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Created bool   `json:"created"`
	}
	runCreateJSON := func(t *testing.T) createResult {
		t.Helper()
		cmd := exec.Command(bd, "merge-slot", "create", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("merge-slot create --json failed: %v\n%s", err, out)
		}
		s := strings.TrimSpace(string(out))
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("no JSON object in create output: %s", s)
		}
		var res createResult
		if err := json.Unmarshal([]byte(s[start:]), &res); err != nil {
			t.Fatalf("parse create JSON: %v\nstdout: %s", err, s)
		}
		return res
	}

	// First create: a genuine new slot → created:true.
	first := runCreateJSON(t)
	if !first.Created {
		t.Fatalf("first create: expected created:true, got %+v", first)
	}
	if first.ID == "" {
		t.Errorf("first create: expected a slot id, got %+v", first)
	}

	// Second create: idempotent no-op → created:false (this is the fix).
	t.Run("second_create_reports_not_created", func(t *testing.T) {
		second := runCreateJSON(t)
		if second.Created {
			t.Fatalf("second (duplicate) create: expected created:false, got %+v", second)
		}
		if second.ID != first.ID {
			t.Errorf("second create: expected same slot id %s, got %+v", first.ID, second)
		}
	})
}
