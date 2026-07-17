package ui

import (
	"testing"
)

// TestShouldUsePagerNonTTY covers the term.IsTerminal false branch of
// shouldUsePager — reached only when NoPager is false and BD_NO_PAGER is unset,
// which the existing TestShouldUsePager cases do not exercise (they return
// earlier). Under `go test`, os.Stdout is a pipe, not a terminal.
func TestShouldUsePagerNonTTY(t *testing.T) {
	t.Setenv("BD_NO_PAGER", "")
	if shouldUsePager(PagerOptions{NoPager: false}) {
		t.Fatal("shouldUsePager with non-TTY stdout should be false")
	}
}

// TestGetTerminalHeightNonTTY verifies getTerminalHeight returns 0 when stdout
// is not a terminal (the test-harness case). getTerminalHeight is otherwise
// uncovered.
func TestGetTerminalHeightNonTTY(t *testing.T) {
	if got := getTerminalHeight(); got != 0 {
		t.Fatalf("getTerminalHeight on non-TTY stdout = %d, want 0", got)
	}
}
