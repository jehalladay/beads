//go:build integration

package dolt

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestWispReadHelpersDoNotRelockUnderPendingWriter is the teeth for beads-z30l4.
//
// getWisp and searchWisps take s.mu.RLock() and then, while still holding it,
// call leaf query helpers (getWispLabels / getWispsByIDs) that used to run their
// queries through the LOCKING queryContext -> rlockOpen -> s.mu.RLock(). That is
// a second, re-entrant read lock. sync.RWMutex read locks are explicitly NOT
// re-entrant: once a writer blocks on s.mu.Lock(), Go queues every subsequent
// RLock() behind it, so the inner RLock blocks forever against the outer RLock
// that can never release (the goroutine is stuck acquiring the inner one) — a
// permanent deadlock whenever a writer (e.g. Close, store_lifecycle.go:49)
// contends between the outer and inner lock.
//
// Deterministic reproduction: this test holds the OUTER RLock itself — exactly
// what getWisp/searchWisps do after their query begins and before a writer
// arrives — then parks a writer on s.mu.Lock() (which cannot proceed while the
// RLock is held, so it is genuinely "pending"), then calls the inner helper.
// Pre-fix the helper's queryContext re-RLock wedges behind the pending writer;
// post-fix the helper uses queryContextNoLock and completes.
//
// The outer RLock is only released AFTER the helper returns (or the deadlock
// timeout fires), because releasing it early would let the writer run and mask
// the bug.
func TestWispReadHelpersDoNotRelockUnderPendingWriter(t *testing.T) {
	skipIfNoDolt(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	store, cleanup := setupTestStore(t)
	defer cleanup()

	// A wisp WITH a label so getWispLabels/getWispsByIDs return a real row and
	// the round-trip assertion is meaningful.
	wisp := &types.Issue{
		ID:        "z30l4-wisp-0001",
		Title:     "reentrant rlock wisp",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
		Labels:    []string{"deadlock-probe"},
	}
	if err := store.CreateIssue(ctx, wisp, "tester"); err != nil {
		t.Fatalf("create wisp: %v", err)
	}

	// runUnderHeldRLockWithPendingWriter mirrors the getWisp/searchWisps lock
	// discipline: take the outer RLock, park a pending writer behind it, then
	// run the inner helper. Returns an error if the helper deadlocked.
	runUnderHeldRLockWithPendingWriter := func(t *testing.T, name string, inner func() error) {
		t.Helper()

		store.mu.RLock()

		writerParked := make(chan struct{})
		releaseWriter := make(chan struct{})
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			close(writerParked)
			store.mu.Lock() // blocks: the outer RLock is held below
			defer store.mu.Unlock()
			<-releaseWriter
		}()
		<-writerParked
		// Let the writer actually enter the blocked Lock() so it is registered
		// as pending before the inner helper runs.
		time.Sleep(50 * time.Millisecond)

		done := make(chan error, 1)
		go func() { done <- inner() }()

		teardown := func() {
			store.mu.RUnlock() // outer RLock drops -> parked writer can proceed
			close(releaseWriter)
			<-writerDone
		}

		select {
		case err := <-done:
			teardown()
			if err != nil {
				t.Fatalf("%s: inner helper returned error: %v", name, err)
			}
		case <-time.After(20 * time.Second):
			teardown()
			t.Fatalf("%s deadlocked: inner query re-acquired s.mu.RLock while a writer was pending (beads-z30l4)", name)
		}
	}

	// GAP 1: getWisp -> getWispLabels.
	runUnderHeldRLockWithPendingWriter(t, "getWispLabels", func() error {
		labels, err := store.getWispLabels(ctx, wisp.ID)
		if err != nil {
			return err
		}
		if len(labels) != 1 || labels[0] != "deadlock-probe" {
			t.Errorf("getWispLabels round-trip mismatch: got %v", labels)
		}
		return nil
	})

	// GAP 2: searchWisps -> scanWispIDs -> getWispsByIDs (both the wisp query
	// and the label-hydration query).
	runUnderHeldRLockWithPendingWriter(t, "getWispsByIDs", func() error {
		issues, err := store.getWispsByIDs(ctx, []string{wisp.ID})
		if err != nil {
			return err
		}
		if len(issues) != 1 || issues[0].ID != wisp.ID {
			t.Errorf("getWispsByIDs returned %d issues, want 1 (%s)", len(issues), wisp.ID)
		} else if len(issues[0].Labels) != 1 || issues[0].Labels[0] != "deadlock-probe" {
			t.Errorf("getWispsByIDs label hydration mismatch: got %v", issues[0].Labels)
		}
		return nil
	})
}
