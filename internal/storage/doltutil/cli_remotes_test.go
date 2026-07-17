package doltutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireDolt skips the test when the dolt CLI is not installed. It is a local
// copy of testutil.RequireDoltBinary — testutil imports this package
// (testdoltbranch.go), so importing it back would form a test import cycle.
// Under GitHub Actions a missing binary is fatal (CI must install dolt).
func requireDolt(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("dolt"); err != nil {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("dolt binary missing under GITHUB_ACTIONS: %v", err)
		}
		t.Skipf("dolt binary not found: %v", err)
	}
}

// initDoltRepo creates an isolated, hermetic dolt repository in a temp dir and
// returns its path. It sets HOME to a per-test temp dir and writes a dolt
// identity there so `dolt init` / `dolt remote add` never prompt for one or
// touch the developer's real ~/.dolt config. Skips the test if dolt is absent.
func initDoltRepo(t *testing.T) string {
	t.Helper()
	requireDolt(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	// Some environments set these to point the CLI at a shared server / creds;
	// clear them so `dolt remote add` operates purely on the local repo state.
	t.Setenv("DOLT_CLI_USER", "")
	t.Setenv("DOLT_CLI_PASSWORD", "")
	t.Setenv("DOLT_ROOT_PATH", "")

	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = home
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
		}
	}
	// A global identity is required for `dolt init` to commit the genesis.
	run("dolt", "config", "--global", "--add", "user.name", "doltutil-test")
	run("dolt", "config", "--global", "--add", "user.email", "doltutil-test@example.com")

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("dolt", "init")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dolt init failed: %v\n%s", err, out)
	}
	return repo
}

// TestCLIRemoteLifecycle exercises the full CLI-remote surface against a real
// (hermetic) dolt repo: add → find → list → remove. This path is the SQL-bypass
// local mirror used by subprocess push/pull/fetch routing (remotes.go).
func TestCLIRemoteLifecycle(t *testing.T) {
	repo := initDoltRepo(t)

	const name, url = "origin", "file:///tmp/doltutil-test-remote"

	// Nothing added yet.
	if got := FindCLIRemote(repo, name); got != "" {
		t.Fatalf("FindCLIRemote before add = %q, want empty", got)
	}
	if remotes, err := ListCLIRemotes(repo); err != nil || len(remotes) != 0 {
		t.Fatalf("ListCLIRemotes before add = %v, %v; want empty, nil", remotes, err)
	}

	// Add.
	if err := AddCLIRemote(repo, name, url); err != nil {
		t.Fatalf("AddCLIRemote: %v", err)
	}
	if got := FindCLIRemote(repo, name); got != url {
		t.Fatalf("FindCLIRemote after add = %q, want %q", got, url)
	}
	remotes, err := ListCLIRemotes(repo)
	if err != nil {
		t.Fatalf("ListCLIRemotes: %v", err)
	}
	if len(remotes) != 1 || remotes[0].Name != name || remotes[0].URL != url {
		t.Fatalf("ListCLIRemotes after add = %+v, want one %s→%s", remotes, name, url)
	}

	// A different name is not found.
	if got := FindCLIRemote(repo, "upstream"); got != "" {
		t.Fatalf("FindCLIRemote(upstream) = %q, want empty (only origin exists)", got)
	}

	// Remove.
	if err := RemoveCLIRemote(repo, name); err != nil {
		t.Fatalf("RemoveCLIRemote: %v", err)
	}
	if got := FindCLIRemote(repo, name); got != "" {
		t.Fatalf("FindCLIRemote after remove = %q, want empty", got)
	}
}

