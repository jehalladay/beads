package ado

import "testing"

// TestTracker_SetProjectsAndProjects round-trips SetProjects/Projects, the
// pre-Init CLI-flag override for project names.
func TestTracker_SetProjectsAndProjects(t *testing.T) {
	tr := &Tracker{}
	if got := tr.Projects(); got != nil {
		t.Errorf("Projects() on fresh tracker = %v, want nil", got)
	}
	tr.SetProjects([]string{"alpha", "beta"})
	got := tr.Projects()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("Projects() = %v, want [alpha beta]", got)
	}
}

// TestTracker_ADOClient returns the underlying client pointer (nil before Init,
// the set client after).
func TestTracker_ADOClient(t *testing.T) {
	tr := &Tracker{}
	if tr.ADOClient() != nil {
		t.Error("ADOClient() on fresh tracker = non-nil, want nil")
	}
	c := &Client{}
	tr.client = c
	if tr.ADOClient() != c {
		t.Error("ADOClient() did not return the set client")
	}
}

// TestTracker_SetFilters stores pull filters for later WIQL queries.
func TestTracker_SetFilters(t *testing.T) {
	tr := &Tracker{}
	if tr.filters != nil {
		t.Error("filters on fresh tracker = non-nil, want nil")
	}
	f := &PullFilters{}
	tr.SetFilters(f)
	if tr.filters != f {
		t.Error("SetFilters did not store the filters")
	}
}
