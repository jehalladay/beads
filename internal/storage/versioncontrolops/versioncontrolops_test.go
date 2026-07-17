package versioncontrolops

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// versioncontrolops functions accept a DBConn (ExecContext/QueryContext/
// QueryRowContext), which *sql.DB satisfies directly — so sqlmock drives them
// hermetically with no Dolt server. These tests raise coverage of the
// branch/backup/commit/status/log/flatten/remote wrappers from ~5% to full,
// asserting both the happy path (exact SQL + args) and the error-wrapping path
// (a failed CALL surfaces an actionable, context-tagged error) for each op.
//
// Every test uses sqlmock's ordered expectations (the default) and asserts
// ExpectationsWereMet, so an unexpected or missing query fails loudly rather
// than passing vacuously.

func newMock(t *testing.T) (DBConn, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func assertMet(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

// --- branches.go ---------------------------------------------------------

func TestListBranches(t *testing.T) {
	db, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"name"}).AddRow("feature").AddRow("main")
	mock.ExpectQuery("SELECT name FROM dolt_branches ORDER BY name").WillReturnRows(rows)

	got, err := ListBranches(context.Background(), db)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(got) != 2 || got[0] != "feature" || got[1] != "main" {
		t.Errorf("ListBranches = %v, want [feature main]", got)
	}
	assertMet(t, mock)
}

