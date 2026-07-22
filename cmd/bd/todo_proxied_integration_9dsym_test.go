//go:build cgo

package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-9dsym: bd todo add/list/done are registered top-level commands, so they
// are reachable for hub-connected/proxied crew — where the global store is nil
// (main.go returns after wiring uowProvider, before store init). The direct RunE
// bodies called getStore() unconditionally, so every proxied `bd todo` subcommand
// failed with "storage is nil". The fix routes each through the proxied UOW stack
// (todo_proxied_server.go), mirroring bd create/list/close. These teeth drive the
// REAL proxied-server `bd` subprocess end-to-end (BEADS_TEST_PROXIED_SERVER=1) —
// exactly the mode the nil-store bug lived in — proving each verb now works AND
// that `bd todo done` preserves the wrapped `bd close` behavior: the pre-close
// guards (beads-k96re) + the completed-molecule auto-close cascade (beads-58kg8).
//
// MUTATION-VERIFY: revert any of the three usesProxiedServer() branches in
// todo.go (so the verb falls through to getStore()) and the corresponding subtest
// FAILS with a "storage is nil" error from the subprocess — the exact pre-fix
// symptom.

// TestProxiedTodoAddListDone_9dsym proves all three `bd todo` subcommands work on
// the proxied path (the nil-store fix) with a single seeded todo.
func TestProxiedTodoAddListDone_9dsym(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "ptd")

	// add: create a todo via the proxied path. Pre-fix this errored "storage is nil".
	addOut, err := bdProxiedRun(t, bd, p.dir, "todo", "add", "first todo")
	if err != nil {
		t.Fatalf("proxied `bd todo add` failed (nil-store regression, beads-9dsym): %v\n%s", err, addOut)
	}
	// "Created <id>: <title>" — pull the id.
	todoID := parseCreatedID(t, string(addOut))
	if todoID == "" {
		t.Fatalf("could not parse created todo id from output:\n%s", addOut)
	}

	// The created issue is a task (bd todo add == create -t task -p 2).
	created := bdProxiedShow(t, bd, p.dir, todoID)
	if created.IssueType != types.TypeTask {
		t.Errorf("todo add created type %q, want %q", created.IssueType, types.TypeTask)
	}
	if created.Priority != 2 {
		t.Errorf("todo add default priority %d, want 2", created.Priority)
	}

	// list: the open todo shows up via the proxied path.
	listOut, err := bdProxiedRun(t, bd, p.dir, "todo", "list")
	if err != nil {
		t.Fatalf("proxied `bd todo list` failed (nil-store regression): %v\n%s", err, listOut)
	}
	if !strings.Contains(string(listOut), todoID) {
		t.Fatalf("proxied `bd todo list` did not list the open todo %s:\n%s", todoID, listOut)
	}

	// done: close the todo via the proxied path.
	doneOut, err := bdProxiedRun(t, bd, p.dir, "todo", "done", todoID)
	if err != nil {
		t.Fatalf("proxied `bd todo done` failed (nil-store regression): %v\n%s", err, doneOut)
	}
	if closed := bdProxiedShow(t, bd, p.dir, todoID); closed.Status != types.StatusClosed {
		t.Fatalf("proxied `bd todo done` did not close %s; status=%q", todoID, closed.Status)
	}

	// After close, the default (open-only) list no longer shows it.
	listOut2, err := bdProxiedRun(t, bd, p.dir, "todo", "list")
	if err != nil {
		t.Fatalf("proxied `bd todo list` (post-close) failed: %v\n%s", err, listOut2)
	}
	if strings.Contains(string(listOut2), todoID) {
		t.Errorf("proxied `bd todo list` still shows closed todo %s (should be open-only):\n%s", todoID, listOut2)
	}
}

