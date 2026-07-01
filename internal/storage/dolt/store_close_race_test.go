package dolt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newRaceTestStore builds a fully-functional *DoltStore against the shared test
// server. It mirrors the New() usage in create_guard_test.go. Skips when no
// server is running and acquires a single shared-server semaphore slot (released
// at test end).
func newRaceTestStore(t *testing.T) *DoltStore {
	t.Helper()
	skipIfNoServer(t)
	return buildRaceTestStore(t, 0)
}

// buildRaceTestStore opens a fresh store against the shared test server WITHOUT
// touching the test-slot semaphore. The caller is responsible for gating
// concurrency (e.g. calling skipIfNoServer once before a loop) — this lets a
// single test stand up many short-lived stores in sequence without exhausting
// the cap-2 slot pool (each newRaceTestStore would hold its slot until test end
// and deadlock after two rounds). seq disambiguates the database name per call.
func buildRaceTestStore(t *testing.T, seq int) *DoltStore {
	t.Helper()
	dbName := fmt.Sprintf("test_close_race_%d_%d_%s", testServerPort, seq, sanitizeDBName(t.Name()))
	t.Cleanup(func() { dropTestDatabase(t, testServerPort, dbName) })

	cfg := &Config{
		Path:            t.TempDir(),
		ServerHost:      "127.0.0.1",
		ServerPort:      testServerPort,
		Database:        dbName,
		MaxOpenConns:    4,
		CreateIfMissing: true,
	}
	store, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

// sanitizeDBName makes a test name safe to embed in a database identifier.
func sanitizeDBName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// TestStoreCloseRace_TxHelpers exercises the five transaction helpers
// concurrently with Close(). Before the fix these helpers read s.closed and/or
// dereference s.db OUTSIDE s.mu, so the unlocked s.db read races with Close()'s
// `s.db = nil` write (data race under -race) and can panic on use-after-close
// (TOCTOU). After the fix the closed/db guard runs INSIDE the lock, so a helper
// either observes the store open (and Close blocks until it finishes) or sees it
// closed and returns ErrStoreClosed — never a torn read of s.db.
//
// Run with `go test -race` to catch the data race; the panic-recovery below
// catches the use-after-close nil dereference even without the race detector.
func TestStoreCloseRace_TxHelpers(t *testing.T) {
	ctx := context.Background()

	// Each helper invocation must terminate cleanly: success, a benign
	// ErrStoreClosed, or a normal SQL error — but NEVER a panic.
	assertNoPanic := func(t *testing.T, name string, fn func() error) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("%s panicked (use-after-close TOCTOU): %v", name, r)
			}
		}()
		// A closed store legitimately returns ErrStoreClosed; any other error
		// (e.g. connection closed) is also acceptable here — we only assert the
		// absence of a panic / a data race on s.db.
		_ = fn()
	}

	// The TOCTOU window is narrow: a helper must be between its closed/db check
	// and its first s.db deref exactly when Close() nils s.db. A single
	// store/Close pair almost never lands there. Repeat the whole race over many
	// fresh stores so the -race detector and the panic-recovery reliably catch
	// the unsynchronized s.db read in the unfixed code.
	// Acquire one shared-server slot for the whole test; build per-round stores
	// without re-acquiring (see buildRaceTestStore) so we don't exhaust the
	// cap-2 semaphore.
	skipIfNoServer(t)

	const rounds = 5
	for round := 0; round < rounds; round++ {
		store := buildRaceTestStore(t, round)

		// helpers maps each named tx helper to a closure that exercises it.
		helpers := map[string]func() error{
			"withReadTx": func() error {
				return store.withReadTx(ctx, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, "SELECT 1")
					return err
				})
			},
			"withWriteTx": func() error {
				return store.withWriteTx(ctx, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, "SELECT 1")
					return err
				})
			},
			"execContext": func() error {
				_, err := store.execContext(ctx, "SELECT 1")
				return err
			},
			"queryContext": func() error {
				rows, err := store.queryContext(ctx, "SELECT 1")
				if rows != nil {
					_ = rows.Close()
				}
				return err
			},
			"queryRowContext": func() error {
				return store.queryRowContext(ctx, func(row *sql.Row) error {
					var n int
					return row.Scan(&n)
				}, "SELECT 1")
			},
		}

		const workersPerHelper = 6
		var wg sync.WaitGroup
		var stop atomic.Bool
		start := make(chan struct{})

		for name, fn := range helpers {
			for w := 0; w < workersPerHelper; w++ {
				wg.Add(1)
				go func(name string, fn func() error) {
					defer wg.Done()
					<-start
					// Hammer the helper, keeping many goroutines inside the
					// check→deref window when Close() nils s.db. Bounded by an
					// iteration cap so a wedged server can never hang the test;
					// the stop flag short-circuits once Close has happened.
					for i := 0; i < 200 && !stop.Load(); i++ {
						assertNoPanic(t, name, fn)
					}
				}(name, fn)
			}
		}

		// Close concurrently with in-flight helper traffic. The brief delay lets
		// the worker pool ramp up so Close lands while helpers are mid-call.
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(2 * time.Millisecond)
			_ = store.Close()
			stop.Store(true)
		}()

		close(start)
		wg.Wait()

		if !store.closed.Load() {
			t.Fatal("store should be closed after the race window")
		}
	}
}

