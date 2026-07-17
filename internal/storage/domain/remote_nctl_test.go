package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRemoteRepo is a hermetic RemoteSQLRepository backed by an ordered slice.
// Per-method error hooks drive the error branches; addErrOnce lets UpdateRemote
// fail only the *first* AddRemote (the new URL) while allowing the restore
// AddRemote (the old URL) to succeed.
type fakeRemoteRepo struct {
	remotes []Remote

	addErr     error
	addErrOnce bool // when true, addErr fires once then clears
	removeErr  error
	listErr    error

	addCalls    int
	removeCalls int
}

func (f *fakeRemoteRepo) AddRemote(ctx context.Context, name, url string) error {
	f.addCalls++
	if f.addErr != nil {
		err := f.addErr
		if f.addErrOnce {
			f.addErr = nil
		}
		return err
	}
	// upsert
	for i := range f.remotes {
		if f.remotes[i].Name == name {
			f.remotes[i].URL = url
			return nil
		}
	}
	f.remotes = append(f.remotes, Remote{Name: name, URL: url})
	return nil
}

func (f *fakeRemoteRepo) RemoveRemote(ctx context.Context, name string) error {
	f.removeCalls++
	if f.removeErr != nil {
		return f.removeErr
	}
	out := f.remotes[:0:0]
	for _, r := range f.remotes {
		if r.Name != name {
			out = append(out, r)
		}
	}
	f.remotes = out
	return nil
}

func (f *fakeRemoteRepo) ListRemotes(ctx context.Context) ([]Remote, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.remotes, nil
}

func (f *fakeRemoteRepo) urlOf(name string) string {
	for _, r := range f.remotes {
		if r.Name == name {
			return r.URL
		}
	}
	return ""
}

func TestDoltRemoteUseCase_Create(t *testing.T) {
	ctx := context.Background()
	uc := NewDoltRemoteUseCase(&fakeRemoteRepo{})

	if err := uc.CreateRemote(ctx, "", "u"); err == nil {
		t.Error("empty name accepted")
	}
	if err := uc.CreateRemote(ctx, "origin", ""); err == nil {
		t.Error("empty url accepted")
	}

	repo := &fakeRemoteRepo{}
	uc = NewDoltRemoteUseCase(repo)
	if err := uc.CreateRemote(ctx, "origin", "file:///x"); err != nil {
		t.Fatalf("CreateRemote: %v", err)
	}
	if repo.urlOf("origin") != "file:///x" {
		t.Errorf("remote not stored: %v", repo.remotes)
	}

	repo.addErr = errors.New("add-boom")
	if err := uc.CreateRemote(ctx, "two", "u"); !errors.Is(err, repo.addErr) {
		t.Fatalf("err = %v, want wrapped add error", err)
	}
}

func TestDoltRemoteUseCase_Delete(t *testing.T) {
	ctx := context.Background()

	uc := NewDoltRemoteUseCase(&fakeRemoteRepo{})
	if err := uc.DeleteRemote(ctx, ""); err == nil {
		t.Error("empty name accepted")
	}

	repo := &fakeRemoteRepo{remotes: []Remote{{Name: "origin", URL: "u"}}}
	uc = NewDoltRemoteUseCase(repo)
	if err := uc.DeleteRemote(ctx, "origin"); err != nil {
		t.Fatalf("DeleteRemote: %v", err)
	}
	if len(repo.remotes) != 0 {
		t.Errorf("remote not removed: %v", repo.remotes)
	}

	repo.removeErr = errors.New("rm-boom")
	if err := uc.DeleteRemote(ctx, "origin"); !errors.Is(err, repo.removeErr) {
		t.Fatalf("err = %v, want wrapped remove error", err)
	}
}

