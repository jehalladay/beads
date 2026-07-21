//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// beads-rb00b: bd purge deletes ephemeral (wisp) issues via the storage-layer
// wisp-delete path (deleteWispBatch), which — like gc-decay for regular issues —
// bypassed the tombstone rewriter. A surviving issue that referenced a purged
// wisp in prose kept the raw id, dangling at a nonexistent issue. The rewrite is
// now hoisted into the storage delete (issueops) and the DoltStore wisp-delete
// paths, so purge tombstones connected references like `bd delete` does.
func TestEmbeddedPurgeTombstonesTextRefs_rb00b(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "PG")

	// A durable survivor references an ephemeral (wisp) issue in its description.
	keeper := bdCreate(t, bd, dir, "keeper", "--type", "task")
	wisp := bdCreate(t, bd, dir, "ephemeral work", "--ephemeral")
	bdUpdate(t, bd, dir, keeper.ID, "--description", "spawned from "+wisp.ID)
	bdDep(t, bd, dir, "add", keeper.ID, wisp.ID)

	// Close then purge the ephemeral.
	bdClose(t, bd, dir, wisp.ID)
	out := bdPurge(t, bd, dir, "--force")
	if !strings.Contains(out, "Purged") {
		t.Fatalf("purge did not report a delete:\n%s", out)
	}

	got := bdShow(t, bd, dir, keeper.ID)
	wantTomb := "[deleted:" + wisp.ID + "]"
	if !strings.Contains(got.Description, wantTomb) {
		t.Errorf("purge left a dangling text ref: keeper description = %q, want it to contain %q",
			got.Description, wantTomb)
	}
}
