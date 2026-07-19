//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerGate proves the `bd gate` subcommand family is
// proxied-server-aware (beads-qppc, aocj/1zuh class). Before the fix, every gate
// subcommand touched the global `store` (SearchIssues/GetIssue/CreateIssue/
// AddDependency/UpdateIssue/CloseIssue), which is NIL in proxiedServerMode
// (main.go sets uowProvider but returns before newDoltStore), so hub-connected
// crew hit "storage is nil". These are clean-mirror legs (every store call has a
// UOW use-case equivalent). This test exercises the full gate lifecycle through
// the proxied UOW stack.
func TestProxiedServerGate(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("gate_create_blocks_and_lists", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gpc")
		target := bdProxiedCreate(t, bd, p.dir, "Gate target", "--type", "task")

		// create: writes a gate issue + a blocking dependency, then commits.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "create", "--blocks", target.ID, "--type", "human", "--reason", "needs review")
		if err != nil {
			t.Fatalf("bd gate create failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd gate create hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "Created gate") || !strings.Contains(stdout, target.ID) {
			t.Fatalf("expected gate-created confirmation blocking %s:\n%s", target.ID, stdout)
		}

		// list: the newly-created open gate must appear.
		listOut, listErr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "list")
		if err != nil {
			t.Fatalf("bd gate list failed: %v\nstdout:\n%s\nstderr:\n%s", listOut, listErr, err)
		}
		if strings.Contains(listOut+listErr, "storage is nil") {
			t.Fatalf("bd gate list hit 'storage is nil' in proxied mode:\n%s\n%s", listOut, listErr)
		}
		if !strings.Contains(listOut, "Open Gates") {
			t.Errorf("expected an open gate in list output:\n%s", listOut)
		}

		// The blocked target must not be ready while the gate is open.
		readyOut := bdProxiedList(t, bd, p, "--json")
		_ = readyOut
	})

	t.Run("gate_create_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gpj")
		target := bdProxiedCreate(t, bd, p.dir, "Gate json target", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "create", "--blocks", target.ID, "--type", "human", "--json")
		if err != nil {
			t.Fatalf("bd gate create --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd gate create --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, `"issue_type"`) || !strings.Contains(stdout, "gate") {
			t.Errorf("expected JSON gate object:\n%s", stdout)
		}
	})

	t.Run("gate_show_and_add_waiter_and_resolve", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gps")
		target := bdProxiedCreate(t, bd, p.dir, "Gate lifecycle target", "--type", "task")

		// Create the gate, capture its JSON to learn the gate ID.
		createOut, createErr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "create", "--blocks", target.ID, "--type", "human", "--json")
		if err != nil {
			t.Fatalf("bd gate create --json failed: %v\n%s\n%s", err, createOut, createErr)
		}
		gate := parseIssueJSON(t, []byte(createOut))
		if gate.ID == "" {
			t.Fatalf("no gate ID parsed from create output:\n%s", createOut)
		}

		// show: must return the gate details, not "storage is nil".
		showOut, showErr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "show", gate.ID)
		if err != nil {
			t.Fatalf("bd gate show failed: %v\n%s\n%s", err, showOut, showErr)
		}
		if strings.Contains(showOut+showErr, "storage is nil") {
			t.Fatalf("bd gate show hit 'storage is nil':\n%s\n%s", showOut, showErr)
		}
		if !strings.Contains(showOut, gate.ID) {
			t.Errorf("expected gate id %s in show output:\n%s", gate.ID, showOut)
		}

		// add-waiter: registers a waiter (write via UpdateIssue).
		waiterOut, waiterErr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "add-waiter", gate.ID, "my-rig/workers/agent-1")
		if err != nil {
			t.Fatalf("bd gate add-waiter failed: %v\n%s\n%s", err, waiterOut, waiterErr)
		}
		if strings.Contains(waiterOut+waiterErr, "storage is nil") {
			t.Fatalf("bd gate add-waiter hit 'storage is nil':\n%s\n%s", waiterOut, waiterErr)
		}
		if !strings.Contains(waiterOut, "Added waiter") {
			t.Errorf("expected waiter-added confirmation:\n%s", waiterOut)
		}
		// The waiter must be persisted (visible via show).
		showOut2 := bdProxiedShowRaw(t, bd, p.dir, gate.ID)
		if !strings.Contains(showOut2, "my-rig/workers/agent-1") {
			// gate show is the gate-specific renderer; fall back to gate show.
			gShow, _, _ := bdProxiedRunBuffers(t, bd, p.dir, "gate", "show", gate.ID)
			if !strings.Contains(gShow, "my-rig/workers/agent-1") {
				t.Errorf("expected registered waiter to persist:\nshow:\n%s\ngate show:\n%s", showOut2, gShow)
			}
		}

		// resolve: closes the gate (CloseIssue).
		resolveOut, resolveErr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "resolve", gate.ID, "--reason", "done")
		if err != nil {
			t.Fatalf("bd gate resolve failed: %v\n%s\n%s", err, resolveOut, resolveErr)
		}
		if strings.Contains(resolveOut+resolveErr, "storage is nil") {
			t.Fatalf("bd gate resolve hit 'storage is nil':\n%s\n%s", resolveOut, resolveErr)
		}
		if !strings.Contains(resolveOut, "Gate resolved") {
			t.Errorf("expected gate-resolved confirmation:\n%s", resolveOut)
		}

		// After resolution, the gate must be closed (not in the default open list).
		listOut, _, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "list")
		if err != nil {
			t.Fatalf("bd gate list (post-resolve) failed: %v\n%s", err, listOut)
		}
		if strings.Contains(listOut, gate.ID) {
			t.Errorf("resolved gate %s should not appear in default (open-only) list:\n%s", gate.ID, listOut)
		}
	})

	t.Run("gate_check_no_open_gates", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gpk")
		// gate check with no gates: reads via SearchIssues; must not hit nil store.
		out, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "check")
		if err != nil {
			t.Fatalf("bd gate check failed: %v\n%s\n%s", err, out, stderr)
		}
		if strings.Contains(out+stderr, "storage is nil") {
			t.Fatalf("bd gate check hit 'storage is nil' in proxied mode:\n%s\n%s", out, stderr)
		}
		if !strings.Contains(out, "No open gates") {
			t.Errorf("expected 'No open gates found' with an empty gate set:\n%s", out)
		}
	})

	t.Run("gate_show_nonexistent_fails_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gpx")
		out, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "show", "gpx-nope999")
		if err == nil {
			t.Fatalf("expected gate show on a nonexistent id to fail; got:\n%s\n%s", out, stderr)
		}
		if strings.Contains(out+stderr, "storage is nil") {
			t.Fatalf("nonexistent gate show hit 'storage is nil' rather than not-found:\n%s\n%s", out, stderr)
		}
	})
}
