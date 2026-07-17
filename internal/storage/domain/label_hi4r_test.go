package domain

import (
	"context"
	"errors"
	"sort"
	"testing"
)

// fakeLabelRepo is a hermetic LabelSQLRepository backed by an in-memory
// id->labels map. It records the LabelOpts of the last call so tests can assert
// issue-vs-wisp routing, and exposes error hooks per method to drive the
// wrapped-error branches.
type fakeLabelRepo struct {
	store map[string][]string // id -> labels (List/Insert/Delete operate here)

	insertErr error
	deleteErr error
	listErr   error
	bulkErr   error

	lastOpts    LabelOpts
	insertCalls int
	deleteCalls int
	listCalls   int
}

func newFakeLabelRepo() *fakeLabelRepo {
	return &fakeLabelRepo{store: map[string][]string{}}
}

func (f *fakeLabelRepo) Insert(ctx context.Context, issueID, label, actor string, opts LabelOpts) error {
	f.insertCalls++
	f.lastOpts = opts
	if f.insertErr != nil {
		return f.insertErr
	}
	for _, l := range f.store[issueID] {
		if l == label {
			return nil // idempotent
		}
	}
	f.store[issueID] = append(f.store[issueID], label)
	return nil
}

func (f *fakeLabelRepo) Delete(ctx context.Context, issueID, label, actor string, opts LabelOpts) error {
	f.deleteCalls++
	f.lastOpts = opts
	if f.deleteErr != nil {
		return f.deleteErr
	}
	cur := f.store[issueID]
	out := cur[:0:0]
	for _, l := range cur {
		if l != label {
			out = append(out, l)
		}
	}
	f.store[issueID] = out
	return nil
}

func (f *fakeLabelRepo) List(ctx context.Context, issueID string, opts LabelOpts) ([]string, error) {
	f.listCalls++
	f.lastOpts = opts
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.store[issueID], nil
}

func (f *fakeLabelRepo) ListByIssueIDs(ctx context.Context, issueIDs []string, opts LabelOpts) (map[string][]string, error) {
	f.lastOpts = opts
	if f.bulkErr != nil {
		return nil, f.bulkErr
	}
	out := map[string][]string{}
	for _, id := range issueIDs {
		out[id] = f.store[id]
	}
	return out, nil
}

func (f *fakeLabelRepo) DeleteAllForIDs(ctx context.Context, ids []string, opts LabelOpts) (int, error) {
	return 0, nil
}

func (f *fakeLabelRepo) CountAllForIDs(ctx context.Context, ids []string, opts LabelOpts) (int, error) {
	return 0, nil
}

func TestLabelUseCase_AddRemove(t *testing.T) {
	ctx := context.Background()

	t.Run("AddLabel inserts on the issue table", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		if err := uc.AddLabel(ctx, "bd-1", "urgent", "me"); err != nil {
			t.Fatalf("AddLabel: %v", err)
		}
		if repo.lastOpts.UseWispsTable {
			t.Error("issue path used wisp table")
		}
		if len(repo.store["bd-1"]) != 1 {
			t.Errorf("store = %v, want [urgent]", repo.store["bd-1"])
		}
	})

	t.Run("AddWispLabel routes wisp table", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		if err := uc.AddWispLabel(ctx, "w-1", "x", "me"); err != nil {
			t.Fatalf("AddWispLabel: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp path did not use wisp table")
		}
	})

	t.Run("AddLabel rejects empty id and empty label before repo", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		if err := uc.AddLabel(ctx, "", "x", "me"); err == nil {
			t.Error("empty id accepted")
		}
		if err := uc.AddLabel(ctx, "bd-1", "", "me"); err == nil {
			t.Error("empty label accepted")
		}
		if repo.insertCalls != 0 {
			t.Errorf("repo hit %d times on invalid input", repo.insertCalls)
		}
	})

	t.Run("AddLabel wraps repo error", func(t *testing.T) {
		sentinel := errors.New("insert-boom")
		repo := newFakeLabelRepo()
		repo.insertErr = sentinel
		uc := NewLabelUseCase(repo)
		if err := uc.AddLabel(ctx, "bd-1", "x", "me"); !errors.Is(err, sentinel) {
			t.Fatalf("err = %v, want wrapped sentinel", err)
		}
	})

	t.Run("RemoveLabel deletes; RemoveWispLabel routes wisp", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.store["bd-1"] = []string{"a", "b"}
		uc := NewLabelUseCase(repo)
		if err := uc.RemoveLabel(ctx, "bd-1", "a", "me"); err != nil {
			t.Fatalf("RemoveLabel: %v", err)
		}
		if len(repo.store["bd-1"]) != 1 || repo.store["bd-1"][0] != "b" {
			t.Errorf("store = %v, want [b]", repo.store["bd-1"])
		}
		if err := uc.RemoveWispLabel(ctx, "w-1", "z", "me"); err != nil {
			t.Fatalf("RemoveWispLabel: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp remove did not route wisp table")
		}
	})

	t.Run("RemoveLabel rejects empty id/label + wraps error", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		if err := uc.RemoveLabel(ctx, "", "x", "me"); err == nil {
			t.Error("empty id accepted")
		}
		if err := uc.RemoveLabel(ctx, "bd-1", "", "me"); err == nil {
			t.Error("empty label accepted")
		}
		sentinel := errors.New("del-boom")
		repo.deleteErr = sentinel
		if err := uc.RemoveLabel(ctx, "bd-1", "a", "me"); !errors.Is(err, sentinel) {
			t.Fatalf("err = %v, want wrapped sentinel", err)
		}
	})
}

