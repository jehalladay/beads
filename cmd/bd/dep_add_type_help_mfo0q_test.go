package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDepAddTypeHelpNoCustomClaim_mfo0q guards beads-mfo0q: the `bd dep add
// --type` flag help must NOT advertise "custom types are also accepted".
// Dependency types are a CLOSED enum — every dep-add path gates on
// IsWellKnown() (cmd/bd/dep.go) and rejects anything else with "unknown
// dependency type", and internal/types/types.go documents that user-facing
// commands "intentionally reject custom dependency types" (there is no
// types.custom path for dep types the way there is for issue types). The old
// help string claimed custom dep types were accepted, so a user following the
// help hit a hard "unknown dependency type" error — a help-vs-behavior doc
// drift. This test fails loudly if that false claim reappears.
func TestDepAddTypeHelpNoCustomClaim_mfo0q(t *testing.T) {
	f := depAddCmd.Flags().Lookup("type")
	if f == nil {
		t.Fatal("dep add has no --type flag")
	}
	usage := f.Usage

	// The core regression: the help must not claim custom dep types are accepted.
	lower := strings.ToLower(usage)
	if strings.Contains(lower, "custom type") {
		t.Errorf("dep add --type help still claims custom types are accepted (dep types are a closed enum that rejects them, beads-mfo0q):\n%q", usage)
	}

	// The help enumerates the closed enum, so it must list EVERY well-known
	// dependency type — a new type added to WellKnownDependencyTypes() that the
	// help omits is the same help-vs-behavior drift in the other direction (the
	// user can't discover a type the command actually accepts). This also guards
	// the original beads-mfo0q help, which listed only 10 of the 19 well-known
	// types.
	for _, wk := range types.WellKnownDependencyTypes() {
		if !strings.Contains(usage, string(wk)) {
			t.Errorf("dep add --type help omits the well-known type %q (closed enum must be fully enumerated, beads-mfo0q):\n%q", wk, usage)
		}
	}
}
