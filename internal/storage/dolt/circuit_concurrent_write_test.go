package dolt

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestCircuitBreaker_FixedTempNameCollision is the deterministic teeth for
// beads-pwkv: writeState previously wrote to a FIXED "<file>.tmp" path, which
// is a shared collision point across the concurrent processes that share the
// per-host:port:db breaker file. A fixed temp name is fragile precisely because
// it is a single named resource every writer contends on.
//
// This test forces that collision deterministically and fs-independently by
// occupying the fixed "<file>.tmp" name with a directory. The old code did
// os.WriteFile(cb.filePath+".tmp", ...); with that name taken by a directory
// the write fails, writeState returns WITHOUT persisting, and the just-written
// "open" trip is silently lost (readState falls back to closed) — the exact
// lost-trip failure the bead describes. The atomicfile-based fix uses a
// UNIQUELY-named temp (os.CreateTemp random suffix), so it is immune to any
// fixed-name collision and persists the state.
func TestCircuitBreaker_FixedTempNameCollision(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "beads-dolt-circuit-test.json")
	cb := &circuitBreaker{filePath: fp}

	// Occupy the OLD fixed temp path with a directory so a fixed-name writer
	// cannot create its temp there. The fixed name is the shared contention
	// point; a co-tenant temp (or here, any entry) at that path breaks it.
	if err := os.Mkdir(fp+".tmp", 0700); err != nil {
		t.Fatalf("seed fixed-temp collision: %v", err)
	}

	open := circuitState{State: circuitOpen, Failures: 5, TrippedAt: time.Now().UTC()}
	cb.writeState(open)

	got := cb.readState()
	if got.State != circuitOpen {
		t.Fatalf("trip was lost to a fixed-temp-name collision (beads-pwkv): "+
			"wrote open, read back %q — writeState must use a unique temp path so a "+
			"collision on the shared temp name cannot silently drop the write", got.State)
	}
}

// TestCircuitBreaker_ConcurrentWriteNoTornState is a concurrency guard for
// beads-pwkv: under many concurrent writers to the shared breaker file, a read
// after any interleaving must observe a whole, parseable state (never a torn
// partial), and no leftover temp file may accumulate. On a local fs this passes
// with or without the fix (small single-write+rename is effectively atomic
// there); its value is guarding against a future regression to a scheme that
// CAN tear (e.g. bufio multi-write). The deterministic teeth for the fix itself
// is TestCircuitBreaker_FixedTempNameCollision above.
func TestCircuitBreaker_ConcurrentWriteNoTornState(t *testing.T) {
	dir := t.TempDir()
	cb := &circuitBreaker{filePath: filepath.Join(dir, "beads-dolt-circuit-test.json")}

	open := circuitState{State: circuitOpen, Failures: 5, TrippedAt: time.Now().UTC()}
	closed := circuitState{State: circuitClosed}

	var wg sync.WaitGroup
	const writers = 16
	const iters = 40
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if (w+i)%2 == 0 {
					cb.writeState(open)
				} else {
					cb.writeState(closed)
				}
				st := cb.readState()
				if st.State != circuitOpen && st.State != circuitClosed {
					t.Errorf("read a state that is neither open nor closed: %q (torn write?)", st.State)
				}
			}
		}(w)
	}
	wg.Wait()

	final := cb.readState()
	if final.State != circuitOpen && final.State != circuitClosed {
		t.Fatalf("final state invalid: %q", final.State)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "beads-dolt-circuit-test.json" {
			t.Errorf("unexpected leftover file after concurrent writes: %q", e.Name())
		}
	}
}
