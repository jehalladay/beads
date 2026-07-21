//go:build cgo

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// beads-idpba: `bd config set-many` in DIRECT mode previously looped
// store.SetConfig per key (config.go). DoltStore.SetConfig commits per call
// (its own withRetryTx), so a DB-level fault on the Nth key left keys 1..N-1
// durably written and N..end unwritten — a partially-applied config set (e.g.
// backup.url stored but backup.enabled not). The PROXIED path
// (runConfigSetManyProxiedServer) was already atomic: one UOW, single commit.
//
// The fix wraps the direct-mode db-key loop in ONE store.RunInTransaction so a
// mid-loop failure rolls the whole set-many back and no key is committed.
//
// These teeth drive configSetManyCmd.RunE through a store whose SetConfig
// faults on the SECOND key, and assert (a) the call errors and (b) NEITHER key
// was written (full rollback — not just the second).
//
// MUTATION-VERIFY: revert the RunE to the per-key store.SetConfig loop and this
// test FAILS — the first key commits before the second faults, leaving a
// partial config write.

var errInjectedSetConfigFailure = errors.New("injected set-config failure (idpba test)")

// faultSetConfigStore faults SetConfig on both the transactional path (the fix)
// and the non-transactional store path (the reverted pre-fix shape), after the
// first N successful writes, so the mutation-verify is honest.
type faultSetConfigStore struct {
	storage.DoltStorage
	failAfter int // allow this many SetConfig calls, then fault
	calls     *int
}

func (f *faultSetConfigStore) SetConfig(ctx context.Context, key, value string) error {
	*f.calls++
	if *f.calls > f.failAfter {
		return errInjectedSetConfigFailure
	}
	return f.DoltStorage.SetConfig(ctx, key, value)
}

func (f *faultSetConfigStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return f.DoltStorage.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		return fn(&faultSetConfigTx{Transaction: tx, parent: f})
	})
}

type faultSetConfigTx struct {
	storage.Transaction
	parent *faultSetConfigStore
}

func (t *faultSetConfigTx) SetConfig(ctx context.Context, key, value string) error {
	*t.parent.calls++
	if *t.parent.calls > t.parent.failAfter {
		return errInjectedSetConfigFailure
	}
	return t.Transaction.SetConfig(ctx, key, value)
}

func TestConfigSetManyIsAtomic_idpba(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// Two recognized, db-backed (non-yaml, non-protected) keys.
	const k1, k2 = "jira.url", "jira.project"

	calls := 0
	fault := &faultSetConfigStore{DoltStorage: real, failAfter: 1, calls: &calls}

	origStore := store
	origRootCtx := rootCtx
	origJSONOutput := jsonOutput
	origProxied := proxiedServerMode
	origActive := isStoreActive()
	origTestMode := testModeUseGlobals
	origCmdCtx := cmdCtx
	t.Cleanup(func() {
		store = origStore
		rootCtx = origRootCtx
		jsonOutput = origJSONOutput
		proxiedServerMode = origProxied
		setStoreActive(origActive)
		testModeUseGlobals = origTestMode
		cmdCtx = origCmdCtx
	})
	rootCtx = ctx
	jsonOutput = true
	proxiedServerMode = false
	// Force the globals path (usesProxiedServer/ensureDirectMode read the store
	// global) and install the fault store as the active store so RunE uses it
	// instead of opening a real DB.
	testModeUseGlobals = true
	cmdCtx = nil
	setStore(fault)
	setStoreActive(true)

	err := configSetManyCmd.RunE(configSetManyCmd, []string{k1 + "=https://example.atlassian.net", k2 + "=PROJ"})
	if err == nil {
		t.Fatalf("expected config set-many to error when the 2nd key faults; got nil")
	}

	// Full rollback: NEITHER key should be persisted.
	store = real
	if v, _ := real.GetConfig(ctx, k1); v != "" {
		t.Errorf("REGRESSION (idpba): %q = %q after a mid-loop failure, want empty — the first key committed before the second faulted (partial config write)", k1, v)
	}
	if v, _ := real.GetConfig(ctx, k2); v != "" {
		t.Errorf("REGRESSION (idpba): %q = %q after a failed set-many, want empty", k2, v)
	}
}
