package scripts_test

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// port-open.sh (beads-l3po) is the portable TCP-readiness probe that lets the
// shared test Dolt server (BEADS_TEST_SHARED_SERVER=1) activate on nodes without
// `nc`. The shared-server speedup was silently disabled on the /fsx refinery
// nodes because the readiness check was `nc -z` and those nodes ship no `nc` —
// the probe errored, never reported ready, and test.sh fell back to per-package
// servers (the slow ~10min gate). beads_port_open probes via nc → bash /dev/tcp
// → python3, so it no longer hinges on an optional binary.
//
// These tests drive the shell function directly by sourcing the lib and calling
// beads_port_open, using BEADS_PORT_OPEN_FORCE to pin each backend so we cover
// the /dev/tcp and python paths deterministically regardless of what's on the
// box (the whole point is not depending on nc). A real listener gives a true
// "open"; an unused port gives "closed".

// runProbe sources port-open.sh and runs `beads_port_open host port`, returning
// the exit code. force pins the backend ("" = auto).
func runProbe(t *testing.T, host string, port int, force string) int {
	t.Helper()
	root := repoRoot(t)
	lib := filepath.Join(root, "scripts", "ci", "lib", "port-open.sh")
	if _, err := os.Stat(lib); err != nil {
		t.Fatalf("scripts/ci/lib/port-open.sh not found: %v", err)
	}
	script := fmt.Sprintf(`source %q; beads_port_open %s %d`, lib, host, port)
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = os.Environ()
	if force != "" {
		cmd.Env = append(cmd.Env, "BEADS_PORT_OPEN_FORCE="+force)
	}
	err := cmd.Run()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	t.Fatalf("running probe failed to launch: %v", err)
	return -1
}

// listenTCP opens a real listener and returns its port + a closer.
func listenTCP(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return port, func() { _ = ln.Close() }
}

// freeClosedPort returns a port number that is (very likely) not listening: we
// bind then immediately release it.
func freeClosedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func TestPortOpen_DevTCPDetectsListenerWithoutNC(t *testing.T) {
	port, closeLn := listenTCP(t)
	defer closeLn()

	// The FIX path: /dev/tcp probe (no nc). Must report OPEN for a real listener.
	if code := runProbe(t, "127.0.0.1", port, "devtcp"); code != 0 {
		t.Fatalf("beads_port_open (devtcp) on a live listener returned %d, want 0 (open)", code)
	}

	// And DOWN for a closed port — otherwise the probe would false-positive and
	// test.sh would proceed against a dead server.
	closed := freeClosedPort(t)
	if code := runProbe(t, "127.0.0.1", closed, "devtcp"); code == 0 {
		t.Fatalf("beads_port_open (devtcp) on a closed port returned 0 (open), want non-zero (closed)")
	}
}

func TestPortOpen_PythonFallbackDetectsListener(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	port, closeLn := listenTCP(t)
	defer closeLn()

	if code := runProbe(t, "127.0.0.1", port, "python"); code != 0 {
		t.Fatalf("beads_port_open (python) on a live listener returned %d, want 0 (open)", code)
	}
	closed := freeClosedPort(t)
	if code := runProbe(t, "127.0.0.1", closed, "python"); code == 0 {
		t.Fatalf("beads_port_open (python) on a closed port returned 0 (open), want non-zero (closed)")
	}
}

// TestPortOpen_AutoDetectWorksWithNCMaskedFromPATH is the core regression: with
// `nc` removed from PATH (simulating the /fsx refinery node), auto-detect must
// STILL correctly probe a live listener via the /dev/tcp fallback. Before the
// fix (nc-only), this path could not report ready and the shared server was
// silently disabled.
func TestPortOpen_AutoDetectWorksWithNCMaskedFromPATH(t *testing.T) {
	port, closeLn := listenTCP(t)
	defer closeLn()

	root := repoRoot(t)
	lib := filepath.Join(root, "scripts", "ci", "lib", "port-open.sh")

	// Build a minimal PATH that deliberately EXCLUDES any dir containing nc, but
	// keeps bash/python3. Simplest robust approach: put only a scratch bin dir +
	// the dirs needed for bash builtins (none — /dev/tcp is a builtin) and
	// python3's dir. We copy python3 resolution but drop nc by using a curated
	// PATH of just the python3 dir.
	pyPath := ""
	if p, err := exec.LookPath("python3"); err == nil {
		pyPath = filepath.Dir(p)
	}
	scratch := t.TempDir() // empty: guarantees no nc here
	newPATH := scratch
	if pyPath != "" {
		newPATH = scratch + string(os.PathListSeparator) + pyPath
	}

	script := fmt.Sprintf(`source %q; beads_port_open 127.0.0.1 %d`, lib, port)
	cmd := exec.Command("bash", "-c", script)
	// Fresh env with the curated PATH (no nc reachable).
	cmd.Env = append(filteredEnvWithoutPATH(), "PATH="+newPATH)

	// Sanity: nc must NOT be resolvable under this PATH, or the test proves
	// nothing about the fallback.
	check := exec.Command("bash", "-c", "command -v nc")
	check.Env = cmd.Env
	if out, _ := check.CombinedOutput(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("test setup: nc still resolvable under curated PATH (%q) — cannot prove the no-nc fallback", strings.TrimSpace(string(out)))
	}

	if err := cmd.Run(); err != nil {
		t.Fatalf("beads_port_open auto-detect with nc masked returned error %v (want 0/open via /dev/tcp) — the shared-server activation would be silently disabled on nc-less nodes", err)
	}
}

// filteredEnvWithoutPATH returns the current environment minus any PATH entry,
// so a test can set an isolated PATH.
func filteredEnvWithoutPATH() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
