package versioncontrolops

import "testing"

// TestAssertSafeIdentifier guards the beads-s4i defense-in-depth check: every
// table name interpolated into merge/settle SQL (executed on a
// MultiStatements=true connection) must be a bare [A-Za-z0-9_]+ identifier.
// The real allowlists (the conflict switch, fkCascadeRepairDeletes) already
// guarantee this; the guard exists so a future allowlist edit that admits a
// crafted name is rejected at the interpolation site instead of forming a
// multi-statement injection.
func TestAssertSafeIdentifier(t *testing.T) {
	valid := []string{
		"metadata", "dependencies", "schema_migrations", "config",
		"labels", "comments", "events", "issue_snapshots",
		"compaction_snapshots", "child_counters", "abc123", "A_B_9",
	}
	for _, name := range valid {
		if err := assertSafeIdentifier(name); err != nil {
			t.Errorf("assertSafeIdentifier(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",                            // empty
		"a b",                         // space
		"a-b",                         // hyphen (invalid identifier char)
		"a'b",                         // single quote — quote breakout
		`a\b`,                         // backslash — escape breakout
		"issues; DROP DATABASE x",     // statement separator
		"issues) --",                  // comment / paren
		"dolt_constraint_violations`", // backtick
		"café",                        // non-ASCII
		"a.b",                         // qualified name
	}
	for _, name := range invalid {
		if err := assertSafeIdentifier(name); err == nil {
			t.Errorf("assertSafeIdentifier(%q) = nil, want error", name)
		}
	}
}
