package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-wijd: hermetic test for closeProxiedCommitMessage (close_proxied_server.go),
// a pure commit-message builder (verified 0% + no test refs).

func TestCloseProxiedCommitMessage(t *testing.T) {
	outcomes := []closeProxiedOutcome{{id: "bd-1"}, {id: "bd-2"}}

	t.Run("ids only", func(t *testing.T) {
		got := closeProxiedCommitMessage(outcomes, nil, nil)
		if got != "bd: close bd-1, bd-2" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("with auto-advance appends next step", func(t *testing.T) {
		cont := &ContinueResult{AutoAdvanced: true, NextStep: &types.Issue{ID: "bd-next"}}
		got := closeProxiedCommitMessage(outcomes, nil, cont)
		if !strings.Contains(got, "bd: close bd-1, bd-2") || !strings.Contains(got, "advance to bd-next") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("auto-advance without a next step does not append", func(t *testing.T) {
		cont := &ContinueResult{AutoAdvanced: true, NextStep: nil}
		got := closeProxiedCommitMessage(outcomes, nil, cont)
		if strings.Contains(got, "advance to") {
			t.Errorf("should not append advance when NextStep is nil, got %q", got)
		}
	})

	t.Run("not-auto-advanced does not append even with a next step", func(t *testing.T) {
		cont := &ContinueResult{AutoAdvanced: false, NextStep: &types.Issue{ID: "bd-next"}}
		got := closeProxiedCommitMessage(outcomes, nil, cont)
		if strings.Contains(got, "advance to") {
			t.Errorf("should not append advance when AutoAdvanced is false, got %q", got)
		}
	})

	t.Run("with claimed issue appends claim", func(t *testing.T) {
		got := closeProxiedCommitMessage(outcomes, &types.Issue{ID: "bd-claim"}, nil)
		if !strings.Contains(got, "claim bd-claim") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("advance and claim both appended", func(t *testing.T) {
		cont := &ContinueResult{AutoAdvanced: true, NextStep: &types.Issue{ID: "bd-next"}}
		got := closeProxiedCommitMessage(outcomes, &types.Issue{ID: "bd-claim"}, cont)
		if !strings.Contains(got, "advance to bd-next") || !strings.Contains(got, "claim bd-claim") {
			t.Errorf("expected both advance and claim, got %q", got)
		}
	})

	t.Run("empty outcomes", func(t *testing.T) {
		got := closeProxiedCommitMessage(nil, nil, nil)
		if got != "bd: close " {
			t.Errorf("got %q, want 'bd: close '", got)
		}
	})
}
