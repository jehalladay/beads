//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestDuplicateRejectsDupOfDupChainAndCycle is the teeth for beads-wqrfi: bd
// duplicate must reject marking an issue as a duplicate of a canonical that is
// ITSELF a closed duplicate. Previously the only guards were self-ref +
// canonical-existence, so:
//   - CHAIN: `duplicate LEAF --of MID` where MID was already closed-as-dup-of-ROOT
//     left LEAF pointing at a dead canonical (MID), not the live ROOT.
//   - CYCLE: `duplicate A --of B` then `duplicate B --of A` closed both, each
//     naming the other → tracing "the real issue" loops forever.
//
// The guard: canonical closed AND has an outgoing "duplicates" edge → reject.
func TestDuplicateRejectsDupOfDupChainAndCycle(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dcc")

	t.Run("dup_of_dup_chain_rejected", func(t *testing.T) {
		root := bdCreate(t, bd, dir, "Root live canonical", "--type", "bug")
		mid := bdCreate(t, bd, dir, "Mid duplicate", "--type", "bug")
		leaf := bdCreate(t, bd, dir, "Leaf", "--type", "bug")

		// MID → ROOT (ROOT open): legal, closes MID.
		bdDuplicate(t, bd, dir, mid.ID, "--of", root.ID)

		// LEAF → MID (MID now closed-as-dup): must be REJECTED.
		out := bdDuplicateFail(t, bd, dir, leaf.ID, "--of", mid.ID)
		if !strings.Contains(out, "itself a closed duplicate") {
			t.Errorf("expected chain-rejection message, got: %s", out)
		}
	})

	t.Run("mutual_cycle_rejected", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "Cycle A", "--type", "bug")
		b := bdCreate(t, bd, dir, "Cycle B", "--type", "bug")

		// A → B (B open): legal, closes A.
		bdDuplicate(t, bd, dir, a.ID, "--of", b.ID)

		// B → A (A now closed-as-dup-of-B): must be REJECTED (would form a cycle).
		out := bdDuplicateFail(t, bd, dir, b.ID, "--of", a.ID)
		if !strings.Contains(out, "itself a closed duplicate") {
			t.Errorf("expected cycle-rejection message, got: %s", out)
		}
	})

	t.Run("normal_closed_canonical_still_allowed", func(t *testing.T) {
		// A plain closed issue that is NOT a duplicate is still a valid canonical.
		canon := bdCreate(t, bd, dir, "Closed but real canonical", "--type", "bug")
		dupe := bdCreate(t, bd, dir, "A dupe", "--type", "bug")
		if _, err := bdRunWithFlockRetry(t, bd, dir, "close", canon.ID); err != nil {
			t.Fatalf("close canonical: %v", err)
		}
		// canon is closed but has NO outgoing duplicates edge → must be accepted.
		bdDuplicate(t, bd, dir, dupe.ID, "--of", canon.ID)
	})
}