func TestListBranches_QueryError(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT name FROM dolt_branches").WillReturnError(errors.New("boom"))

	_, err := ListBranches(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "list branches") {
		t.Errorf("expected wrapped 'list branches' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestListBranches_ScanError(t *testing.T) {
	db, mock := newMock(t)
	// A NULL name into a non-nullable string forces a scan failure.
	rows := sqlmock.NewRows([]string{"name"}).AddRow(nil)
	mock.ExpectQuery("SELECT name FROM dolt_branches").WillReturnRows(rows)

	_, err := ListBranches(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "scan branch") {
		t.Errorf("expected 'scan branch' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestCurrentBranch(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT active_branch()").
		WillReturnRows(sqlmock.NewRows([]string{"branch"}).AddRow("main"))

	got, err := CurrentBranch(context.Background(), db)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if got != "main" {
		t.Errorf("CurrentBranch = %q, want main", got)
	}
	assertMet(t, mock)
}

func TestCurrentBranch_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT active_branch()").WillReturnError(errors.New("no session"))

	_, err := CurrentBranch(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "get current branch") {
		t.Errorf("expected 'get current branch' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestCreateBranch(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BRANCH").WithArgs("feature").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := CreateBranch(context.Background(), db, "feature"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	assertMet(t, mock)
}

func TestCreateBranch_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BRANCH").WithArgs("feature").
		WillReturnError(errors.New("exists"))

	err := CreateBranch(context.Background(), db, "feature")
	if err == nil || !strings.Contains(err.Error(), "create branch feature") {
		t.Errorf("expected 'create branch feature' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestDeleteBranch(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BRANCH").WithArgs("feature").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := DeleteBranch(context.Background(), db, "feature"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	assertMet(t, mock)
}

func TestDeleteBranch_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BRANCH").WithArgs("feature").
		WillReturnError(errors.New("checked out"))

	err := DeleteBranch(context.Background(), db, "feature")
	if err == nil || !strings.Contains(err.Error(), "delete branch feature") {
		t.Errorf("expected 'delete branch feature' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestCheckoutBranch(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_CHECKOUT").WithArgs("feature").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := CheckoutBranch(context.Background(), db, "feature"); err != nil {
		t.Fatalf("CheckoutBranch: %v", err)
	}
	assertMet(t, mock)
}

func TestCheckoutBranch_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_CHECKOUT").WithArgs("nope").
		WillReturnError(errors.New("unknown branch"))

	err := CheckoutBranch(context.Background(), db, "nope")
	if err == nil || !strings.Contains(err.Error(), "checkout branch nope") {
		t.Errorf("expected 'checkout branch nope' error, got %v", err)
	}
	assertMet(t, mock)
}

// --- gc.go ---------------------------------------------------------------

func TestDoltGC(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_GC").WillReturnResult(sqlmock.NewResult(0, 0))

	if err := DoltGC(context.Background(), db); err != nil {
		t.Fatalf("DoltGC: %v", err)
	}
	assertMet(t, mock)
}

func TestDoltGC_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_GC").WillReturnError(errors.New("in tx"))

	err := DoltGC(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "dolt gc") {
		t.Errorf("expected 'dolt gc' error, got %v", err)
	}
	assertMet(t, mock)
}

// --- clone.go ------------------------------------------------------------

func TestDoltClone(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_CLONE").WithArgs("https://host/repo", "localdb").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := DoltClone(context.Background(), db, "https://host/repo", "localdb"); err != nil {
		t.Fatalf("DoltClone: %v", err)
	}
	assertMet(t, mock)
}

func TestDoltClone_ErrorRedactsCredentials(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_CLONE").
		WithArgs("https://user:secret@host/repo", "localdb").
		WillReturnError(errors.New("auth failed"))

	err := DoltClone(context.Background(), db, "https://user:secret@host/repo", "localdb")
	if err == nil {
		t.Fatal("expected error")
	}
	// The wrapped error must not leak the credentials embedded in the URL.
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "user:") {
		t.Errorf("DoltClone error leaks credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "dolt clone") {
		t.Errorf("expected 'dolt clone' prefix, got %v", err)
	}
	assertMet(t, mock)
}

// --- commit.go -----------------------------------------------------------

func TestDirtyTableTracker(t *testing.T) {
	var tr DirtyTableTracker
	// Nil-map read before any write must not panic.
	if got := tr.DirtyTables(); got != nil {
		t.Errorf("empty tracker DirtyTables = %v, want nil", got)
	}
	tr.MarkDirty("issues")
	tr.MarkDirty("issues") // idempotent
	tr.MarkDirty("labels")
	// Dolt-ignored tables are skipped.
	tr.MarkDirty("wisps")
	tr.MarkDirty("wisp_history")

	got := tr.DirtyTables()
	if len(got) != 2 || !got["issues"] || !got["labels"] {
		t.Errorf("DirtyTables = %v, want {issues, labels}", got)
	}
	if got["wisps"] || got["wisp_history"] {
		t.Errorf("wisp tables must be skipped, got %v", got)
	}
}

func TestStageAndCommit(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_ADD").WithArgs("issues").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_COMMIT").WithArgs("msg", "me <me@x>").
		WillReturnResult(sqlmock.NewResult(0, 0))

	dirty := map[string]bool{"issues": true}
	if err := StageAndCommit(context.Background(), db, dirty, "msg", "me <me@x>"); err != nil {
		t.Fatalf("StageAndCommit: %v", err)
	}
	assertMet(t, mock)
}

func TestStageAndCommit_NoOpWhenEmpty(t *testing.T) {
	db, mock := newMock(t)
	// No SQL should run for either empty message or empty dirty set.
	if err := StageAndCommit(context.Background(), db, map[string]bool{"issues": true}, "", "me"); err != nil {
		t.Fatalf("empty msg: %v", err)
	}
	if err := StageAndCommit(context.Background(), db, nil, "msg", "me"); err != nil {
		t.Fatalf("empty dirty: %v", err)
	}
	assertMet(t, mock)
}

func TestStageAndCommit_AddError(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_ADD").WithArgs("issues").
		WillReturnError(errors.New("cannot add"))

	err := StageAndCommit(context.Background(), db, map[string]bool{"issues": true}, "msg", "me")
	if err == nil || !strings.Contains(err.Error(), "dolt add issues") {
		t.Errorf("expected 'dolt add issues' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestStageAndCommit_NothingToCommitIsBenign(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_ADD").WithArgs("issues").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_COMMIT").
		WillReturnError(errors.New("nothing to commit"))

	// A "nothing to commit" error must be swallowed (e.g. all writes were to
	// dolt-ignored tables) — StageAndCommit returns nil.
	if err := StageAndCommit(context.Background(), db, map[string]bool{"issues": true}, "msg", "me"); err != nil {
		t.Errorf("nothing-to-commit should be benign, got %v", err)
	}
	assertMet(t, mock)
}

func TestStageAndCommit_CommitError(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_ADD").WithArgs("issues").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_COMMIT").
		WillReturnError(errors.New("disk full"))

	err := StageAndCommit(context.Background(), db, map[string]bool{"issues": true}, "msg", "me")
	if err == nil || !strings.Contains(err.Error(), "dolt commit") {
		t.Errorf("expected 'dolt commit' error, got %v", err)
	}
	assertMet(t, mock)
}

// --- backup.go -----------------------------------------------------------

func TestBackupAdd(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BACKUP").WithArgs("b1", "file:///tmp/b").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := BackupAdd(context.Background(), db, "b1", "file:///tmp/b"); err != nil {
		t.Fatalf("BackupAdd: %v", err)
	}
	assertMet(t, mock)
}

