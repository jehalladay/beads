//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestDepAddAliasConflict_bscdj pins the beads-bscdj fix: `bd dep add` documents
// --blocked-by and --depends-on as ALIASES for the same depends-on target, but
// the RunE resolvers (direct dep.go + proxied dep_proxied_server.go) pick them
// via a priority chain that takes blocked-by first. So
// `bd dep add A --depends-on X --blocked-by Y` silently used Y and DISCARDED X
// with rc0 and no diagnostic — the dz1t8 input-source silent-drop class (same
// as 637yc bd-edit field flags and comment/note positional-vs-flag). The fix
// rejects the two-alias combo at cobra parse time via MarkFlagsMutuallyExclusive;
// being pre-store it is twin-safe (covers direct + proxied identically).
//
// Mutation check: remove the
//   depAddCmd.MarkFlagsMutuallyExclusive("blocked-by", "depends-on")
// line in dep.go's init() and the two_aliases_rejected subtest goes RED (the
// command succeeds rc0, blocked-by wins, and --depends-on is silently dropped).
func TestDepAddAliasConflict_bscdj(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "da")

	runDep := func(t *testing.T, args ...string) (string, bool) {
		t.Helper()
		full := append([]string{"dep", "add"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		return string(out), err != nil
	}

	t.Run("two_aliases_rejected", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "dep A", "--type", "task")
		x := bdCreate(t, bd, dir, "dep X", "--type", "task")
		y := bdCreate(t, bd, dir, "dep Y", "--type", "task")
		out, failed := runDep(t, a.ID, "--depends-on", x.ID, "--blocked-by", y.ID)
		if !failed {
			t.Fatalf("bd dep add %s --depends-on %s --blocked-by %s must be rejected (aliases are mutually exclusive), got success:\n%s",
				a.ID, x.ID, y.ID, out)
		}
		// cobra's mutually-exclusive error names the flag group.
		if !strings.Contains(out, "none of the others can be") {
			t.Errorf("expected a mutually-exclusive rejection naming --blocked-by/--depends-on, got:\n%s", out)
		}
	})

	// Regression: each alias alone still works (the guard must not over-reject).
	t.Run("depends_on_alone_ok", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "dep A2", "--type", "task")
		x := bdCreate(t, bd, dir, "dep X2", "--type", "task")
		if out, failed := runDep(t, a.ID, "--depends-on", x.ID); failed {
			t.Fatalf("bd dep add %s --depends-on %s (alone) must succeed, got failure:\n%s", a.ID, x.ID, out)
		}
	})

	t.Run("blocked_by_alone_ok", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "dep A3", "--type", "task")
		y := bdCreate(t, bd, dir, "dep Y3", "--type", "task")
		if out, failed := runDep(t, a.ID, "--blocked-by", y.ID); failed {
			t.Fatalf("bd dep add %s --blocked-by %s (alone) must succeed, got failure:\n%s", a.ID, y.ID, out)
		}
	})

	// Regression: the positional 2-arg form still works (no flags at all).
	t.Run("positional_two_arg_ok", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "dep A4", "--type", "task")
		y := bdCreate(t, bd, dir, "dep Y4", "--type", "task")
		if out, failed := runDep(t, a.ID, y.ID); failed {
			t.Fatalf("bd dep add %s %s (positional) must succeed, got failure:\n%s", a.ID, y.ID, out)
		}
	})
}
