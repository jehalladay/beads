//go:build cgo

package main

import (
	"strings"
	"testing"
	"time"
)

// beads-f2mf: hermetic tests for the remaining context.go accessors not covered
// by beads-bk6i (getDBPath/getLockTimeout/isSandboxMode/isVersionUpgradeDetected/
// getPreviousVersion) — each on BOTH the legacy-globals branch and the cmdCtx
// branch — plus admin.go requireServerMode (both server and embedded modes).
// requireServerMode's embedded-error path only exists under cgo (the nocgo
// usesSQLServer always returns true), hence the cgo build tag.

func TestContextAccessorsF2mf_GlobalsBranch(t *testing.T) {
	origDB, origLock, origSandbox, origUpgrade, origPrev := dbPath, lockTimeout, sandboxMode, versionUpgradeDetected, previousVersion
	origTestMode, origCtx := testModeUseGlobals, cmdCtx
	t.Cleanup(func() {
		dbPath, lockTimeout, sandboxMode, versionUpgradeDetected, previousVersion = origDB, origLock, origSandbox, origUpgrade, origPrev
		testModeUseGlobals, cmdCtx = origTestMode, origCtx
	})

	// Force the legacy-globals path.
	testModeUseGlobals = true
	cmdCtx = nil
	if !shouldUseGlobals() {
		t.Fatal("shouldUseGlobals should be true with testModeUseGlobals set")
	}

	dbPath = "/tmp/globals.db"
	lockTimeout = 7 * time.Second
	sandboxMode = true
	versionUpgradeDetected = true
	previousVersion = "1.2.3"

	if getDBPath() != "/tmp/globals.db" {
		t.Errorf("getDBPath = %q, want /tmp/globals.db", getDBPath())
	}
	if getLockTimeout() != 7*time.Second {
		t.Errorf("getLockTimeout = %v, want 7s", getLockTimeout())
	}
	if !isSandboxMode() {
		t.Error("isSandboxMode should read global sandboxMode=true")
	}
	if !isVersionUpgradeDetected() {
		t.Error("isVersionUpgradeDetected should read global versionUpgradeDetected=true")
	}
	if getPreviousVersion() != "1.2.3" {
		t.Errorf("getPreviousVersion = %q, want 1.2.3", getPreviousVersion())
	}
}

func TestContextAccessorsF2mf_CmdCtxBranch(t *testing.T) {
	origTestMode, origCtx := testModeUseGlobals, cmdCtx
	t.Cleanup(func() { testModeUseGlobals, cmdCtx = origTestMode, origCtx })

	// Force the cmdCtx path: not test-mode, non-nil context.
	testModeUseGlobals = false
	cmdCtx = &CommandContext{
		DBPath:                 "/ctx/path.db",
		LockTimeout:            11 * time.Second,
		SandboxMode:            false,
		VersionUpgradeDetected: false,
		PreviousVersion:        "0.9.0",
	}
	if shouldUseGlobals() {
		t.Fatal("shouldUseGlobals should be false with a non-nil cmdCtx and test-mode off")
	}

	if getDBPath() != "/ctx/path.db" {
		t.Errorf("getDBPath = %q, want /ctx/path.db", getDBPath())
	}
	if getLockTimeout() != 11*time.Second {
		t.Errorf("getLockTimeout = %v, want 11s", getLockTimeout())
	}
	if isSandboxMode() {
		t.Error("isSandboxMode should read cmdCtx.SandboxMode=false")
	}
	if isVersionUpgradeDetected() {
		t.Error("isVersionUpgradeDetected should read cmdCtx.VersionUpgradeDetected=false")
	}
	if getPreviousVersion() != "0.9.0" {
		t.Errorf("getPreviousVersion = %q, want 0.9.0", getPreviousVersion())
	}
}

func TestRequireServerMode(t *testing.T) {
	origServer, origProxied := serverMode, proxiedServerMode
	origTestMode, origCtx := testModeUseGlobals, cmdCtx
	t.Cleanup(func() {
		serverMode, proxiedServerMode = origServer, origProxied
		testModeUseGlobals, cmdCtx = origTestMode, origCtx
	})

	// Drive usesSQLServer via the legacy-globals branch. (IsSharedServerMode is
	// off in the test env: BEADS_DOLT_SHARED_SERVER unset + dolt.shared-server
	// default false.)
	testModeUseGlobals = true
	cmdCtx = nil

	t.Run("server mode: no error", func(t *testing.T) {
		serverMode, proxiedServerMode = true, false
		if err := requireServerMode("cleanup"); err != nil {
			t.Fatalf("requireServerMode in server mode should be nil, got %v", err)
		}
	})

	t.Run("proxied server mode: no error", func(t *testing.T) {
		serverMode, proxiedServerMode = false, true
		if err := requireServerMode("compact"); err != nil {
			t.Fatalf("requireServerMode in proxied mode should be nil, got %v", err)
		}
	})

	t.Run("embedded mode: errors with cmd name", func(t *testing.T) {
		serverMode, proxiedServerMode = false, false
		err := requireServerMode("reset")
		if err == nil {
			t.Fatal("requireServerMode in embedded mode should return an error")
		}
		if got := err.Error(); got == "" ||
			!strings.Contains(got, "reset") || !strings.Contains(got, "embedded mode") {
			t.Errorf("error message %q should name the command and embedded mode", got)
		}
	})
}