func TestBackupAdd_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BACKUP").WithArgs("b1", "url").
		WillReturnError(errors.New("conflict"))

	err := BackupAdd(context.Background(), db, "b1", "url")
	if err == nil || !strings.Contains(err.Error(), "add backup b1") {
		t.Errorf("expected 'add backup b1' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestBackupSync(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BACKUP").WithArgs("b1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := BackupSync(context.Background(), db, "b1"); err != nil {
		t.Fatalf("BackupSync: %v", err)
	}
	assertMet(t, mock)
}

func TestBackupSync_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BACKUP").WithArgs("b1").
		WillReturnError(errors.New("unreachable"))

	err := BackupSync(context.Background(), db, "b1")
	if err == nil || !strings.Contains(err.Error(), "sync backup b1") {
		t.Errorf("expected 'sync backup b1' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestBackupRemove(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BACKUP").WithArgs("b1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := BackupRemove(context.Background(), db, "b1"); err != nil {
		t.Fatalf("BackupRemove: %v", err)
	}
	assertMet(t, mock)
}

func TestBackupRemove_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BACKUP").WithArgs("b1").
		WillReturnError(errors.New("no such backup"))

	err := BackupRemove(context.Background(), db, "b1")
	if err == nil || !strings.Contains(err.Error(), "remove backup b1") {
		t.Errorf("expected 'remove backup b1' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestBackupRestore_NoForce(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BACKUP").WithArgs("url", "db").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := BackupRestore(context.Background(), db, "url", "db", false); err != nil {
		t.Fatalf("BackupRestore: %v", err)
	}
	assertMet(t, mock)
}

func TestBackupRestore_Force(t *testing.T) {
	db, mock := newMock(t)
	// The force path threads a literal '--force' into the CALL before the ? args.
	mock.ExpectExec("CALL DOLT_BACKUP.*--force").WithArgs("url", "db").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := BackupRestore(context.Background(), db, "url", "db", true); err != nil {
		t.Fatalf("BackupRestore force: %v", err)
	}
	assertMet(t, mock)
}

func TestBackupRestore_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BACKUP").WithArgs("url", "db").
		WillReturnError(errors.New("bad backup"))

	err := BackupRestore(context.Background(), db, "url", "db", false)
	if err == nil || !strings.Contains(err.Error(), "restore from backup url") {
		t.Errorf("expected 'restore from backup url' error, got %v", err)
	}
	assertMet(t, mock)
}

// Note: ExtractAddressConflictName, DirToFileURL, and sanitizeURL already have
// dedicated coverage in backup_test.go / backup_dirurl_test.go / clone_test.go.

// --- remotes.go ----------------------------------------------------------

func TestListRemotes(t *testing.T) {
	db, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"name", "url"}).
		AddRow("origin", "file:///a").
		AddRow("beta", "dolthub://org/repo")
	mock.ExpectQuery("SELECT name, url FROM dolt_remotes").WillReturnRows(rows)

	got, err := ListRemotes(context.Background(), db)
	if err != nil {
		t.Fatalf("ListRemotes: %v", err)
	}
	if len(got) != 2 || got[0].Name != "origin" || got[1].URL != "dolthub://org/repo" {
		t.Errorf("ListRemotes = %+v", got)
	}
	assertMet(t, mock)
}

func TestListRemotes_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT name, url FROM dolt_remotes").
		WillReturnError(errors.New("boom"))

	_, err := ListRemotes(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "list remotes") {
		t.Errorf("expected 'list remotes' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestRemoveRemote(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_REMOTE").WithArgs("origin").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := RemoveRemote(context.Background(), db, "origin"); err != nil {
		t.Fatalf("RemoveRemote: %v", err)
	}
	assertMet(t, mock)
}

