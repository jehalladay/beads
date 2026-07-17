// Package storage — merge_slot_dequeue_test.go
//
// Regression tests for beads-1qpu: the merge-slot waiters queue must be
// dequeued correctly. Two bugs were fixed:
//
//	(1) a reentrant acquire by the CURRENT holder (with --wait) must NOT queue
//	    the holder behind itself — it is an idempotent no-op (already held).
//	(2) a waiter that acquires the now-open slot must be REMOVED from the
//	    waiters list; release must not resurrect a stale holder entry.
//
// These use a minimal transactional fake distinct from the fakeSlotStore in
// merge_slot_test.go (which covers the non-transactional helpers). Names are
// prefixed dq* to avoid collisions.
package storage

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// dqTx is a minimal Transaction that reads/writes a single in-memory slot
// issue — enough to drive MergeSlotAcquireImpl / MergeSlotReleaseImpl.
type dqTx struct {
	Transaction
	slot *types.Issue
}

func (p *dqTx) GetIssue(_ context.Context, _ string) (*types.Issue, error) { return p.slot, nil }

func (p *dqTx) UpdateIssue(_ context.Context, _ string, updates map[string]interface{}, _ string) error {
	if v, ok := updates["status"]; ok {
		p.slot.Status = v.(types.Status)
	}
	if v, ok := updates["metadata"]; ok {
		p.slot.Metadata = json.RawMessage(v.(string))
	}
	return nil
}

// dqStore embeds Storage and drives RunInTransaction against a single slot.
type dqStore struct {
	Storage
	slot *types.Issue
}

func (p *dqStore) GetConfig(_ context.Context, _ string) (string, error) { return "", ErrNotFound }

func (p *dqStore) RunInTransaction(ctx context.Context, _ string, fn func(tx Transaction) error) error {
	return fn(&dqTx{slot: p.slot})
}

func dqNewSlot(status types.Status, meta slotMeta) *dqStore {
	b, _ := json.Marshal(meta)
	return &dqStore{slot: &types.Issue{ID: "bd-merge-slot", Status: status, Metadata: b}}
}

func dqContains(ss []string, x string) bool {
	for _, s := range ss {
		if s == x {
			return true
		}
	}
	return false
}

// BUG-1: the current holder re-acquiring with wait=true must be an idempotent
// no-op, NOT a self-queue behind itself.
func TestMergeSlotAcquire_ReentrantHolderWaitIsNoOp(t *testing.T) {
	ctx := context.Background()
	s := dqNewSlot(types.StatusInProgress, slotMeta{Holder: "X", Waiters: []string{"W1"}})

	res, err := MergeSlotAcquireImpl(ctx, s, "X", "actor", true)
	if err != nil {
		t.Fatalf("MergeSlotAcquireImpl() err = %v, want nil", err)
	}
	if res.Waiting {
		t.Fatalf("reentrant holder acquire reported Waiting=true; must be idempotent-held")
	}
	if !res.Acquired {
		t.Fatalf("reentrant holder acquire should report Acquired=true (already holds it)")
	}
	if res.Holder != "X" {
		t.Fatalf("Holder = %q, want X", res.Holder)
	}
	final := parseSlotMeta(s.slot)
	if dqContains(final.Waiters, "X") {
		t.Fatalf("holder X was queued as a waiter behind itself: Waiters=%v", final.Waiters)
	}
	// The pre-existing unrelated waiter must be preserved untouched.
	if len(final.Waiters) != 1 || final.Waiters[0] != "W1" {
		t.Fatalf("pre-existing waiters mutated: got %v, want [W1]", final.Waiters)
	}
	if final.Holder != "X" {
		t.Fatalf("holder changed on reentrant acquire: got %q, want X", final.Holder)
	}
}

