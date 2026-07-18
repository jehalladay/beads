package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestValidateSortField pins beads-a9rk: an unrecognized --sort field must be
// rejected (not silently fall back to priority order), while every documented
// field and the empty default are accepted.
func TestValidateSortField(t *testing.T) {
	valid := []string{"", "priority", "created", "updated", "closed", "status", "id", "title", "type", "assignee"}
	for _, f := range valid {
		if err := validateSortField(f); err != nil {
			t.Errorf("validateSortField(%q) = %v, want nil (documented field)", f, err)
		}
	}
	invalid := []string{"banana", "PRIORITY", "Created", "titlex", "prio"}
	for _, f := range invalid {
		if err := validateSortField(f); err == nil {
			t.Errorf("validateSortField(%q) = nil, want an error (unknown field silently sorts by priority otherwise)", f)
		}
	}
}

// TestValidateSortFromCmd exercises the cobra-flag convenience form.
func TestValidateSortFromCmd(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{Use: "q"}
		c.Flags().String("sort", "", "")
		return c
	}
	// unset --sort => valid (default order)
	if err := validateSortFromCmd(newCmd()); err != nil {
		t.Errorf("unset --sort should be valid, got %v", err)
	}
	// explicit valid
	c := newCmd()
	_ = c.Flags().Set("sort", "created")
	if err := validateSortFromCmd(c); err != nil {
		t.Errorf("--sort created should be valid, got %v", err)
	}
	// explicit invalid
	c = newCmd()
	_ = c.Flags().Set("sort", "banana")
	if err := validateSortFromCmd(c); err == nil {
		t.Error("--sort banana should error")
	}
}
