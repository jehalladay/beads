package proxy

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/steveyegge/beads/internal/lockfile"
	"github.com/steveyegge/beads/internal/storage/db/util"
)

const lockFileName = "proxy.lock"

type Endpoint struct {
	Host string
	Port int
}

func (e Endpoint) Address() string {
	return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

type OpenOpts struct {
	IdleTimeout time.Duration
}

const openDeadline = 15 * time.Second

func Open(rootDir string, opts OpenOpts) (Endpoint, error) {
	deadline := time.Now().Add(openDeadline)
	for {
		lock, err := util.TryLock(filepath.Join(rootDir, lockFileName))
		if err == nil {
			ep, spawnErr := spawnAndHandoff(rootDir, opts, lock.File(), deadline)
			// Close only the parent's fd; if the child inherited the fd via
			// fork+exec, the lock persists through the child's reference. Do
			// NOT call lock.Unlock() here — that would LOCK_UN the OFD and
			// drop the child's lock too.
			_ = lock.File().Close()
			return ep, spawnErr
		}
		if !lockfile.IsLocked(err) {
			return Endpoint{}, fmt.Errorf("acquire lock: %w", err)
		}

		if ep, ok := readAndDial(rootDir); ok {
			return ep, nil
		}

		if time.Now().After(deadline) {
			return Endpoint{}, fmt.Errorf("timeout waiting for proxy on %s", rootDir)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// spawnAndHandoff fork+execs the proxy child with the lock fd inherited, then
// waits for the child to bind and write its pidfile. The caller still owns
// lockFile and must Close() it after this returns; the child holds an
// inherited fd to the same open file description, so the flock persists.
func spawnAndHandoff(rootDir string, opts OpenOpts, lockFile *os.File, deadline time.Time) (Endpoint, error) {
	// Stale pidfile from a previous (now-dead) proxy must not mislead racing
	// readers into dialing a port that nobody is listening on.
	_ = RemoveDatabaseProxyPidFile(rootDir)

	port, err := pickFreePort()
	if err != nil {
		return Endpoint{}, fmt.Errorf("pick port: %w", err)
	}

	cmd, err := forkExecChild(rootDir, port, opts.IdleTimeout, lockFile)
	if err != nil {
		return Endpoint{}, fmt.Errorf("fork child: %w", err)
	}

	for {
		if ep, ok := readAndDial(rootDir); ok {
			return ep, nil
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			return Endpoint{}, fmt.Errorf("timeout waiting for proxy to become ready on port %d", port)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

func forkExecChild(rootDir string, port int, idleTimeout time.Duration, lockFile *os.File) (*exec.Cmd, error) {
	// TODO: implement me
	return nil, fmt.Errorf("forkExecChild: TODO implement me")
}

func readAndDial(rootDir string) (Endpoint, bool) {
	pf, err := ReadDatabaseProxyPidFile(rootDir)
	if err != nil || pf == nil {
		return Endpoint{}, false
	}
	ep := Endpoint{Host: "127.0.0.1", Port: pf.Port}
	if !probePort(ep, 500*time.Millisecond) {
		return Endpoint{}, false
	}
	return ep, true
}

func probePort(ep Endpoint, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", ep.Address(), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