// BUG-2: a waiter that acquires the now-open slot must be removed from Waiters.
func TestMergeSlotAcquire_AcquirerRemovedFromWaiters(t *testing.T) {
	ctx := context.Background()
	s := dqNewSlot(types.StatusOpen, slotMeta{Waiters: []string{"Y", "Z"}})

	res, err := MergeSlotAcquireImpl(ctx, s, "Y", "actor", false)
	if err != nil {
		t.Fatalf("MergeSlotAcquireImpl() err = %v, want nil", err)
	}
	if !res.Acquired {
		t.Fatalf("Y should have acquired the open slot")
	}
	final := parseSlotMeta(s.slot)
	if final.Holder != "Y" {
		t.Fatalf("Holder = %q, want Y", final.Holder)
	}
	if dqContains(final.Waiters, "Y") {
		t.Fatalf("acquirer Y still listed as a waiter: Waiters=%v", final.Waiters)
	}
	// Other waiters remain, order preserved (FIFO stability).
	if len(final.Waiters) != 1 || final.Waiters[0] != "Z" {
		t.Fatalf("remaining waiters wrong: got %v, want [Z]", final.Waiters)
	}
}

// A non-waiter acquiring an open slot leaves the existing queue untouched.
func TestMergeSlotAcquire_NonWaiterAcquireKeepsQueue(t *testing.T) {
	ctx := context.Background()
	s := dqNewSlot(types.StatusOpen, slotMeta{Waiters: []string{"A", "B"}})

	res, err := MergeSlotAcquireImpl(ctx, s, "C", "actor", false)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !res.Acquired {
		t.Fatalf("C should have acquired the open slot")
	}
	final := parseSlotMeta(s.slot)
	if final.Holder != "C" {
		t.Fatalf("Holder = %q, want C", final.Holder)
	}
	if len(final.Waiters) != 2 || final.Waiters[0] != "A" || final.Waiters[1] != "B" {
		t.Fatalf("waiters changed for a non-waiter acquirer: got %v, want [A B]", final.Waiters)
	}
}

// A genuinely new waiter (not the holder, not already queued) is appended and
// gets a stable FIFO position — the existing dedup guard still holds.
func TestMergeSlotAcquire_NewWaiterAppendedFIFO(t *testing.T) {
	ctx := context.Background()
	s := dqNewSlot(types.StatusInProgress, slotMeta{Holder: "H", Waiters: []string{"W1"}})

	res, err := MergeSlotAcquireImpl(ctx, s, "W2", "actor", true)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !res.Waiting {
		t.Fatalf("W2 should be waiting on a held slot")
	}
	if res.Position != 2 {
		t.Fatalf("Position = %d, want 2 (behind W1)", res.Position)
	}
	final := parseSlotMeta(s.slot)
	if len(final.Waiters) != 2 || final.Waiters[0] != "W1" || final.Waiters[1] != "W2" {
		t.Fatalf("FIFO order broken: got %v, want [W1 W2]", final.Waiters)
	}
}

// An existing waiter re-issuing acquire --wait is not duplicated (dedup guard).
func TestMergeSlotAcquire_ExistingWaiterNotDuplicated(t *testing.T) {
	ctx := context.Background()
	s := dqNewSlot(types.StatusInProgress, slotMeta{Holder: "H", Waiters: []string{"W1", "W2"}})

	res, err := MergeSlotAcquireImpl(ctx, s, "W1", "actor", true)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !res.Waiting {
		t.Fatalf("W1 should still be waiting")
	}
	final := parseSlotMeta(s.slot)
	if len(final.Waiters) != 2 {
		t.Fatalf("waiter W1 duplicated: got %v, want len 2", final.Waiters)
	}
}

// Release must not resurrect the released holder into the waiters queue and
// must preserve the remaining waiters.
func TestMergeSlotRelease_DoesNotResurrectHolder(t *testing.T) {
	ctx := context.Background()
	s := dqNewSlot(types.StatusInProgress, slotMeta{Holder: "X", Waiters: []string{"Y"}})

	if err := MergeSlotReleaseImpl(ctx, s, "X", "actor"); err != nil {
		t.Fatalf("MergeSlotReleaseImpl() err = %v, want nil", err)
	}
	final := parseSlotMeta(s.slot)
	if final.Holder != "" {
		t.Fatalf("Holder = %q after release, want empty", final.Holder)
	}
	if dqContains(final.Waiters, "X") {
		t.Fatalf("released holder X resurrected into waiters: %v", final.Waiters)
	}
	if len(final.Waiters) != 1 || final.Waiters[0] != "Y" {
		t.Fatalf("waiters after release = %v, want [Y]", final.Waiters)
	}
	if s.slot.Status != types.StatusOpen {
		t.Fatalf("status after release = %v, want open", s.slot.Status)
	}
}
