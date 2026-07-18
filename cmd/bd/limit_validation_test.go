package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestValidateLimitFlag pins beads-eqi4: a NEGATIVE --limit must be rejected,
// while 0 (the documented "unlimited" sentinel) and positive values pass. The
// guard only fires when the flag was explicitly set (changed), so an unset
// default is never rejected. This is the shared check the sibling read commands
// (ready/search/query/gate/find-duplicates/mol current) route through so a
// negative --limit can't silently unbound the result set on any of them (the
// SQL builders apply filter.Limit only when >0).
func TestValidateLimitFlag(t *testing.T) {
	cases := []struct {
		name    string
		limit   int
		changed bool
		wantErr bool
	}{
		{"negative changed rejected (the bug)", -1, true, true},
		{"large negative changed rejected", -1000, true, true},
		{"zero changed is the unlimited sentinel", 0, true, false},
		{"positive changed valid", 25, true, false},
		// An unset flag (changed=false) is never validated, even if the default
		// were somehow negative — matches list's limitChanged gate.
		{"negative but unchanged is ignored", -1, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLimitFlag(tc.limit, tc.changed)
			if tc.wantErr && err == nil {
				t.Errorf("validateLimitFlag(%d, %v) = nil, want an error (negative --limit silently unbounds)", tc.limit, tc.changed)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateLimitFlag(%d, %v) = %v, want nil", tc.limit, tc.changed, err)
			}
		})
	}
}

// TestValidateLimitFromCmd exercises the cobra-flag convenience form the sibling
// read commands call: it must read --limit + its changed state and reject a
// user-supplied negative, while leaving an unset --limit (default) alone.
func TestValidateLimitFromCmd(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{Use: "x"}
		c.Flags().IntP("limit", "n", 50, "")
		return c
	}

	// Unset --limit: no error even though a hypothetical default could be
	// anything — the guard only fires on an explicitly changed negative.
	if err := validateLimitFromCmd(newCmd()); err != nil {
		t.Errorf("unset --limit should not error, got %v", err)
	}

	// Explicit negative: rejected.
	cmd := newCmd()
	if err := cmd.Flags().Set("limit", "-5"); err != nil {
		t.Fatalf("set --limit: %v", err)
	}
	if err := validateLimitFromCmd(cmd); err == nil {
		t.Error("validateLimitFromCmd with --limit -5 = nil, want an error")
	}

	// Explicit 0 (unlimited sentinel) and positive: accepted.
	for _, v := range []string{"0", "10"} {
		c := newCmd()
		if err := c.Flags().Set("limit", v); err != nil {
			t.Fatalf("set --limit %s: %v", v, err)
		}
		if err := validateLimitFromCmd(c); err != nil {
			t.Errorf("validateLimitFromCmd with --limit %s = %v, want nil", v, err)
		}
	}
}
