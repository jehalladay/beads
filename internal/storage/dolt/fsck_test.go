package dolt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPrePushFSCK_EmptyCLIDir verifies that prePushFSCK is a no-op when
// CLIDir is empty (no local noms store configured).
func TestPrePushFSCK_EmptyCLIDir(t *testing.T) {
	t.Parallel()
	s := &DoltStore{dbPath: "", database: "test"}
	if err := s.prePushFSCK(context.Background()); err != nil {
		t.Fatalf("expected nil for empty CLIDir, got %v", err)
	}
}

// TestPrePushFSCK_NoNomsDir verifies that prePushFSCK is a no-op when
// CLIDir exists but .dolt/noms does not (uninitialized or non-dolt directory).
func TestPrePushFSCK_NoNomsDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	s := &DoltStore{dbPath: tmp, database: "mydb"}
	// CLIDir() = tmp/mydb, which doesn't exist and has no .dolt/noms
	if err := s.prePushFSCK(context.Background()); err != nil {
		t.Fatalf("expected nil when .dolt/noms absent, got %v", err)
	}
}

// TestPrePushFSCK_CleanDB verifies that prePushFSCK passes on a fresh
// dolt-initialized database with no corruption.
func TestPrePushFSCK_CleanDB(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt not in PATH")
	}

	tmp := t.TempDir()
	dbDir := filepath.Join(tmp, "mydb")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	initCmd := exec.Command("dolt", "init", "--name", "test", "--email", "test@example.com")
	initCmd.Dir = dbDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("dolt init: %v\n%s", err, out)
	}

	s := &DoltStore{dbPath: tmp, database: "mydb"}
	if err := s.prePushFSCK(context.Background()); err != nil {
		t.Fatalf("expected nil on clean DB, got %v", err)
	}
}

// TestPrePushFSCK_UnopenableDB verifies that prePushFSCK logs a warning and
// proceeds (returns nil) when dolt fsck cannot open the database. This avoids
// misleading users with a corruption warning for environmental / tooling
// failures. Example: dolthub/dolt#10915 (Windows url.Parse bug pre-v1.86.4)
// caused fsck to fail-to-open healthy databases, which the previous wrapper
// reported as "dangling chunk reference: aborting push to prevent propagating
// corrupt chunks".
//
// We simulate the unopenable state by creating a .dolt/noms directory without
// running dolt init — fsck prints "Could not open dolt database" and exits
// non-zero.
func TestPrePushFSCK_UnopenableDB(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt not in PATH")
	}

	tmp := t.TempDir()
	dbDir := filepath.Join(tmp, "mydb")
	// Create .dolt/noms so the skip check passes, but don't init the repo.
	if err := os.MkdirAll(filepath.Join(dbDir, ".dolt", "noms"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	s := &DoltStore{dbPath: tmp, database: "mydb"}
	if err := s.prePushFSCK(context.Background()); err != nil {
		t.Fatalf("expected nil when fsck cannot open db (should warn and proceed), got %v", err)
	}
}

// TestFsckCouldNotOpen verifies the helper identifies both known dolt
// "couldn't open" phrasings and does not classify actual integrity failures
// (or unrelated output) as open-failures.
func TestFsckCouldNotOpen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "windows url.Parse bug pre-1.86.4 (dolthub/dolt#10915)",
			output: `Could not open dolt database: CreateFile \C:\Users\x\.beads\...\.dolt\noms: The filename, directory name, or volume label syntax is incorrect.`,
			want:   true,
		},
		{
			name:   "uninitialized .dolt directory",
			output: "The current directories repository state is invalid\nopen .dolt/repo_state.json: no such file or directory",
			want:   true,
		},
		{
			// beads-9nq1: the exact output the refinery gate captured under load
			// was ONLY the repo_state.json cause line (the "repository state is
			// invalid" banner was dropped/interleaved), which previously slipped
			// through fsckCouldNotOpen and got wrapped as a corruption abort.
			name:   "repo_state.json cause line alone (banner dropped under load)",
			output: "open /tmp/x/001/mydb/.dolt/repo_state.json: no such file or directory",
			want:   true,
		},
		{
			name:   "repo_state.json windows cannot-find variant",
			output: `open C:\Users\x\.dolt\repo_state.json: The system cannot find the file specified.`,
			want:   true,
		},
		{
			name:   "actual dangling chunk reference (must still abort)",
			output: "dangling chunk reference: hash abc123 referenced but not present",
			want:   false,
		},
		{
			// A genuine integrity finding that happens to mention a missing file
			// but NOT repo_state.json must still abort (guard is repo_state.json-
			// scoped, not a blanket "no such file" match).
			name:   "missing chunk file that is not repo_state.json (must still abort)",
			output: "dangling chunk reference: open /x/.dolt/noms/abc.chunk: no such file or directory",
			want:   false,
		},
		{
			name:   "empty output",
			output: "",
			want:   false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := fsckCouldNotOpen(tc.output); got != tc.want {
				t.Errorf("fsckCouldNotOpen(%q) = %v, want %v", tc.output, got, tc.want)
			}
		})
	}
}

// TestPrePushFSCK_TimeoutProceeds is the teeth for the beads-9nq1 timeout leg:
// when the fsck subprocess exceeds the fsck context deadline (the flake mode
// under heavy /fsx node contention — dolt is killed mid-run with empty/partial
// output), prePushFSCK must treat it as "could not run" and return nil (warn +
// proceed), NOT wrap it as ErrDanglingReference and abort the push (which
// bounce-loops the merge queue on a spurious RED). We force the deadline by
// passing an already-expired context, so the exec.CommandContext child is
// killed immediately and fsckCtx.Err() == DeadlineExceeded regardless of how
// fast/slow dolt is — fully hermetic (no real slow dolt needed), but it does
// require a dolt binary on PATH to reach the exec.
func TestPrePushFSCK_TimeoutProceeds(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt not in PATH")
	}

	tmp := t.TempDir()
	dbDir := filepath.Join(tmp, "mydb")
	// .dolt/noms present so prePushFSCK does not short-circuit before the exec.
	if err := os.MkdirAll(filepath.Join(dbDir, ".dolt", "noms"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Already-expired parent context → the derived fsck context is born past its
	// deadline, so CommandContext kills the child at once and the error is a
	// deadline, exercised deterministically.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	s := &DoltStore{dbPath: tmp, database: "mydb"}
	if err := s.prePushFSCK(ctx); err != nil {
		t.Fatalf("expected nil on fsck timeout (should warn and proceed), got %v", err)
	}
}
