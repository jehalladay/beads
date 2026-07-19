package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-71j1: the --type rejection message on count/search/lint hardcoded a
// stale 6-type list (bug,feature,task,epic,chore,decision) that omitted
// spike/story/milestone — all of which the gate accepts and bd create/bd types
// list. The fix routes those messages through types.ValidWorkTypesString(),
// the same source the create path uses. This test pins that the shared helper
// carries the previously-omitted built-ins, so the count/search/lint messages
// (now built from it) can no longer drift stale.
func TestValidWorkTypesStringIncludesNewerBuiltins(t *testing.T) {
	t.Parallel()

	got := types.ValidWorkTypesString()
	for _, want := range []string{"spike", "story", "milestone", "bug", "feature", "task", "epic", "chore", "decision"} {
		if !strings.Contains(got, want) {
			t.Errorf("ValidWorkTypesString() = %q, must contain %q (else count/search/lint --type errors go stale again)", got, want)
		}
	}
}
