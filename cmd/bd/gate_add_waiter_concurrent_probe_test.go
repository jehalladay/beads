//go:build cgo

package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestProxiedServerGateAddWaiterConcurrent probes whether concurrent
// `bd gate add-waiter <same-gate> <distinct-waiter>` calls on the shared Dolt
// sql-server lose waiters. add-waiter is a read-modify-write on the gate's
// single `waiters` JSON cell: it reads the current Waiters slice, appends one,
// and UpdateIssue-writes the whole slice back. On the shared server each proxied
// bd subprocess opens its own transaction snapshot (dolt_sql_provider BeginTx),
// so N concurrent add-waiters all read the SAME pre-existing slice and each
// writes back "old + its own one" — Dolt auto-cell-merges the concurrent commits
// with NO serialization conflict, so only the last writer's slice survives and
// the other N-1 waiters are silently lost. Same class as beads-1i4u (proxied
// ready --claim double-claim) and beads-iq8zr — a shared-server read-then-write
// critical section with no advisory lock.
func TestProxiedServerGateAddWaiterConcurrent(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "gaw")

	target := bdProxiedCreate(t, bd, p.dir, "add-waiter concurrency target", "--type", "task")

	// One shared gate; every worker registers a DISTINCT waiter on it.
	createOut, createErr, err := bdProxiedRunBuffers(t, bd, p.dir,
		"gate", "create", "--blocks", target.ID, "--type", "human", "--json")
	if err != nil {
		t.Fatalf("gate create --json failed: %v\n%s\n%s", err, createOut, createErr)
	}
	gate := parseIssueJSON(t, []byte(createOut))
	if gate.ID == "" {
		t.Fatalf("no gate ID parsed:\n%s", createOut)
	}

	const numWorkers = 8
	waiters := make([]string, numWorkers)
	for w := 0; w < numWorkers; w++ {
		waiters[w] = fmt.Sprintf("my-rig/workers/agent-%d", w)
	}

	type result struct {
		stdout string
		stderr string
		err    error
	}
	results := make([]result, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			cmd := exec.Command(bd, "gate", "add-waiter", gate.ID, waiters[worker])
			cmd.Dir = p.dir
			cmd.Env = bdProxiedEnv(p.dir)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			e := cmd.Run()
			results[worker] = result{stdout: stdout.String(), stderr: stderr.String(), err: e}
		}(w)
	}
	wg.Wait()

	for i, r := range results {
		if r.err != nil {
			t.Fatalf("worker %d add-waiter errored: %v\nstdout=%s\nstderr=%s", i, r.err, r.stdout, r.stderr)
		}
	}

	// Read the final waiter set back. Every distinct waiter that reported success
	// MUST be persisted; a lost waiter means the concurrent read-modify-write
	// clobbered a prior append.
	final := bdProxiedShow(t, bd, p.dir, gate.ID)
	got := make(map[string]bool, len(final.Waiters))
	for _, w := range final.Waiters {
		got[w] = true
	}
	var missing []string
	for _, w := range waiters {
		if !got[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		for i, r := range results {
			t.Logf("worker %d: stdout=%s stderr=%s", i, strings.TrimSpace(r.stdout), strings.TrimSpace(r.stderr))
		}
		t.Fatalf("beads-1i4u/iq8zr class: %d of %d concurrently-added waiters were silently lost: %v\nfinal waiters (%d): %v",
			len(missing), numWorkers, missing, len(final.Waiters), final.Waiters)
	}
}
