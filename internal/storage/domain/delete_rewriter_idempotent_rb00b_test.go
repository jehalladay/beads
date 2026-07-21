package domain

import "testing"

// beads-rb00b: the tombstone rewrite was hoisted into the storage-layer delete
// (issueops) so bulk paths (gc/purge/prune/mol-burn) tombstone like single
// delete. Because the higher cmd layers (single/batch) already tombstone before
// the storage delete runs, the shared rewriter MUST be idempotent — a second
// pass over "[deleted:id]" must NOT re-wrap it into "[deleted:[deleted:id]]".
func TestDeletedReferenceRewriter_Idempotent_rb00b(t *testing.T) {
	rewrite := DeletedReferenceRewriter("bd-abc")

	// First pass: live ref → tombstone, changed=true.
	got, ok := rewrite("was blocked by bd-abc now")
	want := "was blocked by [deleted:bd-abc] now"
	if !ok || got != want {
		t.Fatalf("first pass = %q,%v want %q,true", got, ok, want)
	}

	// Second pass over the already-tombstoned text: no change, ok=false.
	got2, ok2 := rewrite(got)
	if ok2 {
		t.Errorf("second pass reported changed=true (not idempotent): %q", got2)
	}
	if got2 != want {
		t.Errorf("second pass mutated tombstone: got %q, want %q (unchanged)", got2, want)
	}

	// Mixed live + already-tombstoned: only the live one is rewritten, the
	// existing tombstone survives intact.
	mixed, okM := rewrite("[deleted:bd-abc] and bd-abc")
	wantMixed := "[deleted:bd-abc] and [deleted:bd-abc]"
	if !okM || mixed != wantMixed {
		t.Errorf("mixed pass = %q,%v want %q,true", mixed, okM, wantMixed)
	}

	// A pre-tombstoned-only input returns unchanged with ok=false.
	pre, okP := rewrite("see [deleted:bd-abc]")
	if okP || pre != "see [deleted:bd-abc]" {
		t.Errorf("pre-tombstoned pass = %q,%v want unchanged,false", pre, okP)
	}
}
