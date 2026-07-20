// Package storage — merge_slot_acquire_test.go
//
// Hermetic unit tests for the transactional merge-slot impls
// MergeSlotAcquireImpl / MergeSlotReleaseImpl (beads-i1yb), extending the
// non-transactional coverage in merge_slot_test.go (beads-3lu2). These use a
// fake Storage whose RunInTransaction runs the callback against a fake
// Transaction backed by the same in-memory issue map, so UpdateIssue writes are
// observable — no real Dolt.
package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// txSlotStore embeds fakeSlotStore and adds the RunInTransaction + Transaction
// surface the acquire/release impls need.
type txSlotStore struct {
	*fakeSlotStore
	updateErr error // when set, UpdateIssue returns it
}

func newTxSlotStore() *txSlotStore { return &txSlotStore{fakeSlotStore: newFakeSlotStore()} }

func (f *txSlotStore) RunInTransaction(_ context.Context, _ string, fn func(tx Transaction) error) error {
	return fn(fakeSlotTx{store: f})
}

// fakeSlotTx satisfies Transaction (embedded, un-overridden methods panic) and
// backs GetIssue/UpdateIssue with the parent store's issue map.
type fakeSlotTx struct {
	Transaction
	store *txSlotStore
}

func (tx fakeSlotTx) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	iss, ok := tx.store.issues[id]
	if !ok {
		return nil, ErrNotFound
	}
	return iss, nil
}

func (tx fakeSlotTx) UpdateIssue(_ context.Context, id string, updates map[string]interface{}, _ string) error {
	if tx.store.updateErr != nil {
		return tx.store.updateErr
	}
	iss := tx.store.issues[id]
	if iss == nil {
		iss = &types.Issue{ID: id}
		tx.store.issues[id] = iss
	}
	if s, ok := updates["status"].(types.Status); ok {
		iss.Status = s
	}
	if m, ok := updates["metadata"].(string); ok {
		iss.Metadata = json.RawMessage(m)
	}
	return nil
}

func putSlot(f *txSlotStore, status types.Status, holder string, waiters ...string) {
	meta := slotMeta{Holder: holder, Waiters: waiters}
	b, _ := json.Marshal(meta)
	f.issues["bd-merge-slot"] = &types.Issue{ID: "bd-merge-slot", Status: status, Metadata: b}
}

func readSlotMeta(f *txSlotStore) slotMeta {
	return parseSlotMeta(f.issues["bd-merge-slot"])
}