func TestRemoveRemote_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_REMOTE").WithArgs("origin").
		WillReturnError(errors.New("no remote"))

	err := RemoveRemote(context.Background(), db, "origin")
	if err == nil || !strings.Contains(err.Error(), "remove remote origin") {
		t.Errorf("expected 'remove remote origin' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestFetch_NoUser(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_FETCH").WithArgs("origin").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := Fetch(context.Background(), db, "origin", ""); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	assertMet(t, mock)
}

func TestFetch_WithUser(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_FETCH.*--user").WithArgs("root", "origin").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := Fetch(context.Background(), db, "origin", "root"); err != nil {
		t.Fatalf("Fetch with user: %v", err)
	}
	assertMet(t, mock)
}

func TestFetch_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_FETCH").WithArgs("origin").
		WillReturnError(errors.New("unreachable"))

	err := Fetch(context.Background(), db, "origin", "")
	if err == nil || !strings.Contains(err.Error(), "fetch from origin") {
		t.Errorf("expected 'fetch from origin' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestPush_NoUser(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_PUSH").WithArgs("origin", "main").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := Push(context.Background(), db, "origin", "main", ""); err != nil {
		t.Fatalf("Push: %v", err)
	}
	assertMet(t, mock)
}

func TestPush_WithUser(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_PUSH.*--user").WithArgs("root", "origin", "main").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := Push(context.Background(), db, "origin", "main", "root"); err != nil {
		t.Fatalf("Push with user: %v", err)
	}
	assertMet(t, mock)
}

func TestPush_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_PUSH").WithArgs("origin", "main").
		WillReturnError(errors.New("rejected"))

	err := Push(context.Background(), db, "origin", "main", "")
	if err == nil || !strings.Contains(err.Error(), "push to origin/main") {
		t.Errorf("expected 'push to origin/main' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestForcePush_NoUser(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_PUSH.*--force").WithArgs("origin", "main").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := ForcePush(context.Background(), db, "origin", "main", ""); err != nil {
		t.Fatalf("ForcePush: %v", err)
	}
	assertMet(t, mock)
}

func TestForcePush_WithUser(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_PUSH.*--force.*--user").WithArgs("root", "origin", "main").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := ForcePush(context.Background(), db, "origin", "main", "root"); err != nil {
		t.Fatalf("ForcePush with user: %v", err)
	}
	assertMet(t, mock)
}

func TestForcePush_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_PUSH.*--force").WithArgs("origin", "main").
		WillReturnError(errors.New("rejected"))

	err := ForcePush(context.Background(), db, "origin", "main", "")
	if err == nil || !strings.Contains(err.Error(), "force push to origin/main") {
		t.Errorf("expected 'force push to origin/main' error, got %v", err)
	}
	assertMet(t, mock)
}

// --- version_control.go --------------------------------------------------

func TestStatus(t *testing.T) {
	db, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"table_name", "staged", "status"}).
		AddRow("issues", true, "modified").
		AddRow("labels", false, "new")
	mock.ExpectQuery("SELECT table_name, staged, status FROM dolt_status").WillReturnRows(rows)

	st, err := Status(context.Background(), db)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.Staged) != 1 || st.Staged[0].Table != "issues" || st.Staged[0].Status != "modified" {
		t.Errorf("Staged = %+v", st.Staged)
	}
	if len(st.Unstaged) != 1 || st.Unstaged[0].Table != "labels" {
		t.Errorf("Unstaged = %+v", st.Unstaged)
	}
	assertMet(t, mock)
}

func TestStatus_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT table_name, staged, status FROM dolt_status").
		WillReturnError(errors.New("boom"))

	_, err := Status(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "get status") {
		t.Errorf("expected 'get status' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestLog_WithLimit(t *testing.T) {
	db, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"commit_hash", "committer", "email", "date", "message"}).
		AddRow("abc123", "me", "me@x", time.Now(), "first")
	mock.ExpectQuery("SELECT commit_hash, committer, email, date, message FROM dolt_log ORDER BY date DESC LIMIT ?").
		WithArgs(5).WillReturnRows(rows)

	got, err := Log(context.Background(), db, 5)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(got) != 1 || got[0].Hash != "abc123" || got[0].Author != "me" {
		t.Errorf("Log = %+v", got)
	}
	assertMet(t, mock)
}

