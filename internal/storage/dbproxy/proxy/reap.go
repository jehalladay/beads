package proxy

import (
	"path/filepath"
	"time"
)

// Orphan-dolt reaping (beads-pu8c).
//
// DoltServer spawns its dolt sql-server detached (Setsid) with cmd.Dir set to
// the data dir, and the supervising proxy-child reaps it on graceful Stop. But
// if the proxy-child is SIGKILLed (OOM, kill -9, host reboot) it never runs
// Stop, so the dolt process is reparented to init and keeps holding its
// configured port. The OS then releases the proxy-child flock, so the next
// spawnAndHandoff sees the flock FREE and takes the no-kill branch — the
// orphaned dolt is never reaped and the freshly-spawned server cannot bind its
// port, wedging the workspace on every subsequent bd invocation.
//
// reapOrphanedDoltServers closes that gap: on the acquire-succeeds branch,
// before spawning a replacement, it kills any dolt sql-server whose working
// directory is this rootDir (the same identity check the standalone
// internal/doltserver supervisor uses). The seams are package vars so tests can
// inject fakes without real processes.
//
// NOTE (consolidation owed): listDoltServerPIDs / doltProcessInDir / stopProcess
// mirror the unexported primitives in internal/doltserver (doltserver_unix.go:
// listDoltProcessPIDs / isProcessInDir / gracefulStop). They are duplicated
// rather than imported to avoid a new cross-package dependency; a future change
// should lift them into a shared internal helper and delete this mirror
// (tracked on beads-pu8c).
var (
	// listDoltServerPIDs returns the PIDs of all running dolt sql-server
	// processes (zombies excluded).
	listDoltServerPIDs = defaultListDoltServerPIDs
	// doltProcessInDir reports whether the process's working directory is dir.
	doltProcessInDir = defaultDoltProcessInDir
	// stopProcess terminates a process (SIGTERM, wait, then SIGKILL on Unix).
	stopProcess = defaultStopProcess
)

const orphanReapStopTimeout = 5 * time.Second

// reapOrphanedDoltServers terminates every dolt sql-server whose working
// directory is rootDir. It is called on the spawnAndHandoff acquire-succeeds
// branch (previous proxy-child dead) so an orphaned, still-listening dolt is
// cleared before a replacement is spawned. Best-effort: individual stop
// failures are ignored; the subsequent bind/readiness check is the backstop.
func reapOrphanedDoltServers(rootDir string) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		absRoot = rootDir
	}
	for _, pid := range listDoltServerPIDs() {
		if doltProcessInDir(pid, absRoot) {
			_ = stopProcess(pid, orphanReapStopTimeout)
		}
	}
}
