package proxy

import (
	"path/filepath"
	"testing"
	"time"
)

// TestReapOrphanedDoltServers_StopsOnlyInDir verifies the beads-pu8c reap kills
// exactly the dolt sql-servers whose working directory is rootDir, and leaves
// other projects' dolt servers alone. Uses injected seams — no real processes.
func TestReapOrphanedDoltServers_StopsOnlyInDir(t *testing.T) {
	rootDir := t.TempDir()
	absRoot, _ := filepath.Abs(rootDir)
	otherDir := t.TempDir()

	// PID 111 is our orphan (CWD==rootDir); 222 is another project's server.
	cwdByPID := map[int]string{111: absRoot, 222: otherDir}

	origList, origInDir, origStop := listDoltServerPIDs, doltProcessInDir, stopProcess
	t.Cleanup(func() {
		listDoltServerPIDs, doltProcessInDir, stopProcess = origList, origInDir, origStop
	})

	listDoltServerPIDs = func() []int { return []int{111, 222} }
	doltProcessInDir = func(pid int, dir string) bool { return cwdByPID[pid] == dir }
	var stopped []int
	stopProcess = func(pid int, _ time.Duration) error {
		stopped = append(stopped, pid)
		return nil
	}

	reapOrphanedDoltServers(rootDir)

	if len(stopped) != 1 || stopped[0] != 111 {
		t.Fatalf("stopped = %v, want [111] (only the CWD==rootDir orphan)", stopped)
	}
}

// TestReapOrphanedDoltServers_NoOrphans is a clean no-op when nothing matches.
func TestReapOrphanedDoltServers_NoOrphans(t *testing.T) {
	origList, origInDir, origStop := listDoltServerPIDs, doltProcessInDir, stopProcess
	t.Cleanup(func() {
		listDoltServerPIDs, doltProcessInDir, stopProcess = origList, origInDir, origStop
	})

	listDoltServerPIDs = func() []int { return nil }
	doltProcessInDir = func(int, string) bool { return true }
	called := false
	stopProcess = func(int, time.Duration) error { called = true; return nil }

	reapOrphanedDoltServers(t.TempDir())

	if called {
		t.Fatal("stopProcess called with no dolt PIDs listed; want no-op")
	}
}

// TestReapOrphanedDoltServers_StopErrorIsBestEffort ensures a stop failure on
// one orphan does not prevent reaping the others (best-effort contract).
func TestReapOrphanedDoltServers_StopErrorIsBestEffort(t *testing.T) {
	rootDir := t.TempDir()
	absRoot, _ := filepath.Abs(rootDir)

	origList, origInDir, origStop := listDoltServerPIDs, doltProcessInDir, stopProcess
	t.Cleanup(func() {
		listDoltServerPIDs, doltProcessInDir, stopProcess = origList, origInDir, origStop
	})

	listDoltServerPIDs = func() []int { return []int{1, 2, 3} }
	doltProcessInDir = func(int, string) bool { return true }
	var attempts []int
	stopProcess = func(pid int, _ time.Duration) error {
		attempts = append(attempts, pid)
		if pid == 2 {
			return errTestStop
		}
		return nil
	}

	reapOrphanedDoltServers(absRoot)

	if len(attempts) != 3 {
		t.Fatalf("attempts = %v, want all 3 attempted despite pid 2 error", attempts)
	}
}

var errTestStop = &testStopErr{}

type testStopErr struct{}

func (*testStopErr) Error() string { return "stop failed" }
