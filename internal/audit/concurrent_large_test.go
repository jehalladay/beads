package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/lockfile"
)

// TestAppend_ConcurrentLargeEntriesNoTornLines guards beads-sf6p: concurrent
// appends of LARGE entries (past PIPE_BUF, large enough to span filesystem
// stripes) to the shared audit log must leave every line a whole, parseable
// Entry — never a torn/interleaved mix.
//
// HONEST SCOPE: on a LOCAL filesystem (where CI/the refinery run) a single
// O_APPEND write() is atomic regardless of size, so this test passes with OR
// without the flock — it cannot reproduce the Lustre-specific cross-client
// tear here. Its value is twofold: (1) it is a regression guard against a
// future change to a scheme that CAN tear on any fs (e.g. bufio multi-write),
// and (2) it proves the flock serialization added for sf6p is correct and does
// not itself corrupt/deadlock/lose entries under heavy concurrency. The
// production defect is Lustre cross-client only; the fix's effectiveness rests
// on the /fsx mount being `flock` (cluster-coherent), verified at mount level,
// not on this local-fs test.
func TestAppend_ConcurrentLargeEntriesNoTornLines(t *testing.T) {
	tmp := t.TempDir()
	beadsDir := filepath.Join(tmp, ".beads")
	if err := os.MkdirAll(beadsDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	t.Setenv("BEADS_DIR", beadsDir)

	const (
		writers          = 8
		entriesPerWriter = 60
		payloadBytes     = 16 * 1024 // 16KiB — well past PIPE_BUF (4KiB), spans stripes
	)
	// A distinct rune per worker so a torn/interleaved line would mix runes
	// within one payload field (extra signal beyond a JSON-parse failure).
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			payload := strings.Repeat(string(rune('A'+worker)), payloadBytes)
			for i := 0; i < entriesPerWriter; i++ {
				if _, err := Append(&Entry{
					Kind:     "llm_call",
					Actor:    "tester",
					Prompt:   payload,
					Response: payload,
				}); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	p, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20) // allow long lines
	lines := 0
	for sc.Scan() {
		lines++
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %d is not valid JSON (torn write? beads-sf6p): %v", lines, err)
		}
		if len(e.Prompt) != payloadBytes {
			t.Fatalf("line %d Prompt len = %d, want %d (interleaved/torn payload)", lines, len(e.Prompt), payloadBytes)
		}
		// A whole payload is one repeated rune; a torn interleave would mix runes.
		if e.Prompt != "" && strings.Count(e.Prompt, string(e.Prompt[0])) != payloadBytes {
			t.Fatalf("line %d Prompt has mixed runes (interleaved payload, beads-sf6p)", lines)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := writers * entriesPerWriter; lines != want {
		t.Fatalf("got %d lines, want %d (lost/merged entries)", lines, want)
	}
}

// TestAppend_HoldsExclusiveLockDuringWrite gives beads-sf6p deterministic teeth
// on a LOCAL fs (where the Lustre cross-client tear cannot be reproduced): it
// proves Append actually HOLDS an exclusive advisory lock on the audit file
// around the write. While Append is inside the lock-held hook, an independent
// fd's non-blocking exclusive lock on the same path MUST fail — which only
// holds if the flock is engaged. Remove the flock (locked=false) and this goes
// RED: the probe would succeed and `locked` would be reported false.
func TestAppend_HoldsExclusiveLockDuringWrite(t *testing.T) {
	tmp := t.TempDir()
	beadsDir := filepath.Join(tmp, ".beads")
	if err := os.MkdirAll(beadsDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	t.Setenv("BEADS_DIR", beadsDir)

	var sawLocked bool
	var probeContended bool
	lockHeldHook = func(path string, locked bool) {
		sawLocked = locked
		// An independent open of the same audit file must NOT be able to take an
		// exclusive lock while Append holds one (flock is per-open-file-description,
		// so a distinct fd is a faithful stand-in for another process/client).
		probe, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			t.Errorf("probe open: %v", err)
			return
		}
		defer func() { _ = probe.Close() }()
		if lockErr := lockfile.FlockExclusiveNonBlocking(probe); lockErr != nil {
			probeContended = true
		}
	}
	t.Cleanup(func() { lockHeldHook = nil })

	if _, err := Append(&Entry{Kind: "llm_call", Actor: "tester", Prompt: "x"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if !sawLocked {
		t.Fatal("Append did not acquire the exclusive lock (beads-sf6p flock missing)")
	}
	if !probeContended {
		t.Fatal("a second fd acquired an exclusive lock while Append held one — lock not engaged around the write (beads-sf6p)")
	}
}