func TestLog_NoLimit(t *testing.T) {
	db, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"commit_hash", "committer", "email", "date", "message"})
	// No LIMIT clause when limit <= 0.
	mock.ExpectQuery("SELECT commit_hash, committer, email, date, message FROM dolt_log ORDER BY date DESC$").
		WillReturnRows(rows)

	got, err := Log(context.Background(), db, 0)
	if err != nil {
		t.Fatalf("Log no limit: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Log = %+v, want empty", got)
	}
	assertMet(t, mock)
}

func TestLog_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("FROM dolt_log").WithArgs(3).WillReturnError(errors.New("boom"))

	_, err := Log(context.Background(), db, 3)
	if err == nil || !strings.Contains(err.Error(), "get log") {
		t.Errorf("expected 'get log' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestCommitExists_EmptyAndInvalid(t *testing.T) {
	db, _ := newMock(t)
	// Empty hash returns (false, nil) with no query.
	if ok, err := CommitExists(context.Background(), db, ""); ok || err != nil {
		t.Errorf("empty hash = (%v, %v), want (false, nil)", ok, err)
	}
	// A ref that fails ValidateRef returns (false, nil) with no query.
	if ok, err := CommitExists(context.Background(), db, "bad ref!!"); ok || err != nil {
		t.Errorf("invalid ref = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestCommitExists_Found(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT COUNT.*FROM dolt_log").
		WithArgs("abc123", "abc123%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	ok, err := CommitExists(context.Background(), db, "abc123")
	if err != nil || !ok {
		t.Errorf("CommitExists = (%v, %v), want (true, nil)", ok, err)
	}
	assertMet(t, mock)
}

func TestCommitExists_QueryError(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT COUNT.*FROM dolt_log").
		WithArgs("abc123", "abc123%").
		WillReturnError(errors.New("boom"))

	_, err := CommitExists(context.Background(), db, "abc123")
	if err == nil || !strings.Contains(err.Error(), "check commit existence") {
		t.Errorf("expected 'check commit existence' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestMerge_Clean(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_MERGE").WithArgs("me <me@x>", "feature").
		WillReturnResult(sqlmock.NewResult(0, 0))

	conflicts, err := Merge(context.Background(), db, "feature", "me <me@x>")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if conflicts != nil {
		t.Errorf("expected no conflicts, got %+v", conflicts)
	}
	assertMet(t, mock)
}

func TestMerge_ReturnsConflicts(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_MERGE").WithArgs("me", "feature").
		WillReturnError(errors.New("merge has conflicts"))
	// On merge error, Merge queries dolt_conflicts; a non-empty result means
	// conflicts are returned with a nil error.
	mock.ExpectQuery("SELECT .table., num_conflicts FROM dolt_conflicts").
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}).AddRow("issues", 3))

	conflicts, err := Merge(context.Background(), db, "feature", "me")
	if err != nil {
		t.Fatalf("Merge with conflicts should return nil err, got %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].Field != "issues" {
		t.Errorf("conflicts = %+v, want one on 'issues'", conflicts)
	}
	assertMet(t, mock)
}

func TestMerge_ErrorNoConflicts(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_MERGE").WithArgs("me", "feature").
		WillReturnError(errors.New("some other failure"))
	// dolt_conflicts is empty → the original merge error is surfaced.
	mock.ExpectQuery("FROM dolt_conflicts").
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))

	_, err := Merge(context.Background(), db, "feature", "me")
	if err == nil || !strings.Contains(err.Error(), "merge branch feature") {
		t.Errorf("expected 'merge branch feature' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestGetConflicts(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("FROM dolt_conflicts").
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}).
			AddRow("issues", 2).AddRow("labels", 1))

	got, err := GetConflicts(context.Background(), db)
	if err != nil {
		t.Fatalf("GetConflicts: %v", err)
	}
	if len(got) != 2 || got[0].Field != "issues" || got[1].Field != "labels" {
		t.Errorf("GetConflicts = %+v", got)
	}
	assertMet(t, mock)
}

