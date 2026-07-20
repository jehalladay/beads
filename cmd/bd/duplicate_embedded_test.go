//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// bdDuplicate runs "bd duplicate" with the given args and returns raw stdout.
func bdDuplicate(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"duplicate"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd duplicate %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdDuplicateFail runs "bd duplicate" expecting failure.
func bdDuplicateFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"duplicate"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd duplicate %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

func TestEmbeddedDuplicate(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "du")

	// ===== Mark as duplicate =====

	t.Run("mark_duplicate", func(t *testing.T) {
		canonical := bdCreate(t, bd, dir, "Canonical issue", "--type", "bug")
		dupe := bdCreate(t, bd, dir, "Duplicate issue", "--type", "bug")
		out := bdDuplicate(t, bd, dir, dupe.ID, "--of", canonical.ID)
		if !strings.Contains(out, "duplicate") {
			t.Errorf("expected 'duplicate' in output: %s", out)
		}
	})

	// ===== Verify closure =====

	t.Run("duplicate_is_closed", func(t *testing.T) {
		canonical := bdCreate(t, bd, dir, "Canon closed", "--type", "task")
		dupe := bdCreate(t, bd, dir, "Dupe closed", "--type", "task")
		bdDuplicate(t, bd, dir, dupe.ID, "--of", canonical.ID)

		s := openStore(t, beadsDir, "du")
		issue, err := s.GetIssue(t.Context(), dupe.ID)
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		if issue.Status != "closed" {
			t.Errorf("expected status=closed, got %s", issue.Status)
		}
	})

	// ===== Creates dependency link =====

	t.Run("creates_dep_link", func(t *testing.T) {
		canonical := bdCreate(t, bd, dir, "Canon link", "--type", "task")
		dupe := bdCreate(t, bd, dir, "Dupe link", "--type", "task")
		bdDuplicate(t, bd, dir, dupe.ID, "--of", canonical.ID)

		// Check via dep list
		out := bdDep(t, bd, dir, "list", dupe.ID)
		if !strings.Contains(out, canonical.ID) {
			t.Errorf("expected canonical in dep list: %s", out)
		}
	})

	// ===== JSON output =====

	t.Run("json_output", func(t *testing.T) {
		canonical := bdCreate(t, bd, dir, "Canon JSON", "--type", "task")
		dupe := bdCreate(t, bd, dir, "Dupe JSON", "--type", "task")
		fullArgs := []string{"duplicate", dupe.ID, "--of", canonical.ID, "--json"}
		cmd := exec.Command(bd, fullArgs...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("duplicate --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		s := strings.TrimSpace(stdout.String())
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("no JSON: %s", s)
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(s[start:]), &m); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if m["duplicate"] != dupe.ID {
			t.Errorf("expected duplicate=%s, got %v", dupe.ID, m["duplicate"])
		}
		if m["canonical"] != canonical.ID {
			t.Errorf("expected canonical=%s, got %v", canonical.ID, m["canonical"])
		}
	})

	// ===== Error: same ID =====

	t.Run("error_same_id", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Same ID", "--type", "task")
		bdDuplicateFail(t, bd, dir, issue.ID, "--of", issue.ID)
	})

	// ===== Error: nonexistent canonical =====

	t.Run("error_nonexistent_canonical", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "No canon", "--type", "task")
		bdDuplicateFail(t, bd, dir, issue.ID, "--of", "du-nonexistent999")
	})

	// beads-dfzre: the `bd dep add --type duplicates` entry point must reject a
	// duplicate-of-a-duplicate chain and a mutual cycle, exactly like `bd
	// duplicate` does (guarded by beads-wqrfi). wqrfi's guard lived at the CMD
	// layer (runDuplicate) so `bd dep add --type duplicates` bypassed it; the
	// guard now lives at the shared storage seam (CheckDependencyCycleInTx) that
	// AddDependencyInTx also calls, closing that bypass. NOTE: `bd dep add` does
	// NOT close the source, so a closed-status precondition (wqrfi's cmd guard)
	// would MISS a dep-add chain — the seam guard keys on the canonical having an
	// outgoing `duplicates` edge, which is the true corruption signal.

	// dep-add chain: dep add MID ROOT --type duplicates, then dep add LEAF MID
	// --type duplicates (canonical MID is itself a duplicate) must fail.
	t.Run("dep_add_duplicates_chain_rejected", func(t *testing.T) {
		root := bdCreate(t, bd, dir, "depadd chain ROOT", "--type", "bug")
		mid := bdCreate(t, bd, dir, "depadd chain MID", "--type", "bug")
		leaf := bdCreate(t, bd, dir, "depadd chain LEAF", "--type", "bug")
		bdDep(t, bd, dir, "add", mid.ID, root.ID, "--type", "duplicates") // MID -> ROOT
		out := bdDepFail(t, bd, dir, "add", leaf.ID, mid.ID, "--type", "duplicates")
		if !strings.Contains(out, "duplicate") {
			t.Errorf("expected dep-add duplicates chain rejection, got: %s", out)
		}
		if !strings.Contains(out, root.ID) {
			t.Errorf("expected dep-add chain rejection to name the live root %s, got: %s", root.ID, out)
		}
	})

	// dep-add mutual cycle: dep add A B --type duplicates, then dep add B A
	// --type duplicates must fail (canonical A already has an outgoing dup edge).
	t.Run("dep_add_duplicates_mutual_cycle_rejected", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "depadd cycle A", "--type", "bug")
		b := bdCreate(t, bd, dir, "depadd cycle B", "--type", "bug")
		bdDep(t, bd, dir, "add", a.ID, b.ID, "--type", "duplicates") // A -> B
		out := bdDepFail(t, bd, dir, "add", b.ID, a.ID, "--type", "duplicates")
		if !strings.Contains(out, "itself a duplicate") {
			t.Errorf("expected dep-add mutual-cycle rejection, got: %s", out)
		}
	})

	// A `dep add --type duplicates` onto a plain canonical (NOT itself a
	// duplicate) must still succeed — the guard is surgical.
	t.Run("dep_add_duplicates_to_plain_canonical_ok", func(t *testing.T) {
		canonical := bdCreate(t, bd, dir, "plain canonical", "--type", "bug")
		dupe := bdCreate(t, bd, dir, "dup of plain", "--type", "bug")
		out := bdDep(t, bd, dir, "add", dupe.ID, canonical.ID, "--type", "duplicates")
		if !strings.Contains(out, "duplicates") {
			t.Errorf("expected dep-add duplicates onto a plain canonical to succeed, got: %s", out)
		}
	})

	// ===== beads-cjl9y: re-marking as a duplicate of a DIFFERENT canonical is rejected =====
	// A --of C then A --of D (A already a closed duplicate of C) must NOT silently
	// add a SECOND outgoing duplicates edge (leaving A "duplicate of [C D]" = two
	// live canonicals). It must fail; A must keep exactly one duplicates edge (to
	// C, the first canonical). Duplicate-side twin of the pmaud supersede guard.
	t.Run("error_different_canonical_re_duplicate_cjl9y", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "cjl9y A", "--type", "bug")
		c := bdCreate(t, bd, dir, "cjl9y C", "--type", "bug")
		d := bdCreate(t, bd, dir, "cjl9y D", "--type", "bug")
		bdDuplicate(t, bd, dir, a.ID, "--of", c.ID)
		// Second, DIFFERENT canonical must be rejected.
		out := bdDuplicateFail(t, bd, dir, a.ID, "--of", d.ID)
		if !strings.Contains(strings.ToLower(out), "already a duplicate") {
			t.Errorf("expected an 'already a duplicate' rejection for A --of D, got: %s", out)
		}
		// A must still carry exactly ONE duplicates edge (to C, not D).
		s := openStore(t, beadsDir, "du")
		deps, err := s.GetDependenciesWithMetadata(t.Context(), a.ID)
		if err != nil {
			t.Fatalf("GetDependenciesWithMetadata: %v", err)
		}
		var dupN int
		var target string
		for _, dep := range deps {
			if dep.DependencyType == "duplicates" {
				dupN++
				target = dep.ID
			}
		}
		if dupN != 1 {
			t.Fatalf("expected exactly 1 duplicates edge on A after a rejected re-duplicate, got %d (multiple-live-canonicals bug)", dupN)
		}
		if target != c.ID {
			t.Errorf("expected the surviving duplicates edge to point at C=%s, got %s", c.ID, target)
		}
	})

	// ===== beads-cjl9y: SAME-canonical re-duplicate stays an idempotent no-op =====
	// A --of C then A --of C again is a no-op (rc0), and A keeps exactly one
	// duplicates edge — LinkAndClose already dedups an identical edge; the guard
	// must preserve that (report "no change", not a second write or a rejection).
	t.Run("same_canonical_re_duplicate_idempotent_cjl9y", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "cjl9y idem A", "--type", "bug")
		c := bdCreate(t, bd, dir, "cjl9y idem C", "--type", "bug")
		bdDuplicate(t, bd, dir, a.ID, "--of", c.ID)
		// Same canonical again — must succeed (rc0) as a no-op.
		out := bdDuplicate(t, bd, dir, a.ID, "--of", c.ID)
		if !strings.Contains(strings.ToLower(out), "no change") {
			t.Errorf("expected a 'no change' idempotent notice for a same-canonical re-duplicate, got: %s", out)
		}
		s := openStore(t, beadsDir, "du")
		deps, err := s.GetDependenciesWithMetadata(t.Context(), a.ID)
		if err != nil {
			t.Fatalf("GetDependenciesWithMetadata: %v", err)
		}
		var dupN int
		for _, dep := range deps {
			if dep.DependencyType == "duplicates" {
				dupN++
			}
		}
		if dupN != 1 {
			t.Errorf("expected exactly 1 duplicates edge after an idempotent same-canonical re-duplicate, got %d", dupN)
		}
	})
}

// TestEmbeddedDuplicateConcurrent exercises duplicate operations concurrently.
func TestEmbeddedDuplicateConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "duc")

	canonical := bdCreate(t, bd, dir, "Concurrent canonical", "--type", "task")
	var dupeIDs []string
	for i := 0; i < 8; i++ {
		d := bdCreate(t, bd, dir, fmt.Sprintf("concurrent-dupe-%d", i), "--type", "task")
		dupeIDs = append(dupeIDs, d.ID)
	}

	const numWorkers = 8
	type workerResult struct {
		worker int
		err    error
	}
	results := make([]workerResult, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			r := workerResult{worker: worker}

			args := []string{"duplicate", dupeIDs[worker], "--of", canonical.ID}
			cmd := exec.Command(bd, args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("worker %d: %v\n%s", worker, err, out)
			}
			results[worker] = r
		}(w)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil && !strings.Contains(r.err.Error(), "one writer at a time") {
			t.Errorf("worker %d failed: %v", r.worker, r.err)
		}
	}
}
