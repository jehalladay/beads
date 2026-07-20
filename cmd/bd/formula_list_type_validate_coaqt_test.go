package main

import (
	"strings"
	"testing"
)

// beads-coaqt: `bd formula list --type` accepted an unrecognized filter value
// (a typo like "expansions", or a bare "workflows") and silently matched zero
// formulas — the exact-match filter in runFormulaList printed "No formulas
// found." with exit 0, a false all-clear. validateFormulaTypeFilter rejects the
// bad value up front. Empty stays "all". Pure validator — no cgo, no I/O.
func TestValidateFormulaTypeFilter_coaqt(t *testing.T) {
	valid := []string{"", "workflow", "expansion", "aspect", "convoy"}
	for _, v := range valid {
		if err := validateFormulaTypeFilter(v); err != nil {
			t.Errorf("validateFormulaTypeFilter(%q) = %v, want nil (recognized/empty)", v, err)
		}
	}

	invalid := []string{
		"expansions", // typo — the trailing-s trap that a silent match hides
		"workflows",
		"aspects",
		"convoys",
		"Workflow", // case-sensitive: the canonical set is lower-case
		"macro",    // plausible-but-wrong synonym
		"bug",      // an issue type, not a formula type
		"garbage",
	}
	for _, v := range invalid {
		err := validateFormulaTypeFilter(v)
		if err == nil {
			t.Errorf("validateFormulaTypeFilter(%q) = nil, want rejection (unrecognized filter must not silently match zero)", v)
			continue
		}
		if !strings.Contains(err.Error(), "invalid formula type filter") {
			t.Errorf("validateFormulaTypeFilter(%q) error = %q, want it to mention 'invalid formula type filter'", v, err.Error())
		}
	}
}
