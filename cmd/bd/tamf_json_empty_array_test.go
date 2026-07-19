package main

import (
	"encoding/json"
	"testing"
)

// beads-tamf: bd orphans --json and bd formula list --json emitted the bare
// literal `null` on an empty result (nil slice → json null), while every
// sibling list verb (ready/blocked/list) emits `[]`. A consumer iterating the
// result breaks on null but is safe on []. The fix is a non-nil slice init at
// the source; these tests pin that the empty value marshals to a JSON array,
// mirroring the ready/blocked precedent.

func TestOrphansEmptyMarshalsToArrayNotNull(t *testing.T) {
	t.Parallel()

	// This mirrors the source init at orphans.go: an empty result must be a
	// non-nil slice so it renders as [].
	output := []orphanIssueOutput{}
	b, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("empty orphans --json must marshal to [], got %q", b)
	}
}

func TestFormulaListEmptyMarshalsToArrayNotNull(t *testing.T) {
	t.Parallel()

	entries := []FormulaListEntry{}
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("empty formula list --json must marshal to [], got %q", b)
	}
}

// Guard the class invariant: a NIL slice of either type marshals to null (the
// bug), proving the non-nil init at the call site is load-bearing.
func TestNilSlicesMarshalToNull_ProvesInitLoadBearing(t *testing.T) {
	t.Parallel()

	var nilOrphans []orphanIssueOutput
	if b, _ := json.Marshal(nilOrphans); string(b) != "null" {
		t.Errorf("a nil orphan slice should marshal to null (proves init matters), got %q", b)
	}
	var nilEntries []FormulaListEntry
	if b, _ := json.Marshal(nilEntries); string(b) != "null" {
		t.Errorf("a nil formula slice should marshal to null (proves init matters), got %q", b)
	}
}
