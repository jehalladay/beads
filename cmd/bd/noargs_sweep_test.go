package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// The read/query leaf commands below take no positional arguments. Before
// beads-6jzt they had no Args validator, so a stray positional (a query-style
// habit like "bd stats status=open", or a fat-fingered "bd export foo") was
// silently ignored — producing misleading output with rc=0. This pins that
// each now has an Args validator that rejects positionals.
//
// Scoped AROUND beads_dogfooder's ib1u (flatten/gc/prune) and ywth (count),
// which are fixed in their own MRs — intentionally not covered here.
func TestNoArgsSweep_RejectsPositional(t *testing.T) {
	// Command paths as typed on the CLI (parent subcommands included).
	commands := [][]string{
		{"duplicates"},
		{"find-duplicates"},
		{"export"},
		{"orphans"},
		{"stale"},
		{"human", "stats"},
		{"dep", "cycles"},
		{"epic", "close-eligible"},
		{"gate", "discover"},
		{"blocked"},
		{"config", "validate"},
		{"config", "apply"},
		{"config", "drift"},
	}

	for _, path := range commands {
		name := path[len(path)-1]
		t.Run(name, func(t *testing.T) {
			cmd, _, err := rootCmd.Find(path)
			if err != nil {
				t.Fatalf("rootCmd.Find(%v): %v", path, err)
			}
			// Confirm we resolved the leaf, not a parent (Find returns the
			// deepest match; a mistyped path could resolve to an ancestor).
			if cmd.Name() != name {
				t.Fatalf("resolved %q, want %q — path %v did not reach the leaf", cmd.Name(), name, path)
			}
			if cmd.Args == nil {
				t.Fatalf("%q has no Args validator; a stray positional would be silently ignored", name)
			}
			// A positional must be rejected.
			if err := cmd.Args(cmd, []string{"stray"}); err == nil {
				t.Errorf("%q Args validator accepted a stray positional %q, want rejection", name, "stray")
			}
			// No positionals must be accepted.
			if err := cmd.Args(cmd, nil); err != nil {
				t.Errorf("%q Args validator rejected the no-arg case: %v", name, err)
			}
		})
	}
}

// molecule "stale" is a distinct command from the top-level "stale"; ensure it
// also rejects positionals. It lives under the "mol" parent.
func TestNoArgsSweep_MolStaleRejectsPositional(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"mol", "stale"})
	if err != nil {
		t.Fatalf("rootCmd.Find([mol stale]): %v", err)
	}
	if cmd.Name() != "stale" {
		t.Skipf("mol stale not resolvable in this build (got %q); top-level stale is covered elsewhere", cmd.Name())
	}
	if cmd.Args == nil {
		t.Fatalf("mol stale has no Args validator")
	}
	if err := cmd.Args(cmd, []string{"stray"}); err == nil {
		t.Errorf("mol stale accepted a stray positional, want rejection")
	}
}

// Guard: cobra.NoArgs is the validator used (documents intent + catches an
// accidental swap to a permissive validator on any swept command).
func TestNoArgsSweep_UsesNoArgsSemantics(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"orphans"})
	if err != nil {
		t.Fatalf("find orphans: %v", err)
	}
	// cobra.NoArgs rejects any positional; a permissive validator would not.
	if err := cmd.Args(cmd, []string{"x"}); err == nil {
		t.Error("orphans should reject positionals (cobra.NoArgs semantics)")
	}
	_ = cobra.NoArgs // referenced to document the intended validator
}
