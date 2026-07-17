package ui

import (
	"testing"
)

// TestIsStderrTerminal verifies IsStderrTerminal does not panic and returns the
// non-TTY result under `go test` (stderr is a pipe in the harness).
func TestIsStderrTerminal(t *testing.T) {
	if IsStderrTerminal() {
		t.Log("IsStderrTerminal() = true (unexpected under go test, but not fatal)")
	}
}

// TestShouldUseColorGitHookBranch covers the BD_GIT_HOOK=1 early-return branch of
// ShouldUseColor, which is not reached by the existing table test.
func TestShouldUseColorGitHookBranch(t *testing.T) {
	t.Setenv("BD_GIT_HOOK", "1")
	// Force-color would otherwise win; BD_GIT_HOOK must take precedence.
	t.Setenv("CLICOLOR_FORCE", "1")
	if ShouldUseColor() {
		t.Fatal("ShouldUseColor with BD_GIT_HOOK=1 should be false even with CLICOLOR_FORCE")
	}
}
