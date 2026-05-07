//go:build !cgo

package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/db/util"
	"github.com/steveyegge/beads/internal/storage/dolt"
)

// usesSQLServer returns true in non-CGO builds since embedded Dolt requires
// CGO — the only options are the externally-managed dolt sql-server
// (ServerMode) and the per-workspace proxied dolt sql-server (ProxiedServer),
// both of which are SQL-server-shaped.
func usesSQLServer() bool {
	return true
}

// usesProxiedServer reports whether the current session is using the
// per-workspace proxied dolt sql-server (dolt_mode=proxied-server).
func usesProxiedServer() bool {
	if shouldUseGlobals() {
		return proxiedServerMode
	}
	return cmdCtx != nil && cmdCtx.ProxiedServer
}

// newDoltStore creates a SQL-server-backed storage backend. Embedded Dolt is
// not available without CGO; the two server-shaped backends both work fine.
//   - cfg.ProxiedServer: per-workspace proxied dolt sql-server.
//   - cfg.ServerMode: externally-managed dolt sql-server.
func newDoltStore(ctx context.Context, cfg *dolt.Config) (storage.DoltStorage, error) {
	if cfg.ProxiedServer {
		return newProxiedServerStore(ctx, cfg)
	}
	if !cfg.ServerMode {
		return nil, fmt.Errorf("%s", nocgoEmbeddedErrMsg)
	}
	return dolt.New(ctx, cfg)
}

// acquireEmbeddedLock returns a no-op lock in non-CGO builds.
func acquireEmbeddedLock(_ string, _ bool) (util.Unlocker, error) {
	return util.NoopLock{}, nil
}

// newDoltStoreFromConfig creates a SQL-server-backed storage backend from config.
func newDoltStoreFromConfig(ctx context.Context, beadsDir string) (storage.DoltStorage, error) {
	cfg, err := configfile.Load(beadsDir)
	if err == nil && cfg != nil && cfg.IsDoltProxiedServerMode() {
		return newProxiedServerStore(ctx, &dolt.Config{
			BeadsDir:      beadsDir,
			Database:      cfg.GetDoltDatabase(),
			ProxiedServer: true,
		})
	}
	if err == nil && cfg != nil && cfg.IsDoltServerMode() {
		return dolt.NewFromConfig(ctx, beadsDir)
	}
	return nil, fmt.Errorf("%s", nocgoEmbeddedErrMsg)
}

// newReadOnlyStoreFromConfig creates a read-only SQL-server-backed storage backend.
func newReadOnlyStoreFromConfig(ctx context.Context, beadsDir string) (storage.DoltStorage, error) {
	cfg, err := configfile.Load(beadsDir)
	if err == nil && cfg != nil && cfg.IsDoltProxiedServerMode() {
		return newProxiedServerStore(ctx, &dolt.Config{
			BeadsDir:      beadsDir,
			Database:      cfg.GetDoltDatabase(),
			ProxiedServer: true,
			ReadOnly:      true,
		})
	}
	if err == nil && cfg != nil && cfg.IsDoltServerMode() {
		return dolt.NewFromConfigWithOptions(ctx, beadsDir, &dolt.Config{ReadOnly: true})
	}
	return nil, fmt.Errorf("%s", nocgoEmbeddedErrMsg)
}

// nocgoEmbeddedErrMsg guides the user either to a SQL-server-shaped backend
// (no rebuild needed) or to an embedded-capable install path. It intentionally
// enumerates the canonical install paths so users don't have to hunt through
// docs.
const nocgoEmbeddedErrMsg = `embedded Dolt requires a CGO build, but this bd binary was built with CGO_ENABLED=0.

Three options:

  1. Use the proxied dolt sql-server (no external server, no reinstall):
       bd init --proxied-server
     bd spawns a per-workspace proxy + child dolt sql-server under
     .beads/proxieddb/ and manages their lifecycle for you.

  2. Use external server mode (no reinstall needed):
       bd init --server
     Requires a running 'dolt sql-server'. See docs/DOLT.md.

  3. Reinstall with embedded-mode support:
       brew install beads                              # macOS / Linux
       npm install -g @beads/bd                        # any platform with Node
       curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash

See docs/INSTALLING.md for the full comparison.`
