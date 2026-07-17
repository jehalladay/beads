package domain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeGitRepo is a hermetic GitRepository. Bool probes return the paired
// (value, err); the error hooks make the wrapper error branches reachable.
type fakeGitRepo struct {
	isRepo    bool
	isBare    bool
	isJJ      bool
	isColo    bool
	remoteURL string
	remoteHit bool
	config    string
	configHit bool
	branch    string
	hasUpstrm bool
	names     []string
	commit    GitCommitResult

	isRepoErr    error
	isBareErr    error
	isJJErr      error
	isColoErr    error
	initErr      error
	getCfgErr    error
	setCfgErr    error
	getRemoteErr error
	namesErr     error
	branchErr    error
	upstreamErr  error
	addErr       error
	commitErr    error

	didInit   bool
	added     []string
	committed bool
}

func (f *fakeGitRepo) IsGitRepo(ctx context.Context) (bool, error)     { return f.isRepo, f.isRepoErr }
func (f *fakeGitRepo) IsBareGitRepo(ctx context.Context) (bool, error) { return f.isBare, f.isBareErr }
func (f *fakeGitRepo) IsJujutsuRepo(ctx context.Context) (bool, error) { return f.isJJ, f.isJJErr }
func (f *fakeGitRepo) IsColocatedJJGit(ctx context.Context) (bool, error) {
	return f.isColo, f.isColoErr
}

func (f *fakeGitRepo) Init(ctx context.Context) error {
	if f.initErr != nil {
		return f.initErr
	}
	f.didInit = true
	return nil
}

func (f *fakeGitRepo) GetConfig(ctx context.Context, key string) (string, bool, error) {
	return f.config, f.configHit, f.getCfgErr
}

func (f *fakeGitRepo) SetConfig(ctx context.Context, key, value string) error { return f.setCfgErr }

func (f *fakeGitRepo) GetRemoteURL(ctx context.Context, name string) (string, bool, error) {
	return f.remoteURL, f.remoteHit, f.getRemoteErr
}

func (f *fakeGitRepo) ListRemoteNames(ctx context.Context) ([]string, error) {
	return f.names, f.namesErr
}

func (f *fakeGitRepo) CurrentBranch(ctx context.Context) (string, error) {
	return f.branch, f.branchErr
}

func (f *fakeGitRepo) BranchHasUpstream(ctx context.Context, branch string) (bool, error) {
	return f.hasUpstrm, f.upstreamErr
}

func (f *fakeGitRepo) Add(ctx context.Context, paths ...string) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.added = append(f.added, paths...)
	return nil
}

func (f *fakeGitRepo) Commit(ctx context.Context, params GitCommitParams) (GitCommitResult, error) {
	if f.commitErr != nil {
		return GitCommitResult{}, f.commitErr
	}
	f.committed = true
	return f.commit, nil
}

var _ GitRepository = (*fakeGitRepo)(nil)

func TestGitUseCase_BoolProbes(t *testing.T) {
	ctx := context.Background()

	// true value, no error.
	yes := &fakeGitRepo{isRepo: true, isBare: true, isJJ: true, isColo: true}
	uc := NewGitUseCase("", yes)
	if !uc.IsGitRepo(ctx) || !uc.IsBareGitRepo(ctx) || !uc.IsJujutsuRepo(ctx) || !uc.IsColocatedJJGit(ctx) {
		t.Error("true probes returned false")
	}

	// error path: the wrapper swallows the error and returns false.
	boom := &fakeGitRepo{
		isRepoErr: errors.New("x"), isBareErr: errors.New("x"),
		isJJErr: errors.New("x"), isColoErr: errors.New("x"),
	}
	uc = NewGitUseCase("", boom)
	if uc.IsGitRepo(ctx) || uc.IsBareGitRepo(ctx) || uc.IsJujutsuRepo(ctx) || uc.IsColocatedJJGit(ctx) {
		t.Error("errored probes returned true")
	}
}

func TestGitUseCase_EnsureGitRepo(t *testing.T) {
	ctx := context.Background()

	// check error is wrapped.
	ce := &fakeGitRepo{isRepoErr: errors.New("check-boom")}
	if _, err := NewGitUseCase("", ce).EnsureGitRepo(ctx); !errors.Is(err, ce.isRepoErr) {
		t.Errorf("err = %v, want wrapped check error", err)
	}

	// already exists.
	ex := &fakeGitRepo{isRepo: true}
	r, err := NewGitUseCase("", ex).EnsureGitRepo(ctx)
	if err != nil || !r.AlreadyExists || r.DidInit {
		t.Fatalf("EnsureGitRepo exists = %+v, %v", r, err)
	}

	// init error is wrapped.
	ie := &fakeGitRepo{initErr: errors.New("init-boom")}
	if _, err := NewGitUseCase("", ie).EnsureGitRepo(ctx); !errors.Is(err, ie.initErr) {
		t.Errorf("err = %v, want wrapped init error", err)
	}

	// fresh init.
	fresh := &fakeGitRepo{}
	r, err = NewGitUseCase("", fresh).EnsureGitRepo(ctx)
	if err != nil || !r.DidInit || r.AlreadyExists {
		t.Fatalf("EnsureGitRepo init = %+v, %v", r, err)
	}
	if !fresh.didInit {
		t.Error("Init not called")
	}
}

