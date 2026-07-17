package git

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/beads/internal/storage/domain"
)

// GetConfig with an empty key hits the validation guard before shelling out.
func (s *testSuite) TestGetConfig_EmptyKeyErrors() {
	s.gitInit()
	_, found, err := s.repo.GetConfig(s.Ctx(), "")
	s.Require().Error(err)
	s.False(found)
}

// A config key set to an empty value yields found=false (value trims to "").
func (s *testSuite) TestGetConfig_EmptyValueNotFound() {
	s.gitInit()
	s.run("git", "config", "beads.blank", "")
	value, found, err := s.repo.GetConfig(s.Ctx(), "beads.blank")
	s.Require().NoError(err)
	s.False(found)
	s.Empty(value)
}

// GetRemoteURL with an empty name hits the validation guard.
func (s *testSuite) TestGetRemoteURL_EmptyNameErrors() {
	s.gitInit()
	_, found, err := s.repo.GetRemoteURL(s.Ctx(), "")
	s.Require().Error(err)
	s.False(found)
}

// BranchHasUpstream with an empty branch name hits the validation guard.
func (s *testSuite) TestBranchHasUpstream_EmptyBranchErrors() {
	s.gitInit()
	_, err := s.repo.BranchHasUpstream(s.Ctx(), "")
	s.Require().Error(err)
}

// Only the remote half of the upstream config is set — the merge lookup fails,
// exercising the second early-return branch (false, not an error).
func (s *testSuite) TestBranchHasUpstream_RemoteSetMergeUnset() {
	s.gitInit()
	s.writeFile("a.txt", "x")
	s.run("git", "add", "a.txt")
	s.run("git", "commit", "-q", "-m", "init")

	branch, err := s.repo.CurrentBranch(s.Ctx())
	s.Require().NoError(err)
	s.Require().NotEmpty(branch)
	// Set remote but deliberately NOT merge.
	s.run("git", "config", "branch."+branch+".remote", "origin")

	has, err := s.repo.BranchHasUpstream(s.Ctx(), branch)
	s.Require().NoError(err)
	s.False(has)
}

// A detached HEAD makes `symbolic-ref --short HEAD` exit non-zero; CurrentBranch
// swallows the ExitError and returns an empty string with no error.
func (s *testSuite) TestCurrentBranch_DetachedHeadReturnsEmpty() {
	s.gitInit()
	s.writeFile("a.txt", "x")
	s.run("git", "add", "a.txt")
	s.run("git", "commit", "-q", "-m", "init")

	// Detach HEAD at the current commit.
	s.run("git", "checkout", "-q", "--detach", "HEAD")

	branch, err := s.repo.CurrentBranch(s.Ctx())
	s.Require().NoError(err)
	s.Empty(branch)
}

// Outside any repo, `git remote` exits non-zero; ListRemoteNames returns nil,nil.
func (s *testSuite) TestListRemoteNames_OutsideRepoNil() {
	// No gitInit() — tmpDir is not a repo.
	names, err := s.repo.ListRemoteNames(s.Ctx())
	s.Require().NoError(err)
	s.Nil(names)
}

// A bare repo reports true from `rev-parse --is-bare-repository`.
func (s *testSuite) TestIsBareGitRepo_TrueForBareRepo() {
	s.run("git", "init", "-q", "--bare")
	isBare, err := s.repo.IsBareGitRepo(s.Ctx())
	s.Require().NoError(err)
	s.True(isBare)
}

// Commit with an empty message hits the validation guard before shelling out.
func (s *testSuite) TestCommit_EmptyMessageErrors() {
	s.gitInit()
	_, err := s.repo.Commit(s.Ctx(), domain.GitCommitParams{Message: ""})
	s.Require().Error(err)
}

// A failing commit (staged conflict-free but a rejecting pre-commit hook, no
// --no-verify) surfaces the error and returns the hook output verbatim.
func (s *testSuite) TestCommit_HookFailureReturnsError() {
	s.gitInit()
	hookPath := filepath.Join(s.tmpDir, ".git", "hooks", "pre-commit")
	s.Require().NoError(os.WriteFile(hookPath, []byte("#!/bin/sh\necho blocked\nexit 1\n"), 0o755)) //nolint:gosec // test hook
	s.writeFile("a.txt", "x")
	s.Require().NoError(s.repo.Add(s.Ctx(), "a.txt"))

	result, err := s.repo.Commit(s.Ctx(), domain.GitCommitParams{Message: "blocked"})
	s.Require().Error(err)
	s.False(result.DidCommit)
	s.True(strings.Contains(string(result.Output), "blocked"))
}
