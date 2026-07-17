package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initBareBackedRepo creates a git repo with one commit and returns its
// (symlink-resolved) path. It reuses setupTestRepo from gitdir_test.go.
func initBareBackedRepo(t *testing.T) string {
	t.Helper()
	repoPath, _ := setupTestRepo(t)
	resolved, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", repoPath, err)
	}
	return resolved
}

// chdirTo changes into dir and resets the git context cache so the accessors
// re-read from the new location. The original directory is restored on cleanup.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q): %v", dir, err)
	}
	ResetCaches()
	t.Cleanup(func() {
		_ = os.Chdir(orig)
		ResetCaches()
	})
}

// TestContextAccessorsRegularRepo covers the git-context accessors that report
// paths for a plain (non-worktree) repository.
func TestContextAccessorsRegularRepo(t *testing.T) {
	repo := initBareBackedRepo(t)
	chdirTo(t, repo)

	if IsWorktree() {
		t.Error("IsWorktree() = true, want false for a plain repo")
	}

	gitDir, err := GetGitDir()
	if err != nil {
		t.Fatalf("GetGitDir: %v", err)
	}
	if gitDir == "" {
		t.Error("GetGitDir() returned empty string")
	}

	commonDir, err := GetGitCommonDir()
	if err != nil {
		t.Fatalf("GetGitCommonDir: %v", err)
	}
	if !filepath.IsAbs(commonDir) {
		t.Errorf("GetGitCommonDir() = %q, want absolute path", commonDir)
	}

	refsDir, err := GetGitRefsDir()
	if err != nil {
		t.Fatalf("GetGitRefsDir: %v", err)
	}
	if filepath.Base(refsDir) != "refs" {
		t.Errorf("GetGitRefsDir() = %q, want it to end in refs", refsDir)
	}

	headPath, err := GetGitHeadPath()
	if err != nil {
		t.Fatalf("GetGitHeadPath: %v", err)
	}
	if filepath.Base(headPath) != "HEAD" {
		t.Errorf("GetGitHeadPath() = %q, want it to end in HEAD", headPath)
	}

	root := GetRepoRoot()
	if root != repo {
		t.Errorf("GetRepoRoot() = %q, want %q", root, repo)
	}

	mainRoot, err := GetMainRepoRoot()
	if err != nil {
		t.Fatalf("GetMainRepoRoot: %v", err)
	}
	if mainRoot != repo {
		t.Errorf("GetMainRepoRoot() = %q, want %q for a plain repo", mainRoot, repo)
	}
}

// TestContextAccessorsWorktree covers the worktree-specific branches: IsWorktree
// must be true, and GetMainRepoRoot must return the main repo root (parent of the
// shared common dir), not the worktree's own path.
func TestContextAccessorsWorktree(t *testing.T) {
	main := initBareBackedRepo(t)

	// Create a linked worktree in a sibling directory outside the main tree.
	wtRoot := filepath.Join(filepath.Dir(main), "linked-wt")
	cmd := exec.Command("git", "worktree", "add", "-b", "feature", wtRoot, "HEAD")
	cmd.Dir = main
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	wtResolved, err := filepath.EvalSymlinks(wtRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", wtRoot, err)
	}
	chdirTo(t, wtResolved)

	if !IsWorktree() {
		t.Error("IsWorktree() = false, want true inside a linked worktree")
	}

	// In a worktree, git-dir and git-common-dir differ.
	gitDir, err := GetGitDir()
	if err != nil {
		t.Fatalf("GetGitDir: %v", err)
	}
	commonDir, err := GetGitCommonDir()
	if err != nil {
		t.Fatalf("GetGitCommonDir: %v", err)
	}
	absGitDir, _ := filepath.Abs(gitDir)
	if absGitDir == commonDir {
		t.Errorf("expected git-dir (%q) != common-dir (%q) inside a worktree", absGitDir, commonDir)
	}

	// GetMainRepoRoot must resolve to the main repo, not the worktree.
	mainRoot, err := GetMainRepoRoot()
	if err != nil {
		t.Fatalf("GetMainRepoRoot: %v", err)
	}
	mainRootResolved, _ := filepath.EvalSymlinks(mainRoot)
	if mainRootResolved != main {
		t.Errorf("GetMainRepoRoot() = %q (resolved %q), want main repo root %q", mainRoot, mainRootResolved, main)
	}
	if mainRootResolved == wtResolved {
		t.Errorf("GetMainRepoRoot() returned the worktree root %q, want the main repo root", wtResolved)
	}
}

