package uow

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage/domain/db"
)

// fakeRunner is a no-op db.Runner: the use-case accessors only wire it into
// SQL repositories, and no query runs until a use-case method executes, so the
// methods here are never actually called by these tests.
type fakeRunner struct{}

func (fakeRunner) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("fakeRunner: not implemented")
}
func (fakeRunner) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("fakeRunner: not implemented")
}
func (fakeRunner) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

// fakeTx records the lifecycle calls the baseUOW makes on it.
type fakeTx struct {
	commitMsg     string
	commitErr     error
	commits       int
	rollbackUnles int
}

func (t *fakeTx) Runner() db.Runner { return fakeRunner{} }
func (t *fakeTx) Commit(_ context.Context, message string) error {
	t.commits++
	t.commitMsg = message
	return t.commitErr
}
func (t *fakeTx) Rollback(context.Context) error { return nil }
func (t *fakeTx) RollbackUnlessCommitted(context.Context) {
	t.rollbackUnles++
}

// fakeTxProvider hands out a preconfigured Tx or a BeginTx error.
type fakeTxProvider struct {
	tx       Tx
	beginErr error
}

func (p *fakeTxProvider) BeginTx(context.Context) (Tx, error) {
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	return p.tx, nil
}

func TestNewUOW_BeginTxError(t *testing.T) {
	p := &fakeTxProvider{beginErr: errors.New("boom")}
	if _, err := NewUOW(context.Background(), p); err == nil {
		t.Fatal("NewUOW err = nil, want BeginTx error")
	}
}

func TestNewUOW_Success(t *testing.T) {
	tx := &fakeTx{}
	u, err := NewUOW(context.Background(), &fakeTxProvider{tx: tx})
	if err != nil {
		t.Fatalf("NewUOW: %v", err)
	}
	if u == nil {
		t.Fatal("NewUOW returned nil UnitOfWork")
	}
}

func TestBaseUOW_CommitDelegates(t *testing.T) {
	tx := &fakeTx{}
	u, _ := NewUOW(context.Background(), &fakeTxProvider{tx: tx})
	if err := u.Commit(context.Background(), "landing"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if tx.commits != 1 || tx.commitMsg != "landing" {
		t.Errorf("tx.Commit calls=%d msg=%q, want 1 / %q", tx.commits, tx.commitMsg, "landing")
	}
}

func TestBaseUOW_CommitPropagatesError(t *testing.T) {
	tx := &fakeTx{commitErr: errors.New("commit failed")}
	u, _ := NewUOW(context.Background(), &fakeTxProvider{tx: tx})
	if err := u.Commit(context.Background(), "x"); err == nil {
		t.Fatal("Commit err = nil, want propagated tx error")
	}
}

func TestBaseUOW_CloseRollsBack(t *testing.T) {
	tx := &fakeTx{}
	u, _ := NewUOW(context.Background(), &fakeTxProvider{tx: tx})
	u.Close(context.Background())
	if tx.rollbackUnles != 1 {
		t.Errorf("RollbackUnlessCommitted calls=%d, want 1", tx.rollbackUnles)
	}
}

// TestBaseUOW_UseCaseAccessorsAreLazyAndMemoized exercises every accessor and
// asserts each returns a non-nil use-case and memoizes it (second call returns
// the same instance, covering both the construct and cache-hit branches).
func TestBaseUOW_UseCaseAccessorsAreLazyAndMemoized(t *testing.T) {
	u, _ := NewUOW(context.Background(), &fakeTxProvider{tx: &fakeTx{}})
	base := u.(*baseUOW)

	if a, b := base.ConfigUseCase(), base.ConfigUseCase(); a == nil || a != b {
		t.Error("ConfigUseCase not memoized")
	}
	if a, b := base.DoltRemoteUseCase(), base.DoltRemoteUseCase(); a == nil || a != b {
		t.Error("DoltRemoteUseCase not memoized")
	}
	if a, b := base.BootstrapUseCase(), base.BootstrapUseCase(); a == nil || a != b {
		t.Error("BootstrapUseCase not memoized")
	}
	if a, b := base.IssueUseCase(), base.IssueUseCase(); a == nil || a != b {
		t.Error("IssueUseCase not memoized")
	}
	if a, b := base.DependencyUseCase(), base.DependencyUseCase(); a == nil || a != b {
		t.Error("DependencyUseCase not memoized")
	}
	if a, b := base.LabelUseCase(), base.LabelUseCase(); a == nil || a != b {
		t.Error("LabelUseCase not memoized")
	}
	if a, b := base.CommentUseCase(), base.CommentUseCase(); a == nil || a != b {
		t.Error("CommentUseCase not memoized")
	}
}