func TestLabelUseCase_Many(t *testing.T) {
	ctx := context.Background()

	t.Run("AddLabels skips empty entries, wisp variant routes", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		if err := uc.AddLabels(ctx, "bd-1", []string{"a", "", "b"}, "me"); err != nil {
			t.Fatalf("AddLabels: %v", err)
		}
		if repo.insertCalls != 2 {
			t.Errorf("insertCalls = %d, want 2 (empty skipped)", repo.insertCalls)
		}
		if err := uc.AddWispLabels(ctx, "w-1", []string{"c"}, "me"); err != nil {
			t.Fatalf("AddWispLabels: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp addMany did not route wisp table")
		}
	})

	t.Run("AddLabels empty id rejected + wraps insert error", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		if err := uc.AddLabels(ctx, "", []string{"a"}, "me"); err == nil {
			t.Error("empty id accepted")
		}
		repo.insertErr = errors.New("boom")
		if err := uc.AddLabels(ctx, "bd-1", []string{"a"}, "me"); err == nil {
			t.Error("insert error not surfaced")
		}
	})

	t.Run("RemoveLabels skips empty, wisp variant routes, error wrapped", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.store["bd-1"] = []string{"a", "b"}
		uc := NewLabelUseCase(repo)
		if err := uc.RemoveLabels(ctx, "bd-1", []string{"a", ""}, "me"); err != nil {
			t.Fatalf("RemoveLabels: %v", err)
		}
		if repo.deleteCalls != 1 {
			t.Errorf("deleteCalls = %d, want 1", repo.deleteCalls)
		}
		if err := uc.RemoveWispLabels(ctx, "w-1", []string{"z"}, "me"); err != nil {
			t.Fatalf("RemoveWispLabels: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp removeMany did not route wisp table")
		}
		if err := uc.RemoveLabels(ctx, "", []string{"a"}, "me"); err == nil {
			t.Error("empty id accepted")
		}
		repo.deleteErr = errors.New("boom")
		if err := uc.RemoveLabels(ctx, "bd-1", []string{"b"}, "me"); err == nil {
			t.Error("delete error not surfaced")
		}
	})
}

func TestLabelUseCase_SetLabels(t *testing.T) {
	ctx := context.Background()

	t.Run("diff adds missing and removes extras", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.store["bd-1"] = []string{"keep", "drop"}
		uc := NewLabelUseCase(repo)
		// desired: keep + add. "drop" removed, "keep" untouched, "add" inserted.
		if err := uc.SetLabels(ctx, "bd-1", []string{"keep", "add", ""}, "me"); err != nil {
			t.Fatalf("SetLabels: %v", err)
		}
		got := append([]string(nil), repo.store["bd-1"]...)
		sort.Strings(got)
		want := []string{"add", "keep"}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("labels = %v, want %v", got, want)
		}
	})

	t.Run("SetWispLabels routes wisp table", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		if err := uc.SetWispLabels(ctx, "w-1", []string{"a"}, "me"); err != nil {
			t.Fatalf("SetWispLabels: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp setMany did not route wisp table")
		}
	})

	t.Run("empty id rejected", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		if err := uc.SetLabels(ctx, "", []string{"a"}, "me"); err == nil {
			t.Error("empty id accepted")
		}
	})

	t.Run("list-current error is wrapped", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.listErr = errors.New("list-boom")
		uc := NewLabelUseCase(repo)
		if err := uc.SetLabels(ctx, "bd-1", []string{"a"}, "me"); err == nil {
			t.Error("list error not surfaced")
		}
	})

	t.Run("delete-extra error is wrapped", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.store["bd-1"] = []string{"drop"}
		repo.deleteErr = errors.New("del-boom")
		uc := NewLabelUseCase(repo)
		if err := uc.SetLabels(ctx, "bd-1", []string{}, "me"); err == nil {
			t.Error("delete error not surfaced")
		}
	})

	t.Run("insert-missing error is wrapped", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.insertErr = errors.New("ins-boom")
		uc := NewLabelUseCase(repo)
		if err := uc.SetLabels(ctx, "bd-1", []string{"new"}, "me"); err == nil {
			t.Error("insert error not surfaced")
		}
	})
}

