package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestLinkTypeHelpFullEnum_3no17 guards beads-3no17 (sibling of beads-mfo0q):
// the `bd link --type` flag help must enumerate the full closed dependency-type
// enum. `bd link` is shorthand for `bd dep add` and both gate on IsWellKnown()
// (cmd/bd/link.go rejects anything else with "unknown dependency type"), and
// link.go's own comment claims the help "lists exactly the well-known set".
// The old help listed only 5 of the 19 WellKnownDependencyTypes(), so a user
// following the help never discovered valid types like `duplicates` or
// `supersedes` (both accepted at runtime). This test fails loudly if the help
// omits any well-known type or reintroduces a false custom-type claim.
func TestLinkTypeHelpFullEnum_3no17(t *testing.T) {
	f := linkCmd.Flags().Lookup("type")
	if f == nil {
		t.Fatal("link has no --type flag")
	}
	usage := f.Usage

	// link accepts the closed enum, so the help must list EVERY well-known type
	// (the drift beads-3no17 fixes: only 5 of 19 were listed).
	for _, wk := range types.WellKnownDependencyTypes() {
		if !strings.Contains(usage, string(wk)) {
			t.Errorf("link --type help omits the well-known type %q (closed enum must be fully enumerated, beads-3no17):\n%q", wk, usage)
		}
	}

	// Dep types are a closed enum — the help must not claim custom types work
	// (the help-vs-behavior drift of beads-mfo0q, in the other direction).
	if strings.Contains(strings.ToLower(usage), "custom type") {
		t.Errorf("link --type help claims custom types are accepted (dep types are a closed enum that rejects them, beads-3no17):\n%q", usage)
	}
}
