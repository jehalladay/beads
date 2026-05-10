package db

import (
	"context"
	"fmt"
	"strings"
)

type DoltVersionControlRepository interface {
	Checkout(ctx context.Context, args ...string) error
	Branch(ctx context.Context, args ...string) error
	Add(ctx context.Context, args ...string) error
	Commit(ctx context.Context, args ...string) error
	Merge(ctx context.Context, args ...string) error
	Remote(ctx context.Context, args ...string) error
	Fetch(ctx context.Context, args ...string) error
	Push(ctx context.Context, args ...string) error
	Pull(ctx context.Context, args ...string) error
	Clone(ctx context.Context, args ...string) error
}

func NewDoltVersionControlRepository(runner Runner) DoltVersionControlRepository {
	return &doltVersionControlRepositoryImpl{runner: runner}
}

type doltVersionControlRepositoryImpl struct {
	runner Runner
}

var _ DoltVersionControlRepository = (*doltVersionControlRepositoryImpl)(nil)

func (i *doltVersionControlRepositoryImpl) Checkout(ctx context.Context, args ...string) error {
	return i.call(ctx, "DOLT_CHECKOUT", args...)
}

func (i *doltVersionControlRepositoryImpl) Branch(ctx context.Context, args ...string) error {
	return i.call(ctx, "DOLT_BRANCH", args...)
}

func (i *doltVersionControlRepositoryImpl) Add(ctx context.Context, args ...string) error {
	return i.call(ctx, "DOLT_ADD", args...)
}

func (i *doltVersionControlRepositoryImpl) Commit(ctx context.Context, args ...string) error {
	return i.call(ctx, "DOLT_COMMIT", args...)
}

func (i *doltVersionControlRepositoryImpl) Merge(ctx context.Context, args ...string) error {
	return i.call(ctx, "DOLT_MERGE", args...)
}

func (i *doltVersionControlRepositoryImpl) Remote(ctx context.Context, args ...string) error {
	return i.call(ctx, "DOLT_REMOTE", args...)
}

func (i *doltVersionControlRepositoryImpl) Fetch(ctx context.Context, args ...string) error {
	return i.call(ctx, "DOLT_FETCH", args...)
}

func (i *doltVersionControlRepositoryImpl) Push(ctx context.Context, args ...string) error {
	return i.call(ctx, "DOLT_PUSH", args...)
}

func (i *doltVersionControlRepositoryImpl) Pull(ctx context.Context, args ...string) error {
	return i.call(ctx, "DOLT_PULL", args...)
}

func (i *doltVersionControlRepositoryImpl) Clone(ctx context.Context, args ...string) error {
	return i.call(ctx, "DOLT_CLONE", args...)
}

func (i *doltVersionControlRepositoryImpl) call(ctx context.Context, proc string, args ...string) error {
	placeholders := make([]string, len(args))
	iargs := make([]any, len(args))
	for j, a := range args {
		placeholders[j] = "?"
		iargs[j] = a
	}
	query := "CALL " + proc + "(" + strings.Join(placeholders, ", ") + ")"
	if _, err := i.runner.ExecContext(ctx, query, iargs...); err != nil {
		return fmt.Errorf("db: %s: %w", proc, err)
	}
	return nil
}