func TestLabelUseCase_Get(t *testing.T) {
	ctx := context.Background()

	t.Run("GetLabels returns stored; GetWispLabels routes wisp", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.store["bd-1"] = []string{"a", "b"}
		uc := NewLabelUseCase(repo)
		got, err := uc.GetLabels(ctx, "bd-1")
		if err != nil {
			t.Fatalf("GetLabels: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %v, want 2", got)
		}
		if _, err := uc.GetWispLabels(ctx, "w-1"); err != nil {
			t.Fatalf("GetWispLabels: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp list did not route wisp table")
		}
	})

	t.Run("GetLabels empty id rejected + wraps error", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		if _, err := uc.GetLabels(ctx, ""); err == nil {
			t.Error("empty id accepted")
		}
		repo.listErr = errors.New("boom")
		if _, err := uc.GetLabels(ctx, "bd-1"); err == nil {
			t.Error("list error not surfaced")
		}
	})

	t.Run("GetLabelsForIssues empty short-circuits; bulk error wrapped; wisp routes", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		got, err := uc.GetLabelsForIssues(ctx, nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("empty bulk = (%v, %v), want (empty, nil)", got, err)
		}
		repo.store["bd-1"] = []string{"a"}
		full, err := uc.GetLabelsForIssues(ctx, []string{"bd-1"})
		if err != nil || len(full["bd-1"]) != 1 {
			t.Fatalf("bulk = (%v, %v)", full, err)
		}
		if _, err := uc.GetLabelsForWisps(ctx, []string{"w-1"}); err != nil {
			t.Fatalf("GetLabelsForWisps: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp bulk did not route wisp table")
		}
		repo.bulkErr = errors.New("bulk-boom")
		if _, err := uc.GetLabelsForIssues(ctx, []string{"bd-1"}); err == nil {
			t.Error("bulk error not surfaced")
		}
	})
}

func TestLabelUseCase_Inherit(t *testing.T) {
	ctx := context.Background()

	t.Run("copies parent labels minus skip set", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.store["parent"] = []string{"keep1", "skipme", "keep2"}
		uc := NewLabelUseCase(repo)
		got, err := uc.InheritFromParent(ctx, "child", "parent", "me", []string{"skipme"})
		if err != nil {
			t.Fatalf("InheritFromParent: %v", err)
		}
		sort.Strings(got)
		if len(got) != 2 || got[0] != "keep1" || got[1] != "keep2" {
			t.Errorf("inherited = %v, want [keep1 keep2]", got)
		}
		if len(repo.store["child"]) != 2 {
			t.Errorf("child store = %v, want 2 labels", repo.store["child"])
		}
	})

	t.Run("empty childID/parentID rejected", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		if _, err := uc.InheritFromParent(ctx, "", "p", "me", nil); err == nil {
			t.Error("empty childID accepted")
		}
		if _, err := uc.InheritFromParent(ctx, "c", "", "me", nil); err == nil {
			t.Error("empty parentID accepted")
		}
	})

	t.Run("parent with no labels returns nil,nil", func(t *testing.T) {
		repo := newFakeLabelRepo()
		uc := NewLabelUseCase(repo)
		got, err := uc.InheritFromParent(ctx, "c", "p", "me", nil)
		if err != nil || got != nil {
			t.Errorf("got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("list-parent error wrapped", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.listErr = errors.New("list-boom")
		uc := NewLabelUseCase(repo)
		if _, err := uc.InheritFromParent(ctx, "c", "p", "me", nil); err == nil {
			t.Error("list error not surfaced")
		}
	})

	t.Run("insert error returns partial inherited + wrapped error", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.store["p"] = []string{"a"}
		repo.insertErr = errors.New("ins-boom")
		uc := NewLabelUseCase(repo)
		if _, err := uc.InheritFromParent(ctx, "c", "p", "me", nil); err == nil {
			t.Error("insert error not surfaced")
		}
	})

	t.Run("InheritFromWispParent routes wisp table", func(t *testing.T) {
		repo := newFakeLabelRepo()
		repo.store["wp"] = []string{"a"}
		uc := NewLabelUseCase(repo)
		if _, err := uc.InheritFromWispParent(ctx, "wc", "wp", "me", nil); err != nil {
			t.Fatalf("InheritFromWispParent: %v", err)
		}
		if !repo.lastOpts.UseWispsTable {
			t.Error("wisp inherit did not route wisp table")
		}
	})
}