// TestContextAccessorsNonRepo covers the error paths: outside any git repo, the
// accessors that return errors do so, and the string-returning ones return "".
func TestContextAccessorsNonRepo(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)

	if _, err := GetGitDir(); err == nil {
		t.Error("GetGitDir() error = nil, want error outside a repo")
	}
	if _, err := GetGitCommonDir(); err == nil {
		t.Error("GetGitCommonDir() error = nil, want error outside a repo")
	}
	if _, err := GetGitRefsDir(); err == nil {
		t.Error("GetGitRefsDir() error = nil, want error outside a repo")
	}
	if _, err := GetGitHeadPath(); err == nil {
		t.Error("GetGitHeadPath() error = nil, want error outside a repo")
	}
	if _, err := GetMainRepoRoot(); err == nil {
		t.Error("GetMainRepoRoot() error = nil, want error outside a repo")
	}
	if _, err := GetGitHooksDir(); err == nil {
		t.Error("GetGitHooksDir() error = nil, want error outside a repo")
	}
	if root := GetRepoRoot(); root != "" {
		t.Errorf("GetRepoRoot() = %q, want empty string outside a repo", root)
	}
	if IsWorktree() {
		t.Error("IsWorktree() = true, want false outside a repo")
	}
}

// TestGitContextFromEnv covers the parent-env fast path (beads-kpm): when
// BD_GIT_DIR/BD_GIT_COMMON_DIR/BD_GIT_TOPLEVEL are all set, the git context is
// built from them WITHOUT spawning `git rev-parse`. The test runs in a plain
// (non-repo) tempdir, so a real rev-parse would fail — success proves the env
// path was taken.
func TestGitContextFromEnv(t *testing.T) {
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	chdirTo(t, dir) // not a git repo; a git spawn here would error

	gitDir := filepath.Join(dir, ".git")
	t.Setenv("BD_GIT_DIR", gitDir)
	t.Setenv("BD_GIT_COMMON_DIR", gitDir)
	t.Setenv("BD_GIT_TOPLEVEL", dir)
	ResetCaches()
	t.Cleanup(ResetCaches)

	got, err := GetGitDir()
	if err != nil {
		t.Fatalf("GetGitDir with env fast path: %v", err)
	}
	if got != gitDir {
		t.Errorf("GetGitDir() = %q, want %q", got, gitDir)
	}
	if IsWorktree() {
		t.Error("IsWorktree() = true, want false when git-dir == common-dir")
	}
	if root := GetRepoRoot(); root != dir {
		t.Errorf("GetRepoRoot() = %q, want %q", root, dir)
	}
}

// TestGitContextFromEnvWorktree covers the env fast path deriving isWorktree
// from a differing git-dir and common-dir, and GetMainRepoRoot resolving to the
// parent of the common dir.
func TestGitContextFromEnvWorktree(t *testing.T) {
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	chdirTo(t, dir)

	mainRepo := filepath.Join(dir, "main")
	commonDir := filepath.Join(mainRepo, ".git")
	wtGitDir := filepath.Join(commonDir, "worktrees", "feature")
	t.Setenv("BD_GIT_DIR", wtGitDir)
	t.Setenv("BD_GIT_COMMON_DIR", commonDir)
	t.Setenv("BD_GIT_TOPLEVEL", filepath.Join(dir, "feature-wt"))
	ResetCaches()
	t.Cleanup(ResetCaches)

	if !IsWorktree() {
		t.Error("IsWorktree() = false, want true when git-dir != common-dir")
	}
	mainRoot, err := GetMainRepoRoot()
	if err != nil {
		t.Fatalf("GetMainRepoRoot: %v", err)
	}
	if mainRoot != mainRepo {
		t.Errorf("GetMainRepoRoot() = %q, want %q (parent of common dir)", mainRoot, mainRepo)
	}
}

// TestGitContextFromEnvPartialIgnored confirms a partial env set (missing one of
// the three vars) is ignored: the fast path requires all three, otherwise it
// falls back to spawning git (which errors in this non-repo tempdir).
func TestGitContextFromEnvPartialIgnored(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)

	t.Setenv("BD_GIT_DIR", filepath.Join(dir, ".git"))
	// BD_GIT_COMMON_DIR and BD_GIT_TOPLEVEL intentionally unset.
	ResetCaches()
	t.Cleanup(ResetCaches)

	if _, err := GetGitDir(); err == nil {
		t.Error("GetGitDir() error = nil, want fallback-to-git error with a partial env set")
	}
}

// TestGetGitHooksDirDefault covers the default hooks-dir branch (no
// core.hooksPath set): hooks live under the common git dir.
func TestGetGitHooksDirDefault(t *testing.T) {
	repo := initBareBackedRepo(t)
	chdirTo(t, repo)

	hooksDir, err := GetGitHooksDir()
	if err != nil {
		t.Fatalf("GetGitHooksDir: %v", err)
	}
	if filepath.Base(hooksDir) != "hooks" {
		t.Errorf("GetGitHooksDir() = %q, want it to end in hooks", hooksDir)
	}
	commonDir, err := GetGitCommonDir()
	if err != nil {
		t.Fatalf("GetGitCommonDir: %v", err)
	}
	if want := filepath.Join(commonDir, "hooks"); hooksDir != want {
		t.Errorf("GetGitHooksDir() = %q, want %q", hooksDir, want)
	}
}

