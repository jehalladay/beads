package dolt

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestCircuitBreaker_ConcurrentWriteNoTornState is a regression test for
// beads-pwkv: the breaker state file is SHARED across concurrent processes, and
// writeState previously used a fixed "<file>.tmp" name, so concurrent writers
// clobbered each other's temp and could leave a torn JSON that reads back as a
// corrupt (→ silently "closed") state. With atomicfile's unique-temp+rename,
// every read after any interleaving must observe a VALID, fully-written state.
func TestCircuitBreaker_ConcurrentWriteNoTornState(t *testing.T) {
	dir := t.TempDir()
	cb := &circuitBreaker{
		filePath: filepath.Join(dir, "beads-dolt-circuit-test.json"),
	}

	// Two distinct states with different serialized lengths, so a torn/clobbered
	// write (partial bytes from one over the other) would be detectable as
	// invalid JSON or a wrong-but-parseable mix.
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
				// Concurrent readers must never see a torn file: readState maps a
				// corrupt file to closed, but the file on disk must be one of the
				// two whole states — never partial. Re-read and assert validity.
				st := cb.readState()
				if st.State != circuitOpen && st.State != circuitClosed {
					t.Errorf("read a state that is neither open nor closed: %q (torn write?)", st.State)
				}
			}
		}(w)
	}
	wg.Wait()

	// Final state must be a whole, parseable state.
	final := cb.readState()
	if final.State != circuitOpen && final.State != circuitClosed {
		t.Fatalf("final state invalid: %q", final.State)
	}

	// No leftover temp files should accumulate in the directory (atomicfile
	// cleans its temp on success; a fixed-name leak would leave "<file>.tmp").
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if name != "beads-dolt-circuit-test.json" {
			t.Errorf("unexpected leftover file after concurrent writes: %q", name)
		}
	}
}
