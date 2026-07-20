//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedDuplicateRejectsDupOfDupChainAndCycle is the PROXIED twin of the
// beads-wqrfi guard: the proxied bd duplicate path (runLinkAndCloseProxied) must
// also reject a canonical that is itself a closed duplicate (chain + mutual
// cycle), matching the direct path.
func TestProxiedDuplicateRejectsDupOfDupChainAndCycle(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("dup_of_dup_chain_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pcc")
		root := bdProxiedCreate(t, bd, p.dir, "Root live canonical", "--type", "bug")
		mid := bdProxiedCreate(t, bd, p.dir, "Mid duplicate", "--type", "bug")
		leaf := bdProxiedCreate(t, bd, p.dir, "Leaf", "--type", "bug")

		if _, se, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", mid.ID, "--of", root.ID); err != nil {
			t.Fatalf("mid→root should succeed: %v (%s)", err, se)
		}
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", leaf.ID, "--of", mid.ID)
		if err == nil {
			t.Fatalf("leaf→mid (mid closed-as-dup) must be rejected, got success (stdout=%q)", stdout)
		}
		if !strings.Contains(stdout+stderr, "itself a closed duplicate") {
			t.Errorf("expected chain-rejection message, got stdout=%q stderr=%q", stdout, stderr)
		}
	})

	t.Run("mutual_cycle_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pcy")
		a := bdProxiedCreate(t, bd, p.dir, "Cycle A", "--type", "bug")
		b := bdProxiedCreate(t, bd, p.dir, "Cycle B", "--type", "bug")

		if _, se, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", a.ID, "--of", b.ID); err != nil {
			t.Fatalf("a→b should succeed: %v (%s)", err, se)
		}
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", b.ID, "--of", a.ID)
		if err == nil {
			t.Fatalf("b→a (a closed-as-dup-of-b) must be rejected, got success (stdout=%q)", stdout)
		}
		if !strings.Contains(stdout+stderr, "itself a closed duplicate") {
			t.Errorf("expected cycle-rejection message, got stdout=%q stderr=%q", stdout, stderr)
		}
	})
}