// TestGetGitHooksDirAbsoluteConfig covers the branch where core.hooksPath is an
// absolute path: it is returned verbatim without joining to the repo root.
func TestGetGitHooksDirAbsoluteConfig(t *testing.T) {
	repo := initBareBackedRepo(t)
	absHooks := filepath.Join(t.TempDir(), "custom-hooks")

	cmd := exec.Command("git", "config", "core.hooksPath", absHooks)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config core.hooksPath: %v\n%s", err, out)
	}

	chdirTo(t, repo)

	hooksDir, err := GetGitHooksDir()
	if err != nil {
		t.Fatalf("GetGitHooksDir: %v", err)
	}
	if hooksDir != absHooks {
		t.Errorf("GetGitHooksDir() = %q, want absolute config value %q", hooksDir, absHooks)
	}
}

// TestGetJJPrimaryWorkspaceRootFromErrors covers the error branches of
// GetJJPrimaryWorkspaceRootFrom that don't depend on the current directory.
func TestGetJJPrimaryWorkspaceRootFromErrors(t *testing.T) {
	t.Run("not a jj workspace", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := GetJJPrimaryWorkspaceRootFrom(dir); err == nil {
			t.Error("error = nil, want error when startDir is not a jj workspace")
		}
	})

	t.Run("empty .jj/repo file", func(t *testing.T) {
		dir := t.TempDir()
		makeJJSecondaryWorkspace(t, dir, "") // writes just "\n"
		if _, err := GetJJPrimaryWorkspaceRootFrom(dir); err == nil {
			t.Error("error = nil, want error for an empty .jj/repo file")
		}
	})

	t.Run("relative path resolves to primary root", func(t *testing.T) {
		tmp := t.TempDir()
		tmp, _ = filepath.EvalSymlinks(tmp)
		primary := filepath.Join(tmp, "primary")
		secondary := filepath.Join(tmp, "secondary")
		makeJJPrimaryWorkspace(t, primary)
		makeJJSecondaryWorkspace(t, secondary, "../../primary/.jj/repo")

		got, err := GetJJPrimaryWorkspaceRootFrom(secondary)
		if err != nil {
			t.Fatalf("GetJJPrimaryWorkspaceRootFrom: %v", err)
		}
		if got != primary {
			t.Errorf("GetJJPrimaryWorkspaceRootFrom() = %q, want %q", got, primary)
		}
	})
}

// TestJJSecondaryWorkspaceRootFromCases covers the path-aware secondary-root
// resolver across the non-jj, primary, and secondary cases.
func TestJJSecondaryWorkspaceRootFromCases(t *testing.T) {
	t.Run("not a jj repo", func(t *testing.T) {
		dir := t.TempDir()
		if _, ok := JJSecondaryWorkspaceRootFrom(dir); ok {
			t.Error("ok = true, want false outside a jj workspace")
		}
	})

	t.Run("primary workspace is not secondary", func(t *testing.T) {
		dir := t.TempDir()
		makeJJPrimaryWorkspace(t, dir) // .jj/repo is a directory
		if _, ok := JJSecondaryWorkspaceRootFrom(dir); ok {
			t.Error("ok = true, want false for a primary workspace")
		}
	})

	t.Run("secondary workspace resolves root", func(t *testing.T) {
		tmp := t.TempDir()
		tmp, _ = filepath.EvalSymlinks(tmp)
		primary := filepath.Join(tmp, "primary")
		secondary := filepath.Join(tmp, "secondary")
		makeJJPrimaryWorkspace(t, primary)
		makeJJSecondaryWorkspace(t, secondary, filepath.Join(primary, ".jj", "repo"))

		root, ok := JJSecondaryWorkspaceRootFrom(secondary)
		if !ok {
			t.Fatal("ok = false, want true for a secondary workspace")
		}
		rootResolved, _ := filepath.EvalSymlinks(root)
		if rootResolved != secondary {
			t.Errorf("JJSecondaryWorkspaceRootFrom() = %q, want %q", rootResolved, secondary)
		}
	})
}

// TestGetJujutsuRootFromRelative confirms getJujutsuRootFrom makes a relative
// startDir absolute before walking (a bare "." must not stall at Dir(".")).
func TestGetJujutsuRootFromRelative(t *testing.T) {
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	if err := os.Mkdir(filepath.Join(tmp, ".jj"), 0750); err != nil {
		t.Fatalf("mkdir .jj: %v", err)
	}
	chdirTo(t, tmp)

	root, err := getJujutsuRootFrom(".")
	if err != nil {
		t.Fatalf("getJujutsuRootFrom(\".\"): %v", err)
	}
	if root != tmp {
		t.Errorf("getJujutsuRootFrom(\".\") = %q, want %q", root, tmp)
	}
}

// TestNormalizePathNonWindows documents the non-Windows no-op behavior of
// NormalizePath (the Windows conversion branches only run when the path
// separator is a backslash).
func TestNormalizePathNonWindows(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("Windows path normalization is exercised by its own conversions")
	}
	in := "/c/Users/example/path"
	if got := NormalizePath(in); got != in {
		t.Errorf("NormalizePath(%q) = %q, want unchanged on non-Windows", in, got)
	}
}