// TestEnsureCLIRemote covers the idempotent no-op, the fresh-add, and the
// replace-when-URL-differs branches of EnsureCLIRemote against a real repo.
func TestEnsureCLIRemote(t *testing.T) {
	repo := initDoltRepo(t)

	const name = "origin"
	const url1 = "file:///tmp/doltutil-ensure-a"
	const url2 = "file:///tmp/doltutil-ensure-b"

	// Fresh add: remote absent → EnsureCLIRemote creates it.
	if err := EnsureCLIRemote(repo, name, url1); err != nil {
		t.Fatalf("EnsureCLIRemote fresh add: %v", err)
	}
	if got := FindCLIRemote(repo, name); got != url1 {
		t.Fatalf("after fresh ensure, FindCLIRemote = %q, want %q", got, url1)
	}

	// Idempotent no-op: same URL → no error, unchanged, still exactly one remote.
	if err := EnsureCLIRemote(repo, name, url1); err != nil {
		t.Fatalf("EnsureCLIRemote idempotent: %v", err)
	}
	remotes, err := ListCLIRemotes(repo)
	if err != nil {
		t.Fatalf("ListCLIRemotes: %v", err)
	}
	if len(remotes) != 1 || remotes[0].URL != url1 {
		t.Fatalf("after idempotent ensure, remotes = %+v, want single %s", remotes, url1)
	}

	// Replace: different URL → remove old + add new.
	if err := EnsureCLIRemote(repo, name, url2); err != nil {
		t.Fatalf("EnsureCLIRemote replace: %v", err)
	}
	if got := FindCLIRemote(repo, name); got != url2 {
		t.Fatalf("after replace ensure, FindCLIRemote = %q, want %q", got, url2)
	}
}

// TestEnsureCLIRemoteNormalizedMatchIsNoOp verifies that a URL differing from
// the stored one only by Dolt-normalization equivalence is treated as a match
// (RemoteURLsMatch branch), so no mutation occurs.
func TestEnsureCLIRemoteNormalizedMatchIsNoOp(t *testing.T) {
	repo := initDoltRepo(t)

	const name = "origin"
	// dolt stores git-over-https as the normalized git+https form.
	if err := AddCLIRemote(repo, name, "https://github.com/org/repo.git"); err != nil {
		t.Fatalf("AddCLIRemote: %v", err)
	}
	stored := FindCLIRemote(repo, name)
	if stored == "" {
		t.Fatal("remote not stored")
	}

	// Ensuring the normalization-equivalent URL must be a no-op (no error).
	if err := EnsureCLIRemote(repo, name, stored); err != nil {
		t.Fatalf("EnsureCLIRemote with stored URL: %v", err)
	}
	if got := FindCLIRemote(repo, name); got != stored {
		t.Fatalf("URL changed unexpectedly: got %q, want %q", got, stored)
	}
}

// TestCLIRemoteValidationRejects covers the ValidateRemoteName / URL guard
// branches of the mutating helpers. These reject before shelling out, so they
// need no dolt repo — but a valid dir keeps the failure attributable to
// validation rather than a missing repo.
func TestCLIRemoteValidationRejects(t *testing.T) {
	dir := t.TempDir()

	t.Run("AddCLIRemote bad name", func(t *testing.T) {
		if err := AddCLIRemote(dir, "-bad", "file:///tmp/x"); err == nil {
			t.Fatal("AddCLIRemote with leading-dash name = nil, want error")
		}
	})
	t.Run("AddCLIRemote bad url", func(t *testing.T) {
		if err := AddCLIRemote(dir, "origin", ""); err == nil {
			t.Fatal("AddCLIRemote with empty URL = nil, want error")
		}
	})
	t.Run("RemoveCLIRemote bad name", func(t *testing.T) {
		if err := RemoveCLIRemote(dir, "bad name with spaces"); err == nil {
			t.Fatal("RemoveCLIRemote with invalid name = nil, want error")
		}
	})
	t.Run("EnsureCLIRemote bad name", func(t *testing.T) {
		if err := EnsureCLIRemote(dir, "", "file:///tmp/x"); err == nil {
			t.Fatal("EnsureCLIRemote with empty name = nil, want error")
		}
	})
	t.Run("EnsureCLIRemote bad url", func(t *testing.T) {
		if err := EnsureCLIRemote(dir, "origin", "not-a-valid-url-no-scheme"); err == nil {
			t.Fatal("EnsureCLIRemote with schemeless URL = nil, want error")
		}
	})
}

// TestListCLIRemotesErrorOnNonRepo verifies ListCLIRemotes surfaces an error
// (rather than empty success) when the directory is not a dolt repository, and
// that FindCLIRemote maps that error to "" (its documented not-found contract).
func TestListCLIRemotesErrorOnNonRepo(t *testing.T) {
	requireDolt(t)
	dir := t.TempDir()

	if _, err := ListCLIRemotes(dir); err == nil {
		t.Fatal("ListCLIRemotes on non-dolt dir = nil error, want error")
	}
	if got := FindCLIRemote(dir, "origin"); got != "" {
		t.Fatalf("FindCLIRemote on non-dolt dir = %q, want empty", got)
	}
}