func TestGetConflicts_Error(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("FROM dolt_conflicts").WillReturnError(errors.New("boom"))

	_, err := GetConflicts(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "get conflicts") {
		t.Errorf("expected 'get conflicts' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestResolveConflicts(t *testing.T) {
	for _, strat := range []string{"ours", "theirs"} {
		t.Run(strat, func(t *testing.T) {
			db, mock := newMock(t)
			mock.ExpectExec("CALL DOLT_CONFLICTS_RESOLVE").
				WillReturnResult(sqlmock.NewResult(0, 0))

			if err := ResolveConflicts(context.Background(), db, "issues", strat); err != nil {
				t.Fatalf("ResolveConflicts(%s): %v", strat, err)
			}
			assertMet(t, mock)
		})
	}
}

func TestResolveConflicts_InvalidTable(t *testing.T) {
	db, _ := newMock(t)
	err := ResolveConflicts(context.Background(), db, "bad table!", "ours")
	if err == nil || !strings.Contains(err.Error(), "invalid table name") {
		t.Errorf("expected 'invalid table name' error, got %v", err)
	}
}

func TestResolveConflicts_UnknownStrategy(t *testing.T) {
	db, _ := newMock(t)
	err := ResolveConflicts(context.Background(), db, "issues", "mine")
	if err == nil || !strings.Contains(err.Error(), "unknown conflict resolution strategy") {
		t.Errorf("expected unknown-strategy error, got %v", err)
	}
}

func TestResolveConflicts_ExecError(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_CONFLICTS_RESOLVE").WillReturnError(errors.New("boom"))

	err := ResolveConflicts(context.Background(), db, "issues", "ours")
	if err == nil || !strings.Contains(err.Error(), "resolve conflicts") {
		t.Errorf("expected 'resolve conflicts' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestValidateTableName(t *testing.T) {
	tests := []struct {
		name    string
		table   string
		wantErr string
	}{
		{"valid", "issues", ""},
		{"valid with underscore", "issue_labels", ""},
		{"empty", "", "cannot be empty"},
		{"too long", strings.Repeat("a", 65), "too long"},
		{"leading digit", "1table", "invalid table name"},
		{"space", "bad table", "invalid table name"},
		{"semicolon injection", "issues; DROP", "invalid table name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTableName(tt.table)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validateTableName(%q) = %v, want nil", tt.table, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validateTableName(%q) = %v, want contains %q", tt.table, err, tt.wantErr)
			}
		})
	}
}

// --- flatten.go ----------------------------------------------------------

func TestFlatten_AlreadyFlat(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT commit_hash FROM dolt_log ORDER BY date ASC LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"commit_hash"}).AddRow("root"))
	mock.ExpectQuery("SELECT COUNT.*FROM dolt_log").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))

	// commitCount <= 1 → no-op, no flatten steps run.
	if err := Flatten(context.Background(), db); err != nil {
		t.Fatalf("Flatten already-flat: %v", err)
	}
	assertMet(t, mock)
}

func TestFlatten_FullSequence(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT commit_hash FROM dolt_log ORDER BY date ASC LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"commit_hash"}).AddRow("root"))
	mock.ExpectQuery("SELECT COUNT.*FROM dolt_log").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(5))
	// The 7 flatten steps in order.
	mock.ExpectExec("CALL DOLT_BRANCH.*flatten-tmp").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_CHECKOUT.*flatten-tmp").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_RESET.*--soft").WithArgs("root").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_COMMIT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_CHECKOUT.*main").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_RESET.*--hard").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_BRANCH.*-D").WillReturnResult(sqlmock.NewResult(0, 0))

	if err := Flatten(context.Background(), db); err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	assertMet(t, mock)
}

