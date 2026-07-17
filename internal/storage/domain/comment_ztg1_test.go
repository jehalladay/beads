package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// fakeCommentRepo is a hermetic CommentSQLRepository: no DB, canned returns
// keyed off the requested ids. lastOpts records the UseWispsTable flag so a
// test can assert the issue-vs-wisp routing of each use-case method.
type fakeCommentRepo struct {
	counts    map[string]int
	countsErr error
	lists     map[string][]*types.Comment
	listErr   error
	iter      storage.Iter[types.Comment]
	iterErr   error

	lastOpts    CommentOpts
	lastIDs     []string
	lastIterID  string
	countsCalls int
	listCalls   int
	iterCalls   int
}

func (f *fakeCommentRepo) CountsByIssueIDs(ctx context.Context, issueIDs []string, opts CommentOpts) (map[string]int, error) {
	f.countsCalls++
	f.lastOpts = opts
	f.lastIDs = issueIDs
	if f.countsErr != nil {
		return nil, f.countsErr
	}
	return f.counts, nil
}

func (f *fakeCommentRepo) ListByIssueIDs(ctx context.Context, issueIDs []string, opts CommentOpts) (map[string][]*types.Comment, error) {
	f.listCalls++
	f.lastOpts = opts
	f.lastIDs = issueIDs
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.lists, nil
}

func (f *fakeCommentRepo) IterByIssueID(ctx context.Context, issueID string, opts CommentOpts) (storage.Iter[types.Comment], error) {
	f.iterCalls++
	f.lastOpts = opts
	f.lastIterID = issueID
	if f.iterErr != nil {
		return nil, f.iterErr
	}
	return f.iter, nil
}

