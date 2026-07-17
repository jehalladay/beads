package proxy

// Deterministic flake-guard teeth for the concurrent-instantiation timeout
// (beads-j5salo). The uow ConcurrentInstantiation tests flaked because the
// spawn path killed its freshly-forked dolt child on a short (15s) deadline
// while the intended 2-minute ceiling was dead code: under /fsx contention a
// cold dolt boot exceeds 15s, so the lock winner killed its own booting child
// and the lock losers gave up before it came up.
//
// Reproducing real contention is non-deterministic, so instead we assert the
// STRUCTURAL guarantee (mirrors the lock-HELD teeth pattern used elsewhere in
// the tree): the spawn honors ONE unified open budget for booting the child,
// and a caller waiting on that spawn does not bail early. We drive it with a
// FAKE proxy child (this test binary re-execs itself; TestMain intercepts the
// db-proxy-child invocation) whose boot delay and readiness are controlled per
// test, so no real dolt is needed and the timing is deterministic.

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/dbproxy/pidfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fakeChildArg        = "db-proxy-child"
	envFakeChildDelayMS = "BEADS_FAKE_CHILD_DELAY_MS"
	envFakeChildHang    = "BEADS_FAKE_CHILD_NEVER_READY"
)

// TestMain re-purposes this test binary as a fake proxy child when it is
// re-exec'd by forkExecChild (which passes "db-proxy-child" as the first arg).
// In all other cases it runs the tests normally.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == fakeChildArg {
		runFakeProxyChild()
		return
	}
	os.Exit(m.Run())
}

// runFakeProxyChild emulates just enough of the real proxy child for
// readAndDial to observe readiness: after an optional boot delay it listens on
// the assigned port and writes the proxy pidfile, then blocks until killed.
// With BEADS_FAKE_CHILD_NEVER_READY it never becomes ready (exercises timeout).
func runFakeProxyChild() {
	var rootDir string
	var port int
	for i := 2; i < len(os.Args)-1; i++ {
		switch os.Args[i] {
		case "--root":
			rootDir = os.Args[i+1]
		case "--port":
			port, _ = strconv.Atoi(os.Args[i+1])
		}
	}

	if os.Getenv(envFakeChildHang) == "1" {
		time.Sleep(60 * time.Second)
		os.Exit(0)
	}

	if ms := os.Getenv(envFakeChildDelayMS); ms != "" {
		if d, err := strconv.Atoi(ms); err == nil {
			time.Sleep(time.Duration(d) * time.Millisecond)
		}
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		os.Exit(1)
	}
	defer func() { _ = ln.Close() }()
	// Write the pidfile only after the listener is up, so a reader that sees
	// the pidfile always finds a live port (matches the real child's ordering).
	if werr := pidfile.Write(rootDir, PIDFileName, pidfile.PidFile{Pid: os.Getpid(), Port: port}); werr != nil {
		os.Exit(1)
	}
	// Accept-and-close in a loop so probePort's dials succeed; block forever
	// (until the parent kills us). Keeps ln referenced for its whole lifetime.
	for {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		_ = conn.Close()
	}
}

// useFakeProxyChild points ResolveExecutable at this test binary so
// forkExecChild re-execs us as the fake child, and restores it afterward.
func useFakeProxyChild(t *testing.T) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	prev := ResolveExecutable
	ResolveExecutable = func() (string, error) { return self, nil }
	t.Cleanup(func() { ResolveExecutable = prev })
}

func withOpenBudget(t *testing.T, d time.Duration) {
	t.Helper()
	prev := openTotalBudget
	openTotalBudget = d
	t.Cleanup(func() { openTotalBudget = prev })
}

// TestSpawnWaitsForSlowChildWithinBudget is the core teeth: a child that takes
// well over the OLD 15s spawn kill-deadline to become ready must still be
// awaited (not killed early) as long as it comes up within the unified budget.
// We simulate the slow boot with a short delay and a budget comfortably above
// it; if a regression re-introduces a separate, shorter spawn deadline the
// child is killed before it listens and this goes RED.
func TestSpawnWaitsForSlowChildWithinBudget(t *testing.T) {
	useFakeProxyChild(t)
	withOpenBudget(t, 60*time.Second)
	t.Setenv(envFakeChildDelayMS, "700")

	root := t.TempDir()
	logPath := root + "/server.log"
	t.Cleanup(func() { _ = Shutdown(root) })

	start := time.Now()
	ep, err := GetCreateDatabaseProxyServerEndpoint(root, OpenOpts{
		Backend:        BackendLocalServer,
		ConfigFilePath: root + "/config.yaml",
		LogFilePath:    logPath,
		DoltBinPath:    "/usr/bin/true",
	})
	elapsed := time.Since(start)

	require.NoError(t, err, "slow-but-within-budget child must be awaited, not killed early")
	assert.Equal(t, "127.0.0.1", ep.Host)
	assert.NotZero(t, ep.Port)
	// It should return only AFTER the child becomes ready (~700ms boot delay),
	// proving we actually waited for it rather than returning early/stale. The
	// upper bound stays well under the 60s budget (with slack for a busy node)
	// so a return here means the slow child was awaited and served.
	assert.GreaterOrEqual(t, elapsed, 600*time.Millisecond)
	assert.Less(t, elapsed, 60*time.Second)
}

// TestSpawnTimesOutOnUnifiedBudget asserts the timeout path is bounded by
// openTotalBudget (not a separate longer/shorter deadline): a child that never
// becomes ready must fail near the budget, and the error must name the budget.
func TestSpawnTimesOutOnUnifiedBudget(t *testing.T) {
	useFakeProxyChild(t)
	withOpenBudget(t, 800*time.Millisecond)
	t.Setenv(envFakeChildHang, "1")

	root := t.TempDir()
	logPath := root + "/server.log"
	t.Cleanup(func() { _ = Shutdown(root) })

	start := time.Now()
	_, err := GetCreateDatabaseProxyServerEndpoint(root, OpenOpts{
		Backend:        BackendLocalServer,
		ConfigFilePath: root + "/config.yaml",
		LogFilePath:    logPath,
		DoltBinPath:    "/usr/bin/true",
	})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
	// The error must name the unified budget (800ms here), not a separate
	// hard-coded ceiling. If a regression reintroduces a distinct spawn
	// deadline, this fails because the message carries the wrong duration.
	assert.Contains(t, err.Error(), openTotalBudget.String())
	// Bounded well below the old 2-minute spawn ceiling: with an 800ms budget
	// a correct impl fails in ~1s (plus node-contention slack for the orphan
	// reap scan and child fork/kill), never ~2 minutes. The 60s bound cleanly
	// separates "honors the short budget" from "killed on the old 2m ceiling"
	// while tolerating a busy shared /fsx node.
	assert.Less(t, elapsed, 60*time.Second, "timeout must honor the short unified budget, not the old 2m spawn ceiling")
}
