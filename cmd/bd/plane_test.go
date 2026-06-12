package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestPlaneCommandWiring(t *testing.T) {
	if planeCmd.Use != "plane" {
		t.Errorf("Use = %q", planeCmd.Use)
	}
	subs := map[string]bool{}
	for _, c := range planeCmd.Commands() {
		subs[c.Name()] = true
	}
	for _, want := range []string{"sync", "status"} {
		if !subs[want] {
			t.Errorf("plane command missing %q subcommand", want)
		}
	}
	for _, flag := range []string{"pull", "push", "dry-run", "prefer-local", "prefer-plane", "create-only", "state"} {
		if planeSyncCmd.Flags().Lookup(flag) == nil {
			t.Errorf("plane sync missing --%s flag", flag)
		}
	}
}

func TestPlaneStatusCounts(t *testing.T) {
	ref := func(s string) *string { return &s }
	issues := []*types.Issue{
		{ID: "bd-1", ExternalRef: ref("https://plane.example.com/acme/projects/11111111-2222-3333-4444-555555555555/issues/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")},
		{ID: "bd-2", ExternalRef: ref("https://linear.app/team/issue/T-9")},
		{ID: "bd-3"},
		{ID: "bd-4", ExternalRef: ref("plane:acme/11111111-2222-3333-4444-555555555555/bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee")},
	}

	withRef, pending := planeStatusCounts(issues)
	if withRef != 2 {
		t.Errorf("withRef = %d, want 2", withRef)
	}
	// bd-3 has no external ref at all -> pending push candidate;
	// bd-2 belongs to another tracker -> NOT pending for plane.
	if pending != 1 {
		t.Errorf("pending = %d, want 1", pending)
	}
}