func TestCommentUseCase_Counts(t *testing.T) {
	ctx := context.Background()

	t.Run("empty ids short-circuit without hitting repo", func(t *testing.T) {
		repo := &fakeCommentRepo{}
		uc := NewCommentUseCase(repo)
		got, err := uc.GetCommentCounts(ctx, nil)
		if err != nil {
			t.Fatalf("GetCommentCounts: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("counts = %v, want empty", got)
		}
		if repo.countsCalls != 0 {
			t.Errorf("repo hit %d times on empty ids, want 0", repo.countsCalls)
		}
	})

	t.Run("issue counts route with UseWispsTable=false", func(t *testing.T) {
		repo := &fakeCommentRepo{counts: map[string]int{"bd-1": 3}}
		uc := NewCommentUseCase(repo)
		got, err := uc.GetCommentCounts(ctx, []string{"bd-1"})
		if err != nil {
			t.Fatalf("GetCommentCounts: %v", err)
		}
		if got["bd-1"] != 3 {
			t.Errorf("counts[bd-1] = %d, want 3", got["bd-1"])
		}
		if repo.lastOpts.UseWispsTable {
			t.Error("issue path set UseWispsTable=true, want false")
		}
	})

	t.Run("wisp counts route with UseWispsTable=true", func(t *testing.T) {
		repo := &fakeCommentRepo{counts: map[string]int{"w-1": 5}}
		uc := NewCommentUseCase(repo)
		got, err := uc.GetWispCommentCounts(ctx, []string{"w-1"})
		if err != nil {
			t.Fatalf("GetWispCommentCounts: %v", err)
		}
		if got["w-1"] != 5 {
			t.Errorf("counts[w-1] = %d, want 5", got["w-1"])
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp path set UseWispsTable=false, want true")
		}
	})

	t.Run("repo error is wrapped", func(t *testing.T) {
		sentinel := errors.New("boom")
		repo := &fakeCommentRepo{countsErr: sentinel}
		uc := NewCommentUseCase(repo)
		if _, err := uc.GetCommentCounts(ctx, []string{"bd-1"}); !errors.Is(err, sentinel) {
			t.Fatalf("err = %v, want wrapped sentinel", err)
		}
	})
}

func TestCommentUseCase_Lists(t *testing.T) {
	ctx := context.Background()
	c := &types.Comment{ID: "c1", IssueID: "bd-1", Text: "hi"}

	t.Run("GetCommentsForIssues empty short-circuits", func(t *testing.T) {
		repo := &fakeCommentRepo{}
		uc := NewCommentUseCase(repo)
		got, err := uc.GetCommentsForIssues(ctx, nil)
		if err != nil {
			t.Fatalf("GetCommentsForIssues: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
		if repo.listCalls != 0 {
			t.Errorf("repo hit %d times on empty ids, want 0", repo.listCalls)
		}
	})

	t.Run("GetCommentsForIssues returns full map (issue path)", func(t *testing.T) {
		repo := &fakeCommentRepo{lists: map[string][]*types.Comment{"bd-1": {c}}}
		uc := NewCommentUseCase(repo)
		got, err := uc.GetCommentsForIssues(ctx, []string{"bd-1"})
		if err != nil {
			t.Fatalf("GetCommentsForIssues: %v", err)
		}
		if len(got["bd-1"]) != 1 {
			t.Errorf("got %d comments, want 1", len(got["bd-1"]))
		}
		if repo.lastOpts.UseWispsTable {
			t.Error("issue path set UseWispsTable=true, want false")
		}
	})

	t.Run("GetCommentsForWisps routes wisp path", func(t *testing.T) {
		repo := &fakeCommentRepo{lists: map[string][]*types.Comment{"w-1": {c}}}
		uc := NewCommentUseCase(repo)
		if _, err := uc.GetCommentsForWisps(ctx, []string{"w-1"}); err != nil {
			t.Fatalf("GetCommentsForWisps: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp path set UseWispsTable=false, want true")
		}
	})

	t.Run("GetCommentsForWisps empty short-circuits", func(t *testing.T) {
		repo := &fakeCommentRepo{}
		uc := NewCommentUseCase(repo)
		got, err := uc.GetCommentsForWisps(ctx, nil)
		if err != nil {
			t.Fatalf("GetCommentsForWisps: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("list repo error wrapped", func(t *testing.T) {
		sentinel := errors.New("list-boom")
		repo := &fakeCommentRepo{listErr: sentinel}
		uc := NewCommentUseCase(repo)
		if _, err := uc.GetCommentsForIssues(ctx, []string{"bd-1"}); !errors.Is(err, sentinel) {
			t.Fatalf("err = %v, want wrapped sentinel", err)
		}
	})
}

func TestCommentUseCase_SingleID(t *testing.T) {
	ctx := context.Background()
	c := &types.Comment{ID: "c1", IssueID: "bd-1", Text: "hi"}

	t.Run("GetCommentsForIssue returns just that id's slice", func(t *testing.T) {
		repo := &fakeCommentRepo{lists: map[string][]*types.Comment{"bd-1": {c}, "bd-2": {c, c}}}
		uc := NewCommentUseCase(repo)
		got, err := uc.GetCommentsForIssue(ctx, "bd-1")
		if err != nil {
			t.Fatalf("GetCommentsForIssue: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("got %d, want 1 (only bd-1's slice)", len(got))
		}
		if repo.lastIDs[0] != "bd-1" {
			t.Errorf("queried ids %v, want [bd-1]", repo.lastIDs)
		}
	})

	t.Run("GetCommentsForWisp routes wisp path", func(t *testing.T) {
		repo := &fakeCommentRepo{lists: map[string][]*types.Comment{"w-1": {c}}}
		uc := NewCommentUseCase(repo)
		if _, err := uc.GetCommentsForWisp(ctx, "w-1"); err != nil {
			t.Fatalf("GetCommentsForWisp: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp path set UseWispsTable=false, want true")
		}
	})

	t.Run("empty id rejected before repo", func(t *testing.T) {
		repo := &fakeCommentRepo{}
		uc := NewCommentUseCase(repo)
		if _, err := uc.GetCommentsForIssue(ctx, ""); err == nil {
			t.Fatal("empty id accepted, want error")
		}
		if repo.listCalls != 0 {
			t.Errorf("repo hit on empty id (%d calls)", repo.listCalls)
		}
	})

	t.Run("CountCommentsForIssue casts to int64", func(t *testing.T) {
		repo := &fakeCommentRepo{counts: map[string]int{"bd-1": 7}}
		uc := NewCommentUseCase(repo)
		got, err := uc.CountCommentsForIssue(ctx, "bd-1")
		if err != nil {
			t.Fatalf("CountCommentsForIssue: %v", err)
		}
		if got != 7 {
			t.Errorf("count = %d, want 7", got)
		}
	})

	t.Run("CountCommentsForWisp routes wisp path", func(t *testing.T) {
		repo := &fakeCommentRepo{counts: map[string]int{"w-1": 2}}
		uc := NewCommentUseCase(repo)
		got, err := uc.CountCommentsForWisp(ctx, "w-1")
		if err != nil {
			t.Fatalf("CountCommentsForWisp: %v", err)
		}
		if got != 2 {
			t.Errorf("count = %d, want 2", got)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp path set UseWispsTable=false, want true")
		}
	})

	t.Run("CountCommentsForIssue empty id rejected", func(t *testing.T) {
		repo := &fakeCommentRepo{}
		uc := NewCommentUseCase(repo)
		if _, err := uc.CountCommentsForIssue(ctx, ""); err == nil {
			t.Fatal("empty id accepted, want error")
		}
	})

	t.Run("count repo error wrapped", func(t *testing.T) {
		sentinel := errors.New("count-boom")
		repo := &fakeCommentRepo{countsErr: sentinel}
		uc := NewCommentUseCase(repo)
		if _, err := uc.CountCommentsForIssue(ctx, "bd-1"); !errors.Is(err, sentinel) {
			t.Fatalf("err = %v, want wrapped sentinel", err)
		}
	})

	t.Run("listOne repo error wrapped", func(t *testing.T) {
		sentinel := errors.New("listone-boom")
		repo := &fakeCommentRepo{listErr: sentinel}
		uc := NewCommentUseCase(repo)
		if _, err := uc.GetCommentsForIssue(ctx, "bd-1"); !errors.Is(err, sentinel) {
			t.Fatalf("err = %v, want wrapped sentinel", err)
		}
	})
}

func TestCommentUseCase_Iter(t *testing.T) {
	ctx := context.Background()
	c := &types.Comment{ID: "c1", IssueID: "bd-1", Text: "hi"}

	t.Run("IterCommentsForIssue passes through the iterator (issue path)", func(t *testing.T) {
		repo := &fakeCommentRepo{iter: storage.NewSliceIter([]*types.Comment{c})}
		uc := NewCommentUseCase(repo)
		it, err := uc.IterCommentsForIssue(ctx, "bd-1")
		if err != nil {
			t.Fatalf("IterCommentsForIssue: %v", err)
		}
		got, err := storage.Collect(ctx, it)
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("collected %d, want 1", len(got))
		}
		if repo.lastOpts.UseWispsTable {
			t.Error("issue path set UseWispsTable=true, want false")
		}
		if repo.lastIterID != "bd-1" {
			t.Errorf("iter id = %q, want bd-1", repo.lastIterID)
		}
	})

	t.Run("IterCommentsForWisp routes wisp path", func(t *testing.T) {
		repo := &fakeCommentRepo{iter: storage.NewSliceIter([]*types.Comment{})}
		uc := NewCommentUseCase(repo)
		if _, err := uc.IterCommentsForWisp(ctx, "w-1"); err != nil {
			t.Fatalf("IterCommentsForWisp: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp path set UseWispsTable=false, want true")
		}
	})

	t.Run("empty id rejected before repo", func(t *testing.T) {
		repo := &fakeCommentRepo{}
		uc := NewCommentUseCase(repo)
		if _, err := uc.IterCommentsForIssue(ctx, ""); err == nil {
			t.Fatal("empty id accepted, want error")
		}
		if repo.iterCalls != 0 {
			t.Errorf("repo hit on empty id (%d calls)", repo.iterCalls)
		}
	})

	t.Run("iter repo error wrapped", func(t *testing.T) {
		sentinel := errors.New("iter-boom")
		repo := &fakeCommentRepo{iterErr: sentinel}
		uc := NewCommentUseCase(repo)
		if _, err := uc.IterCommentsForIssue(ctx, "bd-1"); !errors.Is(err, sentinel) {
			t.Fatalf("err = %v, want wrapped sentinel", err)
		}
	})
}
