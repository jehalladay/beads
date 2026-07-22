package main

import (
	"context"
)

// beads-21vns: `bd kv` (set/get/clear/list) and the memory subsystem
// (remember/memories/forget/recall) are thin wrappers over the config store —
// each is just store.{Set,Get,GetAll,Delete}Config on a prefixed key. But
// unlike `bd config` (which routes through usesProxiedServer() →
// runConfig*ProxiedServer), kv/memory only had the direct-store path guarded by
// ensureDirectMode(). In proxied-server mode ensureDirectMode fails hard
// (newDoltStoreFromConfig returns "proxy server store should be uow provider"),
// so the ENTIRE hub-connected fleet could not use `bd kv` or `bd remember` —
// the persistent-knowledge subsystem CLAUDE.md mandates. beads-5fu1 only made
// that failure emit a clean --json {error}; this makes it actually work.
//
// Rather than duplicate every subcommand's validation + JSON/text formatting
// (the config_proxied_server.go idiom), these accessors abstract only the
// store interaction: in proxied mode they route the same ConfigUseCase op the
// direct store exposes through a UOW; otherwise they use the global store. The
// callers build the storageKey (kvPrefix+key for kv, kvPrefix+memoryPrefix+key
// for memory) — the sole per-subsystem difference — so all downstream behavior
// (found:false, existence pre-checks, verb selection, reserved-key guards,
// z0fe warnings) stays identical across both paths.

// kvSetConfig writes value at storageKey, routing through the proxied UOW when
// the crew is hub-connected. commitMsg is the Dolt commit message used only on
// the proxied path (the direct path auto-commits elsewhere / on close).
func kvSetConfig(ctx context.Context, storageKey, value, commitMsg string) error {
	if usesProxiedServer() {
		uw := openConfigProxiedUOW(ctx)
		defer uw.Close(ctx)
		if err := uw.ConfigUseCase().SetConfig(ctx, storageKey, value); err != nil {
			return err
		}
		if err := uw.Commit(ctx, commitMsg); err != nil && !isDoltNothingToCommit(err) {
			return err
		}
		return nil
	}
	return store.SetConfig(ctx, storageKey, value)
}

// kvGetConfig reads the value at storageKey (absent collapses to "", nil), via
// the proxied UOW when hub-connected.
func kvGetConfig(ctx context.Context, storageKey string) (string, error) {
	if usesProxiedServer() {
		uw := openConfigProxiedUOW(ctx)
		defer uw.Close(ctx)
		return uw.ConfigUseCase().GetConfig(ctx, storageKey)
	}
	return store.GetConfig(ctx, storageKey)
}

// kvGetAllConfig returns the full config map (used for prefix filtering and
// existence checks), via the proxied UOW when hub-connected.
func kvGetAllConfig(ctx context.Context) (map[string]string, error) {
	if usesProxiedServer() {
		uw := openConfigProxiedUOW(ctx)
		defer uw.Close(ctx)
		return uw.ConfigUseCase().GetAllConfig(ctx)
	}
	return store.GetAllConfig(ctx)
}

// kvDeleteConfig removes storageKey, routing through the proxied UOW when
// hub-connected. commitMsg is used only on the proxied path.
func kvDeleteConfig(ctx context.Context, storageKey, commitMsg string) error {
	if usesProxiedServer() {
		uw := openConfigProxiedUOW(ctx)
		defer uw.Close(ctx)
		if err := uw.ConfigUseCase().DeleteConfig(ctx, storageKey); err != nil {
			return err
		}
		if err := uw.Commit(ctx, commitMsg); err != nil && !isDoltNothingToCommit(err) {
			return err
		}
		return nil
	}
	return store.DeleteConfig(ctx, storageKey)
}
