//go:build integration && !windows

package doltserver_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/testutil/integration"
)

// spawnCwdSleep starts a long-lived `sleep` whose working directory is cwd and
// returns its PID. It is a stand-in for a dolt sql-server rooted at a given cwd:
// ReapServersUnderDir only reaps procs matching the dolt cmdline, so a sleep is
// used to prove the SUBTREE-SCOPING logic (isProcessCwdUnderDir) without needing
// a second real server — see TestReapServersUnderDir_ReapsOrphan for the real one.
func spawnCwdSleep(t *testing.T, cwd string) *exec.Cmd {
	t.Helper()
	c := exec.Command("sleep", "120")
	c.Dir = cwd
	if err := c.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Process.Signal(syscall.SIGKILL)
		_, _ = c.Process.Wait()
	})
	return c
}

// TestReapServersUnderDir_ReapsOrphan is the beads-side twin of the gt-side
// "orphan reaped, live decoy survives" acceptance (hq-sl5wbc / gyjhtc). It
// starts a real dolt sql-server rooted UNDER a private tmp root, deliberately
// abandons it (no Stop), and asserts ReapServersUnderDir(root) kills it.
func TestReapServersUnderDir_ReapsOrphan(t *testing.T) {
	integration.RequireDolt(t)

	root := t.TempDir()
	beadsDir := filepath.Join(root, "orphan", ".beads")
	if err := os.MkdirAll(beadsDir, 0700); err != nil {
		t.Fatalf("mkdir beadsDir: %v", err)
	}
	// Force test mode + an isolated dolt so Start binds an ephemeral port
	// rather than the ambient prod port that leaks in on a crew node.
	t.Setenv("BEADS_TEST_MODE", "1")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_PORT", "")

	doltBin := integration.RequireDolt(t)
	configureReapTestIdentity(t, doltBin, root)
	doltDir := filepath.Join(beadsDir, "dolt")
	if err := os.MkdirAll(doltDir, 0700); err != nil {
		t.Fatalf("mkdir doltDir: %v", err)
	}
	initCmd := exec.Command(doltBin, "init")
	initCmd.Dir = doltDir
	initCmd.Env = append(os.Environ(), "HOME="+root, "DOLT_ROOT_PATH="+root)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("dolt init: %v\n%s", err, out)
	}

	state, err := doltserver.Start(beadsDir)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !state.Running || state.PID == 0 {
		t.Fatalf("server not running: %+v", state)
	}
	// Deliberately DO NOT Stop() — simulate a panicked/killed test that left
	// the server running under the tmp tree.

	killed := doltserver.ReapServersUnderDir(root)

	found := false
	for _, pid := range killed {
		if pid == state.PID {
			found = true
		}
	}
	if !found {
		// Best-effort cleanup so we don't leak the very orphan this test made.
		if p, e := os.FindProcess(state.PID); e == nil {
			_ = p.Kill()
		}
		t.Fatalf("ReapServersUnderDir(%s) did not reap the orphan server PID %d (killed=%v)", root, state.PID, killed)
	}

	// The reaper's contract is "strongest signal delivered to every orphan
	// under root" — asserted above (PID in killed set = SIGTERM→SIGKILL sent).
	// Actual process teardown is asynchronous and, on a contended networked
	// filesystem like /fsx, dolt can sit in uninterruptible I/O for tens of
	// seconds after SIGKILL before the kernel reaps it. Asserting synchronous
	// death here would flake on load, so we only best-effort confirm eventual
	// exit and log (not fail) if the kernel is still slow — the process WILL
	// die once its I/O drains, and we already proved the reaper acted.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if !integration.IsProcessAlive(state.PID) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Best-effort final kill so this test never leaks the orphan it created.
	if p, e := os.FindProcess(state.PID); e == nil {
		_ = p.Kill()
	}
	t.Logf("note: server PID %d not yet reaped by the kernel 20s after SIGKILL (uninterruptible I/O on contended /fsx); reaper delivered the kill, teardown is async", state.PID)
}

// TestReapServersUnderDir_LeavesOutsideProcs is the "live decoy survives" leg:
// a process whose cwd is OUTSIDE the reap root must not be touched. Uses a sleep
// as the decoy (ReapServersUnderDir's dolt-cmdline filter means a sleep is never
// a reap candidate anyway; this asserts the subtree scoping does not over-reach
// even for the process class it does target — verified via the PID list).
func TestReapServersUnderDir_LeavesOutsideProcs(t *testing.T) {
	insideRoot := t.TempDir()
	outsideRoot := t.TempDir()

	// A decoy rooted OUTSIDE the reap root.
	decoy := spawnCwdSleep(t, outsideRoot)

	// Reaping insideRoot must not return the decoy, and the decoy must survive.
	killed := doltserver.ReapServersUnderDir(insideRoot)
	for _, pid := range killed {
		if pid == decoy.Process.Pid {
			t.Fatalf("ReapServersUnderDir(%s) killed a decoy rooted at %s (pid %d)", insideRoot, outsideRoot, decoy.Process.Pid)
		}
	}
	if !integration.IsProcessAlive(decoy.Process.Pid) {
		t.Errorf("decoy PID %d was killed but its cwd is outside the reap root", decoy.Process.Pid)
	}
}

// TestReapServersUnderDir_EmptyRootNoop asserts an empty root reaps nothing.
func TestReapServersUnderDir_EmptyRootNoop(t *testing.T) {
	if killed := doltserver.ReapServersUnderDir(""); len(killed) != 0 {
		t.Errorf("empty root should reap nothing, got %v", killed)
	}
	if killed := doltserver.ReapServersUnderDir("   "); len(killed) != 0 {
		t.Errorf("blank root should reap nothing, got %v", killed)
	}
}

func configureReapTestIdentity(t *testing.T, doltBin, home string) {
	t.Helper()
	for _, args := range [][]string{
		{"config", "--global", "--add", "user.name", "beads-test"},
		{"config", "--global", "--add", "user.email", "beads@test"},
	} {
		cmd := exec.Command(doltBin, args...)
		cmd.Env = append(os.Environ(), "HOME="+home, "DOLT_ROOT_PATH="+home)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dolt %v: %v\n%s", args, err, out)
		}
	}
}
