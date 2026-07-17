package tracker

import (
	"context"
	"strings"
	"testing"
)

// TestSync_WarnsWhenConflictPrefIneffectiveOnOneDirectionalSync is the
// beads-dn2p regression: a conflict-resolution preference (ConflictLocal /
// ConflictExternal, set by --prefer-local / --prefer-<provider>) only takes
// effect during a bidirectional sync, because DetectConflicts/resolveConflicts
// run only under (Pull && Push). On a push-only or pull-only sync the
// preference is silently ignored. The engine now warns so the user knows their
// flag had no effect. Fixing it centrally in Sync covers all providers at once.
func TestSync_WarnsWhenConflictPrefIneffectiveOnOneDirectionalSync(t *testing.T) {
	ctx := context.Background()

	newEngine := func(cr ConflictResolution) (*Engine, SyncOptions) {
		store := newPureTestStore()
		tr := newMockTracker("test")
		e := NewEngine(tr, store, "actor")
		return e, SyncOptions{ConflictResolution: cr}
	}

	t.Run("push-only + prefer-local warns", func(t *testing.T) {
		e, opts := newEngine(ConflictLocal)
		opts.Push = true // one-directional (pull stays false)
		res, err := e.Sync(ctx, opts)
		if err != nil {
			t.Fatalf("Sync error: %v", err)
		}
		if !warningsMention(res.Warnings, "prefer") && !warningsMention(res.Warnings, "conflict") {
			t.Fatalf("expected a warning that the conflict preference has no effect on a one-directional sync, got %v", res.Warnings)
		}
	})

	t.Run("pull-only + prefer-external warns", func(t *testing.T) {
		e, opts := newEngine(ConflictExternal)
		opts.Pull = true // one-directional (push stays false)
		res, err := e.Sync(ctx, opts)
		if err != nil {
			t.Fatalf("Sync error: %v", err)
		}
		if !warningsMention(res.Warnings, "prefer") && !warningsMention(res.Warnings, "conflict") {
			t.Fatalf("expected a warning on pull-only + prefer, got %v", res.Warnings)
		}
	})

	t.Run("bidirectional + prefer does NOT warn (preference is effective)", func(t *testing.T) {
		e, opts := newEngine(ConflictLocal)
		opts.Pull = true
		opts.Push = true
		res, err := e.Sync(ctx, opts)
		if err != nil {
			t.Fatalf("Sync error: %v", err)
		}
		if warningsMention(res.Warnings, "no effect") {
			t.Errorf("bidirectional sync must NOT warn about an ineffective preference, got %v", res.Warnings)
		}
	})

	t.Run("push-only WITHOUT a prefer flag (timestamp default) does NOT warn", func(t *testing.T) {
		e, opts := newEngine(ConflictTimestamp)
		opts.Push = true
		res, err := e.Sync(ctx, opts)
		if err != nil {
			t.Fatalf("Sync error: %v", err)
		}
		if warningsMention(res.Warnings, "no effect") {
			t.Errorf("default (timestamp) resolution must not warn, got %v", res.Warnings)
		}
	})
}

func warningsMention(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(strings.ToLower(w), strings.ToLower(substr)) {
			return true
		}
	}
	return false
}
