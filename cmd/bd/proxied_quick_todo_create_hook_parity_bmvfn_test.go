//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-bmvfn (PROXIED on_create hook parity for `bd q`/`bd quick` and `bd todo add`).
//
// The DIRECT create verbs fire on_create after the commit via the hook decorator
// (internal/storage/hook_decorator.go): store.CreateIssue is routed through
// HookFiringStore (main.go wires store = NewHookFiringStore(store, hookRunner)),
// which records createHookEvents (on_create on a label-free snapshot, then a
// synthetic on_update per cumulative label) and fires them post-commit. BUT the
// proxied handlers (runQuickProxiedServer / runTodoAddProxiedServer) commit via
// the UOW use-case layer, which does NOT wrap HookFiringStore — so a hub-connected
// (proxied, store==nil) crew's on_create automation silently never ran for
// `bd q`/`bd quick` and `bd todo add`. These are the last un-swept create-leg
// siblings of beads-w1vxy (single `bd create`) / beads-pma90 (graph create) /
// beads-29tyj (comment/dep/relate on_update parity).
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level helper
// would false-green by skipping the CLI hook plumbing — the batch-parity family
// lesson). MUTATION-VERIFIED: remove the fireProxiedCreateHooks call added to
// runQuickProxiedServer / runTodoAddProxiedServer and the matching sub-test goes
// RED.
// bdProxiedTodoAdd runs `bd todo add --json <args>` through the proxied subprocess
// and parses the emitted issue object (todo add honors --json via
// outputJSON(issue), beads-s2oy).
func bdProxiedTodoAdd(t *testing.T, bd, dir string, args ...string) *types.Issue {
	t.Helper()
	fullArgs := append([]string{"todo", "add", "--json"}, args...)
	out, err := bdProxiedRun(t, bd, dir, fullArgs...)
	if err != nil {
		t.Fatalf("bd todo add %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return parseIssueJSON(t, out)
}

func TestProxiedQuickTodoCreateHookParity_bmvfn(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	appendHookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}

	// `bd q <title>` prints only the new ID; capture it and assert on_create fired.
	t.Run("quick_fires_on_create", func(t *testing.T) {
		dir := t.TempDir()
		createMarker := filepath.Join(dir, "on_create_marker")
		p := bdProxiedInitWithHooks(t, bd, "qhp", map[string]string{
			"on_create": appendHookBody(createMarker),
		})

		_ = os.Remove(createMarker)
		out, err := bdProxiedRun(t, bd, p.dir, "q", "quick hook test")
		if err != nil {
			t.Fatalf("bd q failed: %v\n%s", err, out)
		}
		id := strings.TrimSpace(string(out))
		if id == "" {
			t.Fatalf("bd q emitted no ID; out=%q", string(out))
		}

		if got, ok := waitForMarkerContains(createMarker, id, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-bmvfn): proxied `bd q` did NOT fire on_create for %s (the direct path fires it via HookFiringStore.CreateIssue → createHookEvents); marker=%q", id, got)
		}
	})

	// A labeled `bd q -l alpha` fires on_create (label-free snapshot) AND the
	// synthetic on_update label stream, mirroring createHookEvents.
	t.Run("labeled_quick_fires_on_create_and_label_on_update", func(t *testing.T) {
		dir := t.TempDir()
		createMarker := filepath.Join(dir, "on_create_marker")
		updateMarker := filepath.Join(dir, "on_update_marker")
		p := bdProxiedInitWithHooks(t, bd, "qlp", map[string]string{
			"on_create": appendHookBody(createMarker),
			"on_update": appendHookBody(updateMarker),
		})

		_ = os.Remove(createMarker)
		_ = os.Remove(updateMarker)
		out, err := bdProxiedRun(t, bd, p.dir, "q", "labeled quick hook test", "-l", "alpha")
		if err != nil {
			t.Fatalf("bd q -l alpha failed: %v\n%s", err, out)
		}
		id := strings.TrimSpace(string(out))
		if id == "" {
			t.Fatalf("bd q -l alpha emitted no ID; out=%q", string(out))
		}

		if got, ok := waitForMarkerContains(createMarker, id, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-bmvfn): proxied labeled `bd q` did NOT fire on_create for %s; marker=%q", id, got)
		}
		if got, ok := waitForMarkerContains(updateMarker, id, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-bmvfn): proxied labeled `bd q` did NOT fire the synthetic on_update label stream for %s (createHookEvents parity); marker=%q", id, got)
		}
	})

	// `bd todo add <title>` (no --label flag) fires a bare on_create.
	t.Run("todo_add_fires_on_create", func(t *testing.T) {
		dir := t.TempDir()
		createMarker := filepath.Join(dir, "on_create_marker")
		p := bdProxiedInitWithHooks(t, bd, "thp", map[string]string{
			"on_create": appendHookBody(createMarker),
		})

		_ = os.Remove(createMarker)
		issue := bdProxiedTodoAdd(t, bd, p.dir, "todo hook test")

		if got, ok := waitForMarkerContains(createMarker, issue.ID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-bmvfn): proxied `bd todo add` did NOT fire on_create for %s (the direct path fires it via HookFiringStore.CreateIssue → createHookEvents); marker=%q", issue.ID, got)
		}
	})
}