// TestProxiedTodoDone_BlockerGuard_9dsym proves `bd todo done` runs the same
// open-blocker pre-close guard `bd close` runs on the proxied path (beads-k96re):
// a blocked todo is refused without --force and closed with it.
func TestProxiedTodoDone_BlockerGuard_9dsym(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "ptb")

	blocker := bdProxiedCreate(t, bd, p.dir, "blocker", "--type", "task")
	target := bdProxiedCreate(t, bd, p.dir, "blocked todo", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", target.ID, blocker.ID, "--type", "blocks"); err != nil {
		t.Fatalf("proxied dep add (blocks) failed: %v\n%s", err, out)
	}

	// Without --force: the guard must refuse the close.
	if out, err := bdProxiedRun(t, bd, p.dir, "todo", "done", target.ID); err == nil {
		t.Fatalf("proxied `bd todo done` closed a blocked todo without --force — the k96re blocker guard did not fire:\n%s", out)
	}
	if got := bdProxiedShow(t, bd, p.dir, target.ID); got.Status == types.StatusClosed {
		t.Fatalf("blocked todo %s was closed despite the guard; status=%q", target.ID, got.Status)
	}

	// With --force: the guard is overridden and the todo closes.
	if out, err := bdProxiedRun(t, bd, p.dir, "todo", "done", target.ID, "--force"); err != nil {
		t.Fatalf("proxied `bd todo done --force` should override the blocker guard: %v\n%s", err, out)
	}
	if got := bdProxiedShow(t, bd, p.dir, target.ID); got.Status != types.StatusClosed {
		t.Fatalf("`bd todo done --force` did not close blocked todo %s; status=%q", target.ID, got.Status)
	}
}

// TestProxiedTodoDone_AutoClosesCompletedMolecule_9dsym proves `bd todo done` of
// a molecule's FINAL step auto-closes the completed root on the proxied path
// (beads-58kg8 parity), and that closing a NON-final step does not.
func TestProxiedTodoDone_AutoClosesCompletedMolecule_9dsym(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "ptm")

	rootID, lastStep := seedProxiedMoleculeLastStepOpen(t, bd, p)

	if out, err := bdProxiedRun(t, bd, p.dir, "todo", "done", lastStep); err != nil {
		t.Fatalf("proxied `bd todo done` of the final step failed: %v\n%s", err, out)
	}
	if root := bdProxiedShow(t, bd, p.dir, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — proxied `bd todo done` of the final step did not auto-close the completed molecule (beads-9dsym/58kg8)", rootID, root.Status, types.StatusClosed)
	}
}

func TestProxiedTodoDone_NonFinalStepDoesNotAutoCloseRoot_9dsym(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "ptn")

	root := bdProxiedCreate(t, bd, p.dir, "molecule root", "--type", "molecule")
	step1 := bdProxiedCreate(t, bd, p.dir, "step 1", "--type", "task")
	step2 := bdProxiedCreate(t, bd, p.dir, "step 2", "--type", "task")
	for _, stepID := range []string{step1.ID, step2.ID} {
		if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", stepID, root.ID, "--type", "parent-child"); err != nil {
			t.Fatalf("proxied dep add %s -> %s failed: %v\n%s", stepID, root.ID, err, out)
		}
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "todo", "done", step1.ID); err != nil {
		t.Fatalf("proxied `bd todo done` of a non-final step failed: %v\n%s", err, out)
	}
	if got := bdProxiedShow(t, bd, p.dir, root.ID); got.Status == types.StatusClosed {
		t.Errorf("molecule root %s auto-closed after `bd todo done` of only ONE of two steps — the cascade must fire only on real completion (beads-9dsym)", root.ID)
	}
}

// parseCreatedID extracts the issue id from a `bd todo add` line of the form
// "Created <id>: <title>". Returns "" if not found.
func parseCreatedID(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Created ") {
			continue
		}
		rest := strings.TrimPrefix(line, "Created ")
		if i := strings.Index(rest, ":"); i >= 0 {
			return strings.TrimSpace(rest[:i])
		}
		return strings.TrimSpace(strings.Fields(rest)[0])
	}
	return ""
}