func TestGitUseCase_OriginRemoteURL(t *testing.T) {
	ctx := context.Background()

	// not a repo -> empty, nil.
	if url, err := NewGitUseCase("", &fakeGitRepo{}).OriginRemoteURL(ctx); url != "" || err != nil {
		t.Errorf("not-a-repo = %q, %v", url, err)
	}
	// IsGitRepo error surfaced.
	re := &fakeGitRepo{isRepoErr: errors.New("boom")}
	if _, err := NewGitUseCase("", re).OriginRemoteURL(ctx); !errors.Is(err, re.isRepoErr) {
		t.Errorf("err = %v, want IsGitRepo error", err)
	}
	// bare repo -> empty, nil.
	if url, err := NewGitUseCase("", &fakeGitRepo{isRepo: true, isBare: true}).OriginRemoteURL(ctx); url != "" || err != nil {
		t.Errorf("bare = %q, %v", url, err)
	}
	// IsBareGitRepo error surfaced.
	be := &fakeGitRepo{isRepo: true, isBareErr: errors.New("bare-boom")}
	if _, err := NewGitUseCase("", be).OriginRemoteURL(ctx); !errors.Is(err, be.isBareErr) {
		t.Errorf("err = %v, want IsBare error", err)
	}
	// happy: origin url returned.
	ok := &fakeGitRepo{isRepo: true, remoteURL: "git@x", remoteHit: true}
	if url, err := NewGitUseCase("", ok).OriginRemoteURL(ctx); err != nil || url != "git@x" {
		t.Fatalf("OriginRemoteURL = %q, %v", url, err)
	}
}

func TestGitUseCase_DetectFork(t *testing.T) {
	ctx := context.Background()

	// not a repo.
	if isFork, url, err := NewGitUseCase("", &fakeGitRepo{}).DetectFork(ctx); isFork || url != "" || err != nil {
		t.Errorf("not-a-repo = %v %q %v", isFork, url, err)
	}
	// IsGitRepo error surfaced.
	re := &fakeGitRepo{isRepoErr: errors.New("boom")}
	if _, _, err := NewGitUseCase("", re).DetectFork(ctx); !errors.Is(err, re.isRepoErr) {
		t.Errorf("err = %v, want IsGitRepo error", err)
	}
	// no upstream remote -> not a fork.
	if isFork, _, err := NewGitUseCase("", &fakeGitRepo{isRepo: true}).DetectFork(ctx); isFork || err != nil {
		t.Errorf("no-upstream = %v %v", isFork, err)
	}
	// GetRemoteURL error surfaced.
	ge := &fakeGitRepo{isRepo: true, getRemoteErr: errors.New("rem-boom")}
	if _, _, err := NewGitUseCase("", ge).DetectFork(ctx); !errors.Is(err, ge.getRemoteErr) {
		t.Errorf("err = %v, want GetRemoteURL error", err)
	}
	// fork detected.
	ok := &fakeGitRepo{isRepo: true, remoteURL: "up@x", remoteHit: true}
	if isFork, url, err := NewGitUseCase("", ok).DetectFork(ctx); err != nil || !isFork || url != "up@x" {
		t.Fatalf("DetectFork = %v %q %v", isFork, url, err)
	}
}

func TestGitUseCase_BeadsRole(t *testing.T) {
	ctx := context.Background()

	// BeadsRole passes through repo GetConfig.
	g := &fakeGitRepo{config: "engineer", configHit: true}
	role, has, err := NewGitUseCase("", g).BeadsRole(ctx)
	if err != nil || !has || role != "engineer" {
		t.Fatalf("BeadsRole = %q %v %v", role, has, err)
	}

	// SetBeadsRole rejects empty.
	if err := NewGitUseCase("", &fakeGitRepo{}).SetBeadsRole(ctx, ""); err == nil {
		t.Error("empty role accepted")
	}
	// SetBeadsRole error surfaced.
	se := &fakeGitRepo{setCfgErr: errors.New("set-boom")}
	if err := NewGitUseCase("", se).SetBeadsRole(ctx, "r"); !errors.Is(err, se.setCfgErr) {
		t.Errorf("err = %v, want set error", err)
	}
	// happy.
	if err := NewGitUseCase("", &fakeGitRepo{}).SetBeadsRole(ctx, "r"); err != nil {
		t.Fatalf("SetBeadsRole: %v", err)
	}
}

