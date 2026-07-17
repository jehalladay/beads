package proxy_test

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/dbproxy/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the pure / hermetically-reachable surface of the proxy
// package (no fork+exec of a child, no live Dolt). The spawn/handoff and
// shutdown paths are exercised elsewhere by server_test.go against a fake
// server; this file targets Backend, Stats, PickFreePort, and the early
// validation branches of GetCreateDatabaseProxyServerEndpoint.

func TestBackend_String(t *testing.T) {
	assert.Equal(t, "external", proxy.BackendExternal.String())
	assert.Equal(t, "local-server", proxy.BackendLocalServer.String())
	assert.Equal(t, "local-shared-server", proxy.BackendLocalSharedServer.String())
	assert.Equal(t, "whatever", proxy.Backend("whatever").String())
}

func TestBackend_Valid(t *testing.T) {
	for _, b := range []proxy.Backend{
		proxy.BackendExternal, proxy.BackendLocalServer, proxy.BackendLocalSharedServer,
	} {
		assert.Truef(t, b.Valid(), "%q should be valid", b)
	}
	for _, b := range []proxy.Backend{"", "bogus", "External", "local"} {
		assert.Falsef(t, b.Valid(), "%q should be invalid", b)
	}
}

func TestBackend_Validate(t *testing.T) {
	t.Run("empty is a distinct error", func(t *testing.T) {
		err := proxy.Backend("").Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be set")
	})

	t.Run("unknown lists the supported set", func(t *testing.T) {
		err := proxy.Backend("bogus").Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown backend")
		assert.Contains(t, err.Error(), "bogus")
		// The error must enumerate every known backend name.
		for _, name := range proxy.KnownBackendNames() {
			assert.Contains(t, err.Error(), name)
		}
	})

	t.Run("recognized backends validate cleanly", func(t *testing.T) {
		assert.NoError(t, proxy.BackendExternal.Validate())
		assert.NoError(t, proxy.BackendLocalServer.Validate())
		assert.NoError(t, proxy.BackendLocalSharedServer.Validate())
	})
}

func TestKnownBackendNames(t *testing.T) {
	got := proxy.KnownBackendNames()
	assert.Equal(t, []string{"external", "local-server", "local-shared-server"}, got)

	// Must be a fresh slice each call: mutating the result must not corrupt
	// a subsequent call.
	got[0] = "mutated"
	again := proxy.KnownBackendNames()
	assert.Equal(t, "external", again[0], "KnownBackendNames must return a defensive copy")
}

func TestStats_NilReceiverSafe(t *testing.T) {
	var s *proxy.Stats // nil
	// Snapshot on a nil receiver returns the zero value, not a panic.
	assert.Equal(t, proxy.Counters{}, s.Snapshot())
	// Every mutator must be a no-op (not a panic) on a nil receiver.
	assert.NotPanics(t, func() {
		s.IncListenAndServe()
		s.IncBackendStart()
		s.IncBackendStop()
		s.IncIdleTimeout()
		s.IncSignalReceived()
		s.IncAccept()
		s.IncAcceptError()
		s.IncBackendDialAttempt()
		s.IncBackendDialSuccess()
		s.IncBackendDialError()
		s.IncHandledConn()
		s.AddBytesClientToBackend(10)
		s.AddBytesBackendToClient(20)
	})
}

func TestStats_Counters(t *testing.T) {
	s := &proxy.Stats{}

	s.IncListenAndServe()
	s.IncBackendStart()
	s.IncBackendStart()
	s.IncBackendStop()
	s.IncIdleTimeout()
	s.IncSignalReceived()
	s.IncAccept()
	s.IncAcceptError()
	s.IncBackendDialAttempt()
	s.IncBackendDialSuccess()
	s.IncBackendDialError()
	s.IncHandledConn()
	s.AddBytesClientToBackend(100)
	s.AddBytesClientToBackend(23)
	s.AddBytesBackendToClient(7)

	got := s.Snapshot()
	want := proxy.Counters{
		ListenAndServeCalls:  1,
		BackendStartCalls:    2,
		BackendStopCalls:     1,
		IdleTimeouts:         1,
		SignalsReceived:      1,
		AcceptCalls:          1,
		AcceptErrors:         1,
		BackendDialAttempts:  1,
		BackendDialSuccess:   1,
		BackendDialErrors:    1,
		HandledConns:         1,
		BytesClientToBackend: 123,
		BytesBackendToClient: 7,
	}
	assert.Equal(t, want, got)
}

func TestPickFreePort(t *testing.T) {
	port, err := proxy.PickFreePort()
	require.NoError(t, err)
	assert.Greater(t, port, 0)
	assert.LessOrEqual(t, port, 65535)

	// Two consecutive picks should each succeed (the port is released after
	// probing, so they may or may not differ — just assert both are valid).
	port2, err := proxy.PickFreePort()
	require.NoError(t, err)
	assert.Greater(t, port2, 0)
}

func TestGetCreateDatabaseProxyServerEndpoint_ValidationErrors(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		opts    proxy.OpenOpts
		wantSub string
	}{
		{
			name:    "empty backend",
			opts:    proxy.OpenOpts{},
			wantSub: "must be set",
		},
		{
			name:    "unknown backend",
			opts:    proxy.OpenOpts{Backend: "bogus"},
			wantSub: "unknown backend",
		},
		{
			name:    "local-server missing config path",
			opts:    proxy.OpenOpts{Backend: proxy.BackendLocalServer},
			wantSub: "ConfigFilePath is required",
		},
		{
			name: "local-server missing log path",
			opts: proxy.OpenOpts{
				Backend:        proxy.BackendLocalServer,
				ConfigFilePath: "/tmp/cfg.yaml",
			},
			wantSub: "LogFilePath is required",
		},
		{
			name: "local-server missing dolt bin path",
			opts: proxy.OpenOpts{
				Backend:        proxy.BackendLocalServer,
				ConfigFilePath: "/tmp/cfg.yaml",
				LogFilePath:    "/tmp/log.txt",
			},
			wantSub: "DoltBinPath is required",
		},
		{
			name:    "external missing log path",
			opts:    proxy.OpenOpts{Backend: proxy.BackendExternal},
			wantSub: "LogFilePath is required",
		},
		{
			name: "external with invalid config",
			opts: proxy.OpenOpts{
				Backend:     proxy.BackendExternal,
				LogFilePath: "/tmp/log.txt",
				External:    configfile.ExternalDoltConfig{}, // neither socket nor host/port
			},
			wantSub: "OpenOpts.External",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep, err := proxy.GetCreateDatabaseProxyServerEndpoint(dir, tt.opts)
			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), strings.ToLower(tt.wantSub))
			assert.Equal(t, proxy.Endpoint{}, ep)
		})
	}
}
