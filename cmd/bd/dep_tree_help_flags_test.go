package main

import (
	"regexp"
	"testing"
)

// TestDepTreeHelpFlagsExist guards that every long flag referenced in the
// `bd dep tree` help/Long text (e.g. "--max-depth=3") is actually a registered
// flag on depTreeCmd. beads-bv5p: the help advertised "--depth=3" while the
// registered flag is "--max-depth", so the documented copy-paste example failed
// with "unknown flag: --depth". A doc string that names a nonexistent flag is a
// silent friction bug; this test fails loudly if it recurs.
func TestDepTreeHelpFlagsExist(t *testing.T) {
	flagRef := regexp.MustCompile(`--([a-z][a-z0-9-]*)`)
	for _, m := range flagRef.FindAllStringSubmatch(depTreeCmd.Long, -1) {
		name := m[1]
		if depTreeCmd.Flags().Lookup(name) == nil && depTreeCmd.InheritedFlags().Lookup(name) == nil && depTreeCmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("dep tree help references --%s but no such flag is registered on depTreeCmd", name)
		}
	}
}
