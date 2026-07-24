package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestPrintHumanStats(t *testing.T) {
	tests := []struct {
		name   string
		issues []*types.Issue
		done   map[string]bool
		want   humanStats
	}{
		{
			name:   "empty list",
			issues: nil,
			want:   humanStats{Total: 0, Pending: 0, Responded: 0, Dismissed: 0},
		},
		{
			name: "mixed statuses",
			issues: []*types.Issue{
				{ID: "bd-1", Status: "open"},
				{ID: "bd-2", Status: "in_progress"},
				{ID: "bd-3", Status: "blocked"},
				{ID: "bd-4", Status: "closed", CloseReason: "Responded"},
				{ID: "bd-5", Status: "closed", CloseReason: "Dismissed: not needed"},
				{ID: "bd-6", Status: "hooked"},
			},
			want: humanStats{Total: 6, Pending: 4, Responded: 1, Dismissed: 1},
		},
		{
			name: "all closed responded",
			issues: []*types.Issue{
				{ID: "bd-1", Status: "closed", CloseReason: "Responded"},
				{ID: "bd-2", Status: "closed", CloseReason: "Responded"},
			},
			want: humanStats{Total: 2, Pending: 0, Responded: 2, Dismissed: 0},
		},
		{
			name: "all dismissed",
			issues: []*types.Issue{
				{ID: "bd-1", Status: "closed", CloseReason: "Dismissed"},
				{ID: "bd-2", Status: "closed", CloseReason: "Dismissed: stale"},
			},
			want: humanStats{Total: 2, Pending: 0, Responded: 0, Dismissed: 2},
		},
		// beads-wcr98 teeth: a custom DONE-category status must count as complete
		// (responded, or dismissed if its CloseReason says so), NOT Pending. With
		// the fix reverted (literal "closed" only) bd-2 and bd-3 fall to Pending
		// and this case fails — the done-category awareness is load-bearing.
		{
			name: "custom done-category status counts as complete",
			issues: []*types.Issue{
				{ID: "bd-1", Status: "open"},
				{ID: "bd-2", Status: "resolved", CloseReason: "Responded"},
				{ID: "bd-3", Status: "resolved", CloseReason: "Dismissed: wontfix"},
				{ID: "bd-4", Status: "closed", CloseReason: "Responded"},
			},
			done: map[string]bool{"resolved": true},
			want: humanStats{Total: 4, Pending: 1, Responded: 2, Dismissed: 1},
		},
		// A custom status NOT in the done set (e.g. a frozen/parked category) stays
		// Pending — the done map must be consulted, not "any non-closed = pending"
		// blindly nor "any custom status = done".
		{
			name: "non-done custom status stays pending",
			issues: []*types.Issue{
				{ID: "bd-1", Status: "parked"},
				{ID: "bd-2", Status: "resolved", CloseReason: "Responded"},
			},
			done: map[string]bool{"resolved": true},
			want: humanStats{Total: 2, Pending: 1, Responded: 1, Dismissed: 0},
		},
		// Degraded-safe: an empty/nil done set reduces to byte-identical
		// literal-"closed" behavior (a done-category status with no config falls to
		// Pending, exactly as before the fix).
		{
			name: "nil done set falls back to literal-closed",
			issues: []*types.Issue{
				{ID: "bd-1", Status: "resolved", CloseReason: "Responded"},
				{ID: "bd-2", Status: "closed", CloseReason: "Responded"},
			},
			done: nil,
			want: humanStats{Total: 2, Pending: 1, Responded: 1, Dismissed: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeHumanStats(tt.issues, tt.done)
			if got != tt.want {
				t.Errorf("computeHumanStats() = %+v, want %+v", got, tt.want)
			}
			// Also verify the plaintext printer does not panic on the counts.
			printHumanStats(got)
		})
	}
}

func TestPrintHumanList(t *testing.T) {
	tests := []struct {
		name   string
		issues []*types.Issue
	}{
		{
			name:   "empty list",
			issues: nil,
		},
		{
			name: "single issue",
			issues: []*types.Issue{
				{ID: "bd-abc", Title: "Need human input", Status: "open", Priority: 1},
			},
		},
		{
			name: "multiple issues with varied status",
			issues: []*types.Issue{
				{ID: "bd-1", Title: "Review needed", Status: "open"},
				{ID: "bd-2", Title: "Approval required", Status: "blocked", Priority: 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify no panic
			printHumanList(tt.issues)
		})
	}
}

func TestHumanCmdSubcommands(t *testing.T) {
	// Verify all subcommands are registered
	subCmds := humanCmd.Commands()
	names := make([]string, len(subCmds))
	for i, cmd := range subCmds {
		names[i] = cmd.Name()
	}
	joined := strings.Join(names, ",")

	for _, expected := range []string{"list", "respond", "dismiss", "stats"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("missing subcommand %q in human command", expected)
		}
	}
}

func TestHumanRespondRequiresResponseFlag(t *testing.T) {
	flag := humanRespondCmd.Flags().Lookup("response")
	if flag == nil {
		t.Fatal("respond command should have --response flag")
	}
}

func TestHumanDismissHasReasonFlag(t *testing.T) {
	flag := humanDismissCmd.Flags().Lookup("reason")
	if flag == nil {
		t.Fatal("dismiss command should have --reason flag")
	}
}

func TestHumanListHasStatusFlag(t *testing.T) {
	flag := humanListCmd.Flags().Lookup("status")
	if flag == nil {
		t.Fatal("list command should have --status flag")
	}
}