func TestMergeSlotAcquireImpl(t *testing.T) {
	ctx := context.Background()

	t.Run("empty holder is rejected", func(t *testing.T) {
		f := newTxSlotStore()
		if _, err := MergeSlotAcquireImpl(ctx, f, "", "actor", false); err == nil ||
			!strings.Contains(err.Error(), "holder must not be empty") {
			t.Fatalf("expected empty-holder error, got %v", err)
		}
	})

	t.Run("slot not found", func(t *testing.T) {
		f := newTxSlotStore() // no slot issue
		if _, err := MergeSlotAcquireImpl(ctx, f, "alice", "actor", false); err == nil ||
			!strings.Contains(err.Error(), "merge slot not found") {
			t.Fatalf("expected not-found error, got %v", err)
		}
	})

	t.Run("available slot is acquired", func(t *testing.T) {
		f := newTxSlotStore()
		putSlot(f, types.StatusOpen, "")
		res, err := MergeSlotAcquireImpl(ctx, f, "alice", "actor", false)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if !res.Acquired || res.Holder != "alice" || res.Waiting {
			t.Fatalf("unexpected result: %+v", res)
		}
		if f.issues["bd-merge-slot"].Status != types.StatusInProgress {
			t.Error("slot should be in_progress after acquire")
		}
		if readSlotMeta(f).Holder != "alice" {
			t.Error("slot metadata should record the new holder")
		}
	})

	t.Run("held slot without --wait is a no-op report", func(t *testing.T) {
		f := newTxSlotStore()
		putSlot(f, types.StatusInProgress, "bob")
		res, err := MergeSlotAcquireImpl(ctx, f, "alice", "actor", false)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if res.Acquired || res.Waiting {
			t.Fatalf("held slot, no wait: expected neither acquired nor waiting, got %+v", res)
		}
		if res.Holder != "bob" {
			t.Errorf("Holder = %q, want bob", res.Holder)
		}
	})

	t.Run("held slot with --wait enqueues the caller", func(t *testing.T) {
		f := newTxSlotStore()
		putSlot(f, types.StatusInProgress, "bob")
		res, err := MergeSlotAcquireImpl(ctx, f, "alice", "actor", true)
		if err != nil {
			t.Fatalf("acquire wait: %v", err)
		}
		if !res.Waiting || res.Position != 1 {
			t.Fatalf("expected waiting at position 1, got %+v", res)
		}
		if w := readSlotMeta(f).Waiters; len(w) != 1 || w[0] != "alice" {
			t.Errorf("waiters = %v, want [alice]", w)
		}
	})

	t.Run("held slot with --wait is idempotent for an existing waiter", func(t *testing.T) {
		f := newTxSlotStore()
		putSlot(f, types.StatusInProgress, "bob", "alice")
		res, err := MergeSlotAcquireImpl(ctx, f, "alice", "actor", true)
		if err != nil {
			t.Fatalf("acquire wait dup: %v", err)
		}
		if !res.Waiting {
			t.Fatal("expected Waiting=true")
		}
		if w := readSlotMeta(f).Waiters; len(w) != 1 {
			t.Errorf("waiter should not be duplicated, got %v", w)
		}
	})

	// beads-ssall: a re-poll by a non-tail waiter must report its ACTUAL 1-based
	// rank, not the total queue depth. Before the fix, result.Position was set
	// to len(meta.Waiters) unconditionally, so a head/middle waiter that
	// re-polled (the normal "am I next yet?" driver loop) was told a position
	// further back than its real place; only the tail waiter was coincidentally
	// correct. Queue order is unaffected — this is a report-only defect.
	t.Run("re-poll reports actual rank, not queue depth (beads-ssall)", func(t *testing.T) {
		// Queue: [bob, carol, dave], slot held by alice. Each re-polls.
		cases := []struct {
			waiter  string
			wantPos int
		}{
			{"bob", 1},   // head — the bug reported 3
			{"carol", 2}, // middle — the bug reported 3
			{"dave", 3},  // tail — coincidentally correct even under the bug
		}
		for _, tc := range cases {
			f := newTxSlotStore()
			putSlot(f, types.StatusInProgress, "alice", "bob", "carol", "dave")
			res, err := MergeSlotAcquireImpl(ctx, f, tc.waiter, "actor", true)
			if err != nil {
				t.Fatalf("re-poll %s: %v", tc.waiter, err)
			}
			if !res.Waiting {
				t.Fatalf("re-poll %s: expected Waiting=true, got %+v", tc.waiter, res)
			}
			if res.Position != tc.wantPos {
				t.Errorf("re-poll %s: Position = %d, want %d (rank, not queue depth)", tc.waiter, res.Position, tc.wantPos)
			}
			// Queue must be unchanged (no duplicate append, order intact).
			if w := readSlotMeta(f).Waiters; len(w) != 3 || w[0] != "bob" || w[1] != "carol" || w[2] != "dave" {
				t.Errorf("re-poll %s: waiters = %v, want [bob carol dave] unchanged", tc.waiter, w)
			}
		}
	})

	// A FIRST enqueue (not yet waiting) must still get the tail position — the
	// fix must not regress the append-then-report path.
	t.Run("first enqueue reports tail position (beads-ssall regression guard)", func(t *testing.T) {
		f := newTxSlotStore()
		putSlot(f, types.StatusInProgress, "alice", "bob", "carol")
		res, err := MergeSlotAcquireImpl(ctx, f, "dave", "actor", true)
		if err != nil {
			t.Fatalf("first enqueue: %v", err)
		}
		if !res.Waiting || res.Position != 3 {
			t.Fatalf("first enqueue: expected waiting at position 3 (new tail), got %+v", res)
		}
		if w := readSlotMeta(f).Waiters; len(w) != 3 || w[2] != "dave" {
			t.Errorf("first enqueue: waiters = %v, want dave appended at tail", w)
		}
	})
}

func TestMergeSlotReleaseImpl(t *testing.T) {
	ctx := context.Background()

	t.Run("slot not found", func(t *testing.T) {
		f := newTxSlotStore()
		if err := MergeSlotReleaseImpl(ctx, f, "alice", "actor"); err == nil ||
			!strings.Contains(err.Error(), "merge slot not found") {
			t.Fatalf("expected not-found error, got %v", err)
		}
	})

	t.Run("wrong holder is rejected", func(t *testing.T) {
		f := newTxSlotStore()
		putSlot(f, types.StatusInProgress, "bob")
		if err := MergeSlotReleaseImpl(ctx, f, "alice", "actor"); err == nil ||
			!strings.Contains(err.Error(), "held by bob, not alice") {
			t.Fatalf("expected wrong-holder error, got %v", err)
		}
	})

	t.Run("already-open is idempotent", func(t *testing.T) {
		f := newTxSlotStore()
		putSlot(f, types.StatusOpen, "")
		if err := MergeSlotReleaseImpl(ctx, f, "", "actor"); err != nil {
			t.Fatalf("release open slot: %v", err)
		}
	})

	t.Run("held slot is released, waiters preserved", func(t *testing.T) {
		f := newTxSlotStore()
		putSlot(f, types.StatusInProgress, "alice", "bob", "carol")
		if err := MergeSlotReleaseImpl(ctx, f, "alice", "actor"); err != nil {
			t.Fatalf("release: %v", err)
		}
		slot := f.issues["bd-merge-slot"]
		if slot.Status != types.StatusOpen {
			t.Error("slot should be open after release")
		}
		meta := readSlotMeta(f)
		if meta.Holder != "" {
			t.Errorf("holder should be cleared, got %q", meta.Holder)
		}
		if len(meta.Waiters) != 2 || meta.Waiters[0] != "bob" {
			t.Errorf("waiters should be preserved, got %v", meta.Waiters)
		}
	})

	t.Run("empty holder skips the ownership check", func(t *testing.T) {
		f := newTxSlotStore()
		putSlot(f, types.StatusInProgress, "bob")
		if err := MergeSlotReleaseImpl(ctx, f, "", "actor"); err != nil {
			t.Fatalf("force release with empty holder: %v", err)
		}
		if f.issues["bd-merge-slot"].Status != types.StatusOpen {
			t.Error("slot should be released regardless of holder when holder arg is empty")
		}
	})
}
