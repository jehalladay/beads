package proxy_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/dbproxy/pidfile"
	"github.com/steveyegge/beads/internal/storage/dbproxy/proxy"
	"github.com/steveyegge/beads/internal/storage/dbproxy/server"
	"github.com/steveyegge/beads/internal/storage/dbproxy/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover proxy.Shutdown / shutdownPair hermetically — no fork+exec
// of a real dolt sql-server, no live process. The two happy branches (lock
// free, stale pidfile) and the "lock held by a dead PID" timeout branch are all
// reachable with just a temp dir + the exported flock/pidfile helpers.

// deadPID returns a PID that is (almost certainly) not a live process, so that
// os.FindProcess(...).Kill() is a no-op and the lock is never actually released.
const deadPID = 0x7fffffff // 2147483647, above any realistic Linux pid_max

// TestShutdown_NoLocksHeld: an empty rootDir has neither proxy nor child
// running, so each shutdownPair acquires the flock immediately and returns nil.
func TestShutdown_NoLocksHeld(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, proxy.Shutdown(dir))
}

// TestShutdown_StalePidfilesNoLock: pidfiles left behind by dead processes but
// no live flock holder — Shutdown acquires each lock and removes the stale
// pidfiles, returning nil.
func TestShutdown_StalePidfilesNoLock(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, pidfile.Write(dir, proxy.PIDFileName, pidfile.PidFile{Pid: deadPID, Port: 1234}))
	require.NoError(t, pidfile.Write(dir, server.PIDFileName, pidfile.PidFile{Pid: deadPID, Port: 5678}))

	require.NoError(t, proxy.Shutdown(dir))

	// Stale pidfiles are cleaned up on success.
	pf, err := pidfile.Read(dir, proxy.PIDFileName)
	require.NoError(t, err)
	assert.Nil(t, pf, "proxy pidfile should be removed")
	cpf, err := pidfile.Read(dir, server.PIDFileName)
	require.NoError(t, err)
	assert.Nil(t, cpf, "child pidfile should be removed")
}

// TestShutdown_ChildLockHeldByDeadPID: the child (dolt sql-server) lock is held
// and its pidfile records a dead PID. shutdownPair reads the pidfile, tries to
// SIGKILL the (already-dead) PID, then polls the flock until the confirmation
// deadline — which it never crosses because the test still holds the lock — so
// Shutdown returns a timeout error naming the child lock.
func TestShutdown_ChildLockHeldByDeadPID(t *testing.T) {
	dir := t.TempDir()

	// Hold the child lock for the whole test so the poll loop can never
	// reacquire it (simulates a holder whose PID we can't actually kill).
	lock, err := util.TryLock(filepath.Join(dir, server.LockFileName))
	require.NoError(t, err)
	defer lock.Unlock()

	require.NoError(t, pidfile.Write(dir, server.PIDFileName, pidfile.PidFile{Pid: deadPID, Port: 5678}))

	err = proxy.Shutdown(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dolt sql-server", "error should attribute the failure to the child pair")
	assert.Contains(t, err.Error(), "timeout", "error should be the confirmation timeout")
}

// TestShutdown_ProxyLockHeldByDeadPID: the child pair is clean but the proxy's
// own lock is held with a dead PID recorded — Shutdown clears the child, then
// times out on the proxy lock.
func TestShutdown_ProxyLockHeldByDeadPID(t *testing.T) {
	dir := t.TempDir()

	lock, err := util.TryLock(filepath.Join(dir, proxy.LockFileName))
	require.NoError(t, err)
	defer lock.Unlock()

	require.NoError(t, pidfile.Write(dir, proxy.PIDFileName, pidfile.PidFile{Pid: deadPID, Port: 1234}))

	err = proxy.Shutdown(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "proxy:", "error should attribute the failure to the proxy pair")
	assert.Contains(t, err.Error(), "timeout")
}

// TestShutdown_LockReleasedDuringPoll: the child lock is held with a dead PID
// recorded, but a concurrent goroutine releases the lock shortly after Shutdown
// starts (simulating the recorded process finally exiting and the OS dropping
// its flock). shutdownPair's poll loop then reacquires the lock and returns nil
// — exercising the success-after-poll branch, not just the timeout branch.
func TestShutdown_LockReleasedDuringPoll(t *testing.T) {
	dir := t.TempDir()

	lock, err := util.TryLock(filepath.Join(dir, server.LockFileName))
	require.NoError(t, err)
	require.NoError(t, pidfile.Write(dir, server.PIDFileName, pidfile.PidFile{Pid: deadPID, Port: 5678}))

	// Release the lock after a couple of poll intervals so the loop's next
	// TryLock succeeds well before the 5s confirmation deadline.
	go func() {
		time.Sleep(200 * time.Millisecond)
		lock.Unlock()
	}()

	require.NoError(t, proxy.Shutdown(dir))

	// The (stale) child pidfile is removed once the lock is reacquired.
	pf, err := pidfile.Read(dir, server.PIDFileName)
	require.NoError(t, err)
	assert.Nil(t, pf, "child pidfile should be removed after successful shutdown")
}

// TestShutdown_LockHeldNoPidfile: a lock is held but there is no pidfile to read
// (Read returns nil, nil) — shutdownPair skips the kill and still polls to the
// deadline, returning a timeout error rather than panicking on a nil pidfile.
func TestShutdown_LockHeldNoPidfile(t *testing.T) {
	dir := t.TempDir()

	lock, err := util.TryLock(filepath.Join(dir, server.LockFileName))
	require.NoError(t, err)
	defer lock.Unlock()

	// No pidfile written.
	_, statErr := os.Stat(filepath.Join(dir, server.PIDFileName))
	require.True(t, os.IsNotExist(statErr))

	err = proxy.Shutdown(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

// TestShutdown_ProbeError: when the very first TryLock fails with a non-lock
// error (not "already locked"), shutdownPair returns a wrapped "probe" error
// rather than proceeding to the kill/poll path. We force this by passing a
// rootDir that is a regular FILE, so util.TryLock's MkdirAll(filepath.Dir(
// lockPath)) — i.e. MkdirAll(<the file>) — fails with "not a directory". The
// child pair is probed first, so the error attributes to the sql-server pair.
func TestShutdown_ProbeError(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "iamafile")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))

	err := proxy.Shutdown(notADir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dolt sql-server", "error should attribute to the child pair probed first")
	assert.Contains(t, err.Error(), "probe", "error should be the probe (non-lock) failure, not a timeout")
	assert.NotContains(t, err.Error(), "timeout", "must not fall through to the poll/timeout path")
}