func TestDoltRemoteUseCase_List(t *testing.T) {
	ctx := context.Background()

	repo := &fakeRemoteRepo{remotes: []Remote{{Name: "a", URL: "1"}, {Name: "b", URL: "2"}}}
	uc := NewDoltRemoteUseCase(repo)
	got, err := uc.ListRemotes(ctx)
	if err != nil {
		t.Fatalf("ListRemotes: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d remotes, want 2", len(got))
	}

	repo.listErr = errors.New("list-boom")
	if _, err := uc.ListRemotes(ctx); !errors.Is(err, repo.listErr) {
		t.Fatalf("err = %v, want wrapped list error", err)
	}
}

func TestDoltRemoteUseCase_Update(t *testing.T) {
	ctx := context.Background()

	t.Run("empty name/url rejected before repo", func(t *testing.T) {
		repo := &fakeRemoteRepo{}
		uc := NewDoltRemoteUseCase(repo)
		if err := uc.UpdateRemote(ctx, "", "u"); err == nil {
			t.Error("empty name accepted")
		}
		if err := uc.UpdateRemote(ctx, "origin", ""); err == nil {
			t.Error("empty url accepted")
		}
		if repo.removeCalls != 0 || repo.addCalls != 0 {
			t.Error("repo touched on invalid input")
		}
	})

	t.Run("happy path: remove then add", func(t *testing.T) {
		repo := &fakeRemoteRepo{remotes: []Remote{{Name: "origin", URL: "old"}}}
		uc := NewDoltRemoteUseCase(repo)
		if err := uc.UpdateRemote(ctx, "origin", "new"); err != nil {
			t.Fatalf("UpdateRemote: %v", err)
		}
		if repo.urlOf("origin") != "new" {
			t.Errorf("url = %q, want new", repo.urlOf("origin"))
		}
	})

	t.Run("remove error is wrapped", func(t *testing.T) {
		repo := &fakeRemoteRepo{remotes: []Remote{{Name: "origin", URL: "old"}}}
		repo.removeErr = errors.New("rm-boom")
		uc := NewDoltRemoteUseCase(repo)
		if err := uc.UpdateRemote(ctx, "origin", "new"); err == nil {
			t.Error("remove error not surfaced")
		}
	})

	t.Run("add failure restores previous URL", func(t *testing.T) {
		repo := &fakeRemoteRepo{remotes: []Remote{{Name: "origin", URL: "old"}}}
		// First AddRemote (the new URL) fails; restore AddRemote(old) succeeds.
		repo.addErr = errors.New("add-boom")
		repo.addErrOnce = true
		uc := NewDoltRemoteUseCase(repo)
		err := uc.UpdateRemote(ctx, "origin", "new")
		if err == nil {
			t.Fatal("expected an error when the new-URL add fails")
		}
		if repo.urlOf("origin") != "old" {
			t.Errorf("previous URL not restored: url = %q, want old", repo.urlOf("origin"))
		}
	})

	t.Run("add failure with no previous URL cannot restore", func(t *testing.T) {
		// oldURL stays empty (remote not present in list) -> plain add error.
		repo := &fakeRemoteRepo{}
		repo.addErr = errors.New("add-boom")
		uc := NewDoltRemoteUseCase(repo)
		if err := uc.UpdateRemote(ctx, "ghost", "new"); err == nil {
			t.Error("add error not surfaced")
		}
	})

	t.Run("add failure AND restore failure both reported", func(t *testing.T) {
		repo := &fakeRemoteRepo{remotes: []Remote{{Name: "origin", URL: "old"}}}
		// addErr persists (not once) so both the new-URL add and the restore add fail.
		repo.addErr = errors.New("add-boom")
		uc := NewDoltRemoteUseCase(repo)
		err := uc.UpdateRemote(ctx, "origin", "new")
		if err == nil {
			t.Fatal("expected an error when both add and restore fail")
		}
		// The message must mention the restore also failed.
		if !strings.Contains(err.Error(), "restoring previous URL") {
			t.Errorf("err = %q, want it to mention the failed restore", err.Error())
		}
	})
}
