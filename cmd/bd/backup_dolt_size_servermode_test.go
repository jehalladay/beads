package main

import (
	"strings"
	"testing"
)

// TestDoltBackupSizeServerModeSentinel is the beads-dnuk regression: in
// server/proxied mode the local Dolt dir is a stub (real data is on the remote
// hub), so doltBackupSize must return the remote sentinel instead of walking
// the local dir and reporting a misleading "0 B". The display helpers render
// the sentinel as "unknown (remote server)".
func TestDoltBackupSizeServerModeSentinel(t *testing.T) {
	origServer, origProxied := serverMode, proxiedServerMode
	origTestMode, origCtx := testModeUseGlobals, cmdCtx
	t.Cleanup(func() {
		serverMode, proxiedServerMode = origServer, origProxied
		testModeUseGlobals, cmdCtx = origTestMode, origCtx
	})
	testModeUseGlobals = true
	cmdCtx = nil

	t.Run("server_mode_returns_sentinel", func(t *testing.T) {
		serverMode, proxiedServerMode = true, false
		size, err := doltBackupSize()
		if err != nil {
			t.Fatalf("doltBackupSize in server mode should not error, got %v", err)
		}
		if size != dbSizeRemoteSentinel {
			t.Fatalf("server mode: expected sentinel %d, got %d (a real walk of the stub dir → misleading 0 B)", dbSizeRemoteSentinel, size)
		}
	})

	t.Run("proxied_server_mode_returns_sentinel", func(t *testing.T) {
		serverMode, proxiedServerMode = false, true
		size, err := doltBackupSize()
		if err != nil {
			t.Fatalf("doltBackupSize in proxied mode should not error, got %v", err)
		}
		if size != dbSizeRemoteSentinel {
			t.Fatalf("proxied mode: expected sentinel %d, got %d", dbSizeRemoteSentinel, size)
		}
	})

	t.Run("json_size_renders_unknown_for_sentinel", func(t *testing.T) {
		serverMode, proxiedServerMode = true, false
		out := showDBSizeJSON()
		if out == nil {
			t.Fatal("showDBSizeJSON returned nil in server mode")
		}
		if out["bytes"] != nil {
			t.Errorf("server mode: expected null bytes, got %v", out["bytes"])
		}
		human, _ := out["human"].(string)
		if !strings.Contains(human, "remote server") {
			t.Errorf("server mode: expected human 'unknown (remote server)', got %q", human)
		}
	})
}
