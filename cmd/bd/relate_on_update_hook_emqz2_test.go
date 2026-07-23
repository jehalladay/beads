//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// beads-emqz2 (HOOK-PARITY, direct relate/unrelate leg — direct-path counterpart
// of beads-29tyj, which fixed the PROXIED relate/unrelate handlers).
//
// `bd dep add A B --type related` writes a DepRelatesTo edge and fires the
// on_update hook. `bd relate A B` / `bd unrelate A B` write/remove the SAME
// DepRelatesTo edge (both directions) but did NOT fire on_update — so on_update
// automation silently never ran for the relate/unrelate verbs, while it DID run
// for the equivalent dep-add-related and for the proxied relate/unrelate
// handlers (29tyj). The parity was INVERTED: a hub-connected (proxied) crew's
// on_update ran for relate, a direct/embedded crew's did not.
//
// Driven END-TO-END through the actual `bd relate`/`bd unrelate` subprocess: a
// tx-helper unit test would false-green by skipping the post-commit hook
// plumbing entirely (the hook-parity family lesson — same as 7o4av/usumn).
// MUTATION-VERIFIED: revert the relate/unrelate hook-firing added in relate.go
// and relate_fires_on_update / unrelate_fires_on_update go RED (the marker file
// stays empty) while the dep-add-related control stays GREEN.
func TestRelateUnrelateFiresOnUpdateHook_emqz2(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// installOnUpdateHook writes an on_update marker script into the workspace
	// hooks dir and returns the marker path (empty until the hook fires).
	installOnUpdateHook := func(t *testing.T, beadsDir string) string {
		t.Helper()
		hooksDir := filepath.Join(beadsDir, "hooks")
		if err := os.MkdirAll(hooksDir, 0o755); err != nil {
			t.Fatalf("mkdir hooks: %v", err)
		}
		marker := filepath.Join(beadsDir, "on_update_marker.txt")
		script := "#!/bin/sh\necho fired >> " + marker + "\n"
		if err := os.WriteFile(filepath.Join(hooksDir, "on_update"), []byte(script), 0o755); err != nil {
			t.Fatalf("write on_update hook: %v", err)
		}
		return marker
	}

	// fireCount returns how many times the on_update hook fired (marker lines).
	// The hook runs as a fire-and-forget goroutine post-commit; poll briefly so
	// the assertion is not an async-timing artifact (matches the bead repro's
	// 2s settle).
	fireCount := func(t *testing.T, marker string) int {
		t.Helper()
		for attempt := 0; attempt < 40; attempt++ {
			if b, err := os.ReadFile(marker); err == nil {
				n := 0
				for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
					if strings.TrimSpace(ln) != "" {
						n++
					}
				}
				if n > 0 {
					return n
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		return 0
	}

	// resetMarker truncates the marker between phases.
	resetMarker := func(t *testing.T, marker string) {
		t.Helper()
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			t.Fatalf("reset marker: %v", err)
		}
	}

	// CONTROL: `bd dep add A B --type related` fires on_update (the authoritative
	// behavior the relate verb must mirror; also confirms the harness wires the
	// on_update hook at all).
	t.Run("dep_add_related_fires_on_update_control", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "ctl")
		marker := installOnUpdateHook(t, beadsDir)
		a := bdCreate(t, bd, dir, "control A").ID
		b := bdCreate(t, bd, dir, "control B").ID
		resetMarker(t, marker)
		if _, stderr, err := bdRun8m9o7(t, bd, dir, "dep", "add", a, b, "--type", "related"); err != nil {
			t.Fatalf("beads-emqz2: `dep add --type related` should succeed; err %v\nstderr:\n%s", err, stderr)
		}
		if n := fireCount(t, marker); n == 0 {
			t.Fatalf("beads-emqz2 CONTROL: `dep add --type related` must fire on_update (harness check); fired 0 times")
		}
	})

	t.Run("relate_fires_on_update", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "rel")
		marker := installOnUpdateHook(t, beadsDir)
		a := bdCreate(t, bd, dir, "relate A").ID
		b := bdCreate(t, bd, dir, "relate B").ID
		resetMarker(t, marker)
		if _, stderr, err := bdRun8m9o7(t, bd, dir, "dep", "relate", a, b); err != nil {
			t.Fatalf("beads-emqz2: `bd relate` should succeed; err %v\nstderr:\n%s", err, stderr)
		}
		if n := fireCount(t, marker); n == 0 {
			t.Fatalf("beads-emqz2: `bd relate %s %s` must fire on_update at parity with "+
				"`dep add --type related` (and the proxied relate handler, 29tyj); fired 0 times [BUG]", a, b)
		}
	})

	t.Run("unrelate_fires_on_update", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "unr")
		marker := installOnUpdateHook(t, beadsDir)
		a := bdCreate(t, bd, dir, "unrelate A").ID
		b := bdCreate(t, bd, dir, "unrelate B").ID
		// Establish the link first (via relate) so unrelate has an edge to remove.
		if _, stderr, err := bdRun8m9o7(t, bd, dir, "dep", "relate", a, b); err != nil {
			t.Fatalf("beads-emqz2: setup `bd relate` should succeed; err %v\nstderr:\n%s", err, stderr)
		}
		resetMarker(t, marker)
		if _, stderr, err := bdRun8m9o7(t, bd, dir, "dep", "unrelate", a, b); err != nil {
			t.Fatalf("beads-emqz2: `bd unrelate` should succeed; err %v\nstderr:\n%s", err, stderr)
		}
		if n := fireCount(t, marker); n == 0 {
			t.Fatalf("beads-emqz2: `bd unrelate %s %s` must fire on_update at parity with "+
				"`dep remove` (and the proxied unrelate handler, 29tyj); fired 0 times [BUG]", a, b)
		}
	})
}
