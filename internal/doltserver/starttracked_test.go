//go:build integration && !windows

package doltserver_test

import (
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/testutil/integration"
)

// startTracked starts a managed dolt server for beadsDir and registers its PID
// with reg IMMEDIATELY, closing the register-timing window that the old
// Start()->os.FindProcess()->reg.Register() sequence left open: an external
// SIGKILL (go test -timeout, panic) landing between Start returning and the
// caller registering the PID would orphan the detached child, because t.Cleanup
// (which normally reaps via reg) does not run on external SIGKILL. Registering
// inside this helper — before returning to the caller — narrows that window to
// nothing observable by the caller.
//
// As a second-line backstop for the no-cleanup-runs path (external SIGKILL kills
// the whole binary before ANY t.Cleanup fires), startTracked also installs a
// DIR-SCOPED t.Cleanup that Stops the server and then runs
// doltserver.ReapServersUnderDir(beadsDir). ReapServersUnderDir only kills dolt
// processes whose working directory is under beadsDir (a per-test t.TempDir), so
// it is SAFE on the shared /fsx node: it never touches the production hub
// (172.31.26.56) or concurrent peer-test dolt servers rooted elsewhere. The
// backstop is idempotent and per-server — a childless leak is a no-op.
//
// startTracked never calls t.Fatal, so it is safe to invoke from a goroutine
// (e.g. the concurrent-start races in port_race_test.go); the caller inspects
// the returned error. On a Start error the PID (if any) is still registered so a
// half-started child is reaped.
func startTracked(t *testing.T, reg *integration.ProcessRegistry, beadsDir string) (*doltserver.State, error) {
	t.Helper()
	state, err := doltserver.Start(beadsDir)
	// Register FIRST — before the error check — so even a partially-started
	// server (non-zero PID with a non-nil error) is tracked for teardown.
	if state != nil && state.PID != 0 {
		if p, ferr := os.FindProcess(state.PID); ferr == nil {
			reg.Register(p)
		}
	}
	// Dir-scoped backstop: covers the case where reg's own t.Cleanup cannot run
	// (external SIGKILL of the whole binary) OR the PID moved (adoption). Only
	// reaps dolt rooted under this test's beadsDir, never prod/peer servers.
	t.Cleanup(func() {
		_ = doltserver.Stop(beadsDir)
		_ = doltserver.ReapServersUnderDir(beadsDir)
	})
	return state, err
}
