package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
)

// watermarkFakeStore models a shared multi-writer hub: HEAD advances from
// commitA to commitB the moment BackupDatabase runs, simulating another writer
// committing in the window between the sync completing and any post-read.
//
// It embeds a nil storage.DoltStorage so it satisfies the full interface
// (GetCurrentCommit is part of VersionControl) while only overriding the two
// methods runBackupExport calls; it also implements storage.BackupStore so the
// UnwrapStore(store).(storage.BackupStore) assertion in runBackupExport
// succeeds.
type watermarkFakeStore struct {
	storage.DoltStorage // nil — any un-overridden method would panic (none are called)

	commitA        string // HEAD before the backup
	commitB        string // HEAD after the backup (a racing writer's commit)
	backupRan      bool   // flips true once BackupDatabase is invoked
	headSeenByBS   string // the HEAD value visible at the moment BackupDatabase ran
	getCommitCalls int
}

func (f *watermarkFakeStore) GetCurrentCommit(_ context.Context) (string, error) {
	f.getCommitCalls++
	if f.backupRan {
		return f.commitB, nil
	}
	return f.commitA, nil
}

func (f *watermarkFakeStore) BackupDatabase(_ context.Context, _ string) error {
	// The backup captures data as of the CURRENT HEAD (commitA). Record it, then
	// simulate a racing writer landing commitB immediately after the sync.
	f.headSeenByBS = f.commitA
	f.backupRan = true
	return nil
}

// The remaining BackupStore methods are unused by runBackupExport but required
// for the interface assertion to hold.
func (f *watermarkFakeStore) BackupAdd(_ context.Context, _, _ string) error       { return nil }
func (f *watermarkFakeStore) BackupSync(_ context.Context, _ string) error         { return nil }
func (f *watermarkFakeStore) BackupRemove(_ context.Context, _ string) error       { return nil }
func (f *watermarkFakeStore) RestoreDatabase(_ context.Context, _ string, _ bool) error {
	return nil
}

// TestRunBackupExport_WatermarkPreBackup_91ffk guards beads-91ffk: the backup
// watermark must record the commit the backup actually captured (the pre-backup
// HEAD), NOT a re-read of HEAD after BackupDatabase returns. On a shared hub a
// commit landing in that window would otherwise be recorded as backed-up while
// being absent from the backup, so the next run's change-detection silently
// SKIPs it.
//
// MUTATION-VERIFY: move the GetCurrentCommit read back to AFTER BackupDatabase
// (state.LastDoltCommit = post-backup HEAD) and this test FAILS — the watermark
// becomes commitB (the racing commit the backup does not contain).
func TestRunBackupExport_WatermarkPreBackup_91ffk(t *testing.T) {
	// backupDir() resolves backup.git-repo/backup when that points at a real git
	// repo — a hermetic path that needs no beads workspace.
	gitRepo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gitRepo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	config.Set("backup.git-repo", gitRepo)
	t.Cleanup(func() { config.Set("backup.git-repo", "") })

	fake := &watermarkFakeStore{
		commitA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		commitB: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	prevStore := store
	store = fake
	t.Cleanup(func() { store = prevStore })

	// force=true skips the read-only change-detection pre-check so the only
	// GetCurrentCommit call is the watermark capture under test.
	state, err := runBackupExport(context.Background(), true)
	if err != nil {
		t.Fatalf("runBackupExport: %v", err)
	}

	if !fake.backupRan {
		t.Fatalf("BackupDatabase was never called")
	}
	// Sanity: the backup captured HEAD == commitA.
	if fake.headSeenByBS != fake.commitA {
		t.Fatalf("backup captured HEAD %q, want commitA %q", fake.headSeenByBS, fake.commitA)
	}
	// The watermark must be the pre-backup HEAD (commitA), not the racing commitB.
	if state.LastDoltCommit != fake.commitA {
		t.Errorf("watermark overshoot (beads-91ffk): LastDoltCommit = %q, want pre-backup HEAD %q (a commit that landed during the sync must NOT be recorded as backed-up)",
			state.LastDoltCommit, fake.commitA)
	}
	if state.LastDoltCommit == fake.commitB {
		t.Errorf("watermark recorded the racing commit %q that the backup does not contain — next run will silently SKIP it", fake.commitB)
	}
}