// TestStoreCloseRace_InfraTypesVsSetConfig reproduces the specific pairing
// called out in the bead: GetInfraTypes (a withReadTx reader) racing SetConfig
// (a withRetryTx writer) while Close() runs. Same invariant: no panic, no torn
// s.db read.
func TestStoreCloseRace_InfraTypesVsSetConfig(t *testing.T) {
	store := newRaceTestStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("GetInfraTypes panicked: %v", r)
			}
		}()
		<-start
		for i := 0; i < 50; i++ {
			_ = store.GetInfraTypes(ctx)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("SetConfig panicked: %v", r)
			}
		}()
		<-start
		for i := 0; i < 50; i++ {
			err := store.SetConfig(ctx, "race.key", fmt.Sprintf("v%d", i))
			if err != nil && !errors.Is(err, ErrStoreClosed) {
				// connection-closed errors after Close are acceptable
				_ = err
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_ = store.Close()
	}()

	close(start)
	wg.Wait()
}

// TestStoreCloseRace_LockSerializesClose deterministically proves the invariant
// behind the fix: rlockOpen holds s.mu.RLock across the s.db access, so once a
// helper has the read lock, Close() (which takes s.mu.Lock() before niling s.db)
// MUST block until the helper releases. This removes the TOCTOU without relying
// on scheduler timing — unlike the stress tests above, it fails 100% of the time
// against the pre-fix code (where the helpers checked closed/db before locking,
// so Close could nil s.db while a helper held no lock at all).
func TestStoreCloseRace_LockSerializesClose(t *testing.T) {
	store := newRaceTestStore(t)

	// Acquire the open-guard read lock, simulating a helper that has just
	// validated the store is open and is about to touch s.db.
	release, err := store.rlockOpen()
	if err != nil {
		t.Fatalf("rlockOpen on a fresh store: %v", err)
	}

	closeReturned := make(chan struct{})
	go func() {
		_ = store.Close()
		close(closeReturned)
	}()

	// Close must NOT complete while the read lock is held. If the guard did not
	// hold the lock across the db access (the pre-fix bug), Close would race
	// ahead and nil s.db here.
	select {
	case <-closeReturned:
		t.Fatal("Close() returned while the open-guard read lock was held — lock does not serialize Close (TOCTOU not fixed)")
	case <-time.After(100 * time.Millisecond):
		// Expected: Close is blocked on s.mu.Lock().
	}

	// While we hold the guard's read lock, s.db must still be non-nil: Close is
	// parked on s.mu.Lock() and cannot have niled it yet. Reading s.db here is
	// race-safe because we still hold the RLock from rlockOpen (do NOT take a
	// second RLock — recursive read-locking with a writer parked can deadlock).
	if store.db == nil {
		t.Fatal("s.db was niled while the open-guard read lock was held")
	}

	// Release the guard; Close must now proceed and finish.
	release()
	select {
	case <-closeReturned:
		// Expected.
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not complete after the read lock was released")
	}

	if !store.closed.Load() {
		t.Fatal("store should report closed after Close returned")
	}
}