func TestFlatten_StepError(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT commit_hash FROM dolt_log ORDER BY date ASC LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"commit_hash"}).AddRow("root"))
	mock.ExpectQuery("SELECT COUNT.*FROM dolt_log").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(3))
	mock.ExpectExec("CALL DOLT_BRANCH.*flatten-tmp").
		WillReturnError(errors.New("branch exists"))

	err := Flatten(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "flatten step") {
		t.Errorf("expected 'flatten step' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestFlatten_InitialHashError(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT commit_hash FROM dolt_log ORDER BY date ASC LIMIT 1").
		WillReturnError(errors.New("empty log"))

	err := Flatten(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "find initial commit") {
		t.Errorf("expected 'find initial commit' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestFlattenDryRun(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT COUNT.*FROM dolt_log").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(7))
	mock.ExpectQuery("SELECT commit_hash FROM dolt_log ORDER BY date ASC LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"commit_hash"}).AddRow("root"))

	count, hash, err := FlattenDryRun(context.Background(), db)
	if err != nil {
		t.Fatalf("FlattenDryRun: %v", err)
	}
	if count != 7 || hash != "root" {
		t.Errorf("FlattenDryRun = (%d, %q), want (7, root)", count, hash)
	}
	assertMet(t, mock)
}

func TestFlattenDryRun_CountError(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("SELECT COUNT.*FROM dolt_log").WillReturnError(errors.New("boom"))

	_, _, err := FlattenDryRun(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "count commits") {
		t.Errorf("expected 'count commits' error, got %v", err)
	}
	assertMet(t, mock)
}

// --- compact.go ----------------------------------------------------------

func TestCompact_FullSequence(t *testing.T) {
	db, mock := newMock(t)
	// The full recipe: create temp branch at boundary, checkout, soft-reset to
	// initial, commit the squashed base, cherry-pick each recent commit, then
	// swing main onto the compacted branch and delete the temp branch.
	mock.ExpectExec("CALL DOLT_BRANCH").WithArgs("boundaryhash").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_CHECKOUT").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_RESET").WithArgs("initialhash").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_COMMIT").WithArgs("compact: squash 3 commits into base snapshot").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_CHERRY_PICK").WithArgs("recent1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_CHERRY_PICK").WithArgs("recent2").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_CHECKOUT").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_RESET").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_BRANCH").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := Compact(context.Background(), db, "initialhash", "boundaryhash", 3, []string{"recent1", "recent2"})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	assertMet(t, mock)
}

func TestCompact_CreateBranchErrorNoCleanup(t *testing.T) {
	db, mock := newMock(t)
	// The very first step fails before the temp branch exists, so the deferred
	// best-effort cleanup must NOT fire (branchCreated stays false).
	mock.ExpectExec("CALL DOLT_BRANCH").WithArgs("boundaryhash").
		WillReturnError(errors.New("boom"))

	err := Compact(context.Background(), db, "initialhash", "boundaryhash", 2, nil)
	if err == nil || !strings.Contains(err.Error(), "create temp branch") {
		t.Errorf("expected 'create temp branch' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestCompact_StepErrorRunsCleanup(t *testing.T) {
	db, mock := newMock(t)
	// A failure after the temp branch is created triggers the deferred cleanup:
	// checkout main + delete the temp branch (both best-effort, ignored errors).
	mock.ExpectExec("CALL DOLT_BRANCH").WithArgs("boundaryhash").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_CHECKOUT").
		WillReturnError(errors.New("checkout failed"))
	// Deferred cleanup.
	mock.ExpectExec("CALL DOLT_CHECKOUT").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_BRANCH").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := Compact(context.Background(), db, "initialhash", "boundaryhash", 1, nil)
	if err == nil || !strings.Contains(err.Error(), "checkout temp") {
		t.Errorf("expected 'checkout temp' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestCompact_CherryPickError(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_BRANCH").WithArgs("boundaryhash").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_CHECKOUT").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_RESET").WithArgs("initialhash").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_COMMIT").WithArgs("compact: squash 5 commits into base snapshot").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_CHERRY_PICK").WithArgs("deadbeefcafe").
		WillReturnError(errors.New("conflict"))
	// Deferred cleanup after the cherry-pick failure.
	mock.ExpectExec("CALL DOLT_CHECKOUT").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_BRANCH").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := Compact(context.Background(), db, "initialhash", "boundaryhash", 5, []string{"deadbeefcafe"})
	// The step label truncates the hash to 8 chars (min(8, len)).
	if err == nil || !strings.Contains(err.Error(), "cherry-pick deadbeef") {
		t.Errorf("expected 'cherry-pick deadbeef' error, got %v", err)
	}
	assertMet(t, mock)
}

// --- remotes.go Pull + mergesettle.go clean path ------------------------

// expectCleanSettle queues the sqlmock expectations for a conflict-free,
// violation-free MergeAndSettle: the pre-merge cleanliness probe, the two
// session flags, the merge itself, then the empty conflict/violation scans
// that let SettleMerge return nil without resolving or aborting anything.
func expectCleanSettle(mock sqlmock.Sqlmock, ref string) {
	// workingSetClean: an empty dolt_status means the pre-merge tree is clean.
	mock.ExpectQuery("SELECT 1 FROM dolt_status LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"?"}))
	mock.ExpectExec("SET @@dolt_allow_commit_conflicts").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET @@dolt_force_transaction_commit").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_MERGE").WithArgs(ref).
		WillReturnResult(sqlmock.NewResult(0, 0))
	// TryAutoResolveMergeConflicts: no conflicts → (false, nil).
	mock.ExpectQuery("FROM dolt_conflicts").
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
	// TryRepairFKCascadeViolations → constraintViolationTables: none.
	mock.ExpectQuery("FROM dolt_constraint_violations WHERE num_violations").
		WillReturnRows(sqlmock.NewRows([]string{"table"}))
}

func TestPull_NoUser_Clean(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_FETCH").WithArgs("origin", "main").
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectCleanSettle(mock, "origin/main")

	if err := Pull(context.Background(), db, "origin", "main", ""); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	assertMet(t, mock)
}

func TestPull_WithUser_Clean(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_FETCH.*--user").WithArgs("root", "origin", "main").
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectCleanSettle(mock, "origin/main")

	if err := Pull(context.Background(), db, "origin", "main", "root"); err != nil {
		t.Fatalf("Pull with user: %v", err)
	}
	assertMet(t, mock)
}

func TestPull_FetchError(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_FETCH").WithArgs("origin", "main").
		WillReturnError(errors.New("auth failed"))

	err := Pull(context.Background(), db, "origin", "main", "")
	if err == nil || !strings.Contains(err.Error(), "fetch from origin/main") {
		t.Errorf("expected 'fetch from origin/main' error, got %v", err)
	}
	assertMet(t, mock)
}

func TestPull_MergeAlreadyUpToDate(t *testing.T) {
	db, mock := newMock(t)
	// "up to date" from DOLT_MERGE is swallowed, so a clean settle still succeeds.
	mock.ExpectExec("CALL DOLT_FETCH").WithArgs("origin", "main").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT 1 FROM dolt_status LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"?"}))
	mock.ExpectExec("SET @@dolt_allow_commit_conflicts").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET @@dolt_force_transaction_commit").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_MERGE").WithArgs("origin/main").
		WillReturnError(errors.New("branch is already up to date"))
	mock.ExpectQuery("FROM dolt_conflicts").
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
	mock.ExpectQuery("FROM dolt_constraint_violations WHERE num_violations").
		WillReturnRows(sqlmock.NewRows([]string{"table"}))

	if err := Pull(context.Background(), db, "origin", "main", ""); err != nil {
		t.Fatalf("Pull up-to-date should succeed, got %v", err)
	}
	assertMet(t, mock)
}