func TestGitUseCase_HasAnyRemotes(t *testing.T) {
	ctx := context.Background()

	// error -> false.
	if NewGitUseCase("", &fakeGitRepo{namesErr: errors.New("x")}).HasAnyRemotes(ctx) {
		t.Error("error returned true")
	}
	// none -> false.
	if NewGitUseCase("", &fakeGitRepo{}).HasAnyRemotes(ctx) {
		t.Error("empty returned true")
	}
	// some -> true.
	if !NewGitUseCase("", &fakeGitRepo{names: []string{"origin"}}).HasAnyRemotes(ctx) {
		t.Error("non-empty returned false")
	}
}

func TestGitUseCase_HasUpstream(t *testing.T) {
	ctx := context.Background()

	// CurrentBranch error -> false.
	if NewGitUseCase("", &fakeGitRepo{branchErr: errors.New("x")}).HasUpstream(ctx) {
		t.Error("branch error returned true")
	}
	// empty branch -> false.
	if NewGitUseCase("", &fakeGitRepo{branch: ""}).HasUpstream(ctx) {
		t.Error("empty branch returned true")
	}
	// BranchHasUpstream error -> false.
	if NewGitUseCase("", &fakeGitRepo{branch: "main", upstreamErr: errors.New("x")}).HasUpstream(ctx) {
		t.Error("upstream error returned true")
	}
	// has upstream -> true.
	if !NewGitUseCase("", &fakeGitRepo{branch: "main", hasUpstrm: true}).HasUpstream(ctx) {
		t.Error("upstream returned false")
	}
}

func TestGitUseCase_CommitInitArtifacts(t *testing.T) {
	ctx := context.Background()

	// empty BeadsDir rejected.
	if _, err := NewGitUseCase("", &fakeGitRepo{}).CommitInitArtifacts(ctx, CommitInitArtifactsParams{Message: "m"}); err == nil {
		t.Error("empty BeadsDir accepted")
	}
	// empty Message rejected.
	if _, err := NewGitUseCase("", &fakeGitRepo{}).CommitInitArtifacts(ctx, CommitInitArtifactsParams{BeadsDir: ".beads"}); err == nil {
		t.Error("empty Message accepted")
	}

	// optional paths: an existing relative path is staged, a missing one and
	// an empty entry are skipped; an absolute existing path is staged as-is.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "present.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	absFile := filepath.Join(dir, "abs.txt")
	if err := os.WriteFile(absFile, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := &fakeGitRepo{commit: GitCommitResult{DidCommit: true}}
	uc := NewGitUseCase(dir, repo)
	res, err := uc.CommitInitArtifacts(ctx, CommitInitArtifactsParams{
		BeadsDir:      ".beads",
		OptionalPaths: []string{"", "present.txt", "missing.txt", absFile},
		Message:       "init",
		NoVerify:      true,
	})
	if err != nil {
		t.Fatalf("CommitInitArtifacts: %v", err)
	}
	if !res.DidCommit {
		t.Error("DidCommit false")
	}
	// .beads always staged; present.txt + absFile staged; empty + missing skipped.
	want := map[string]bool{".beads": true, "present.txt": true, absFile: true}
	if len(res.StagedPaths) != len(want) {
		t.Fatalf("staged = %v, want %v", res.StagedPaths, want)
	}
	for _, p := range res.StagedPaths {
		if !want[p] {
			t.Errorf("unexpected staged path %q", p)
		}
	}

	// add error is wrapped.
	ae := &fakeGitRepo{addErr: errors.New("add-boom")}
	if _, err := NewGitUseCase(dir, ae).CommitInitArtifacts(ctx, CommitInitArtifactsParams{BeadsDir: ".beads", Message: "m"}); !errors.Is(err, ae.addErr) {
		t.Errorf("err = %v, want add error", err)
	}

	// commit error is wrapped.
	ceq := &fakeGitRepo{commitErr: errors.New("commit-boom")}
	if _, err := NewGitUseCase(dir, ceq).CommitInitArtifacts(ctx, CommitInitArtifactsParams{BeadsDir: ".beads", Message: "m"}); !errors.Is(err, ceq.commitErr) {
		t.Errorf("err = %v, want commit error", err)
	}
}
