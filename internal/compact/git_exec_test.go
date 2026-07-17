package compact

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	beadsctx "github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/git"
)

// beads-xcy1: cover the PRODUCTION defaultGitExec path (0%). The other git
// tests swap the gitExec hook for a mock, so defaultGitExec itself — which
// resolves a RepoContext and runs real git — is never exercised. Drive it
// through GetCurrentCommitHash against a real temp git repo.

// initGitRepo creates a minimal git repo in dir with one commit and returns its
// HEAD sha. Skips the test if git is unavailable.
func initGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("commit", "--allow-empty", "-m", "initial")
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return string(out)
}

func TestDefaultGitExec_RealRepo(t *testing.T) {
	// Ensure the production hook is in effect (other tests may have swapped it),
	// and reset the repo/git caches so GetRepoContext resolves our temp repo.
	orig := gitExec
	gitExec = defaultGitExec
	t.Cleanup(func() { gitExec = orig })

	dir := t.TempDir()
	want := initGitRepo(t, dir)

	// GetRepoContext requires a .beads directory; point BEADS_DIR at one inside
	// the repo so defaultGitExec resolves this repo's RepoContext.
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	// FindBeadsDir requires the dir to contain project files (metadata.json,
	// config.yaml, a dolt/ dir, or a *.db) — an empty .beads is ignored.
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	t.Setenv("BEADS_DIR", beadsDir)

	t.Chdir(dir)
	beadsctx.ResetCaches()
	git.ResetCaches()
	t.Cleanup(func() {
		beadsctx.ResetCaches()
		git.ResetCaches()
	})

	got := GetCurrentCommitHash()
	if got == "" {
		t.Fatal("expected a commit hash from the real repo, got empty")
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(got) {
		t.Errorf("expected a 40-char hex sha, got %q", got)
	}
	// defaultGitExec's output feeds GetCurrentCommitHash's TrimSpace — the
	// result must match git's own rev-parse (minus the trailing newline).
	if got != want[:40] {
		t.Errorf("hash mismatch: got %q want %q", got, want[:40])
	}
}

func TestDefaultGitExec_NotARepo(t *testing.T) {
	orig := gitExec
	gitExec = defaultGitExec
	t.Cleanup(func() { gitExec = orig })

	// A bare temp dir with no git repo (and no .beads) → GetRepoContext errors,
	// so defaultGitExec returns that error and GetCurrentCommitHash yields "".
	dir := t.TempDir()
	t.Chdir(dir)
	beadsctx.ResetCaches()
	git.ResetCaches()
	t.Cleanup(func() {
		beadsctx.ResetCaches()
		git.ResetCaches()
	})

	if got := GetCurrentCommitHash(); got != "" {
		t.Errorf("expected empty hash outside a git repo, got %q", got)
	}
}