func TestPull_UnresolvableConflictAbortsAndReports(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectExec("CALL DOLT_FETCH").WithArgs("origin", "main").
		WillReturnResult(sqlmock.NewResult(0, 0))
	// Pre-merge clean, flags, merge.
	mock.ExpectQuery("SELECT 1 FROM dolt_status LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"?"}))
	mock.ExpectExec("SET @@dolt_allow_commit_conflicts").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET @@dolt_force_transaction_commit").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CALL DOLT_MERGE").WithArgs("origin/main").
		WillReturnResult(sqlmock.NewResult(0, 0))
	// TryAutoResolveMergeConflicts sees a conflict on a non-allowlisted table
	// ("issues") → returns (false, nil), nothing resolved.
	mock.ExpectQuery("FROM dolt_conflicts").
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}).AddRow("issues", 2))
	// resolved==false → SettleMerge re-queries conflicts via GetConflicts.
	mock.ExpectQuery("FROM dolt_conflicts").
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}).AddRow("issues", 2))
	// Operator-required conflicts → abortMerge runs DOLT_MERGE('--abort').
	mock.ExpectExec("CALL DOLT_MERGE").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := Pull(context.Background(), db, "origin", "main", "")
	if err == nil || !strings.Contains(err.Error(), "require operator resolution") {
		t.Errorf("expected operator-resolution conflict error, got %v", err)
	}
	// The wrapped MergeConflictsError names the conflicting table.
	var mce *MergeConflictsError
	if !errors.As(err, &mce) {
		t.Fatalf("expected *MergeConflictsError, got %T: %v", err, err)
	}
	if len(mce.Conflicts) != 1 || mce.Conflicts[0].Field != "issues" {
		t.Errorf("conflicts = %+v, want one on 'issues'", mce.Conflicts)
	}
	assertMet(t, mock)
}
