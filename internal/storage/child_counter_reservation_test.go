// Package storage — child_counter_reservation_test.go
//
// Hermetic unit tests for the pure context-carried child-counter reservation
// helpers (WithReservedChildCounter / HasReservedChildCounter). No DB — these
// only read/write a context value, so they exercise the prefix-parse and
// match logic with zero I/O.
package storage

import (
	"context"
	"testing"
)

func TestWithReservedChildCounter_RoundTrip(t *testing.T) {
	ctx := WithReservedChildCounter(context.Background(), "beads-abc", "beads-abc.3")
	if !HasReservedChildCounter(ctx, "beads-abc", 3) {
		t.Fatal("HasReservedChildCounter = false for the reserved (parent, childNum), want true")
	}
}

func TestWithReservedChildCounter_InvalidChildIDIsNoOp(t *testing.T) {
	tests := []struct {
		name     string
		parentID string
		childID  string
	}{
		{"missing parent prefix", "beads-abc", "other-xyz.3"},
		{"prefix but no dot", "beads-abc", "beads-abc3"},
		{"non-numeric suffix", "beads-abc", "beads-abc.notanum"},
		{"empty suffix", "beads-abc", "beads-abc."},
		{"parent equals child (no suffix)", "beads-abc", "beads-abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := context.Background()
			ctx := WithReservedChildCounter(base, tc.parentID, tc.childID)
			// On an invalid childID the helper returns ctx unchanged, so no
			// reservation is carried for any child number.
			if HasReservedChildCounter(ctx, tc.parentID, 3) {
				t.Fatalf("HasReservedChildCounter = true after no-op WithReservedChildCounter(%q, %q), want false",
					tc.parentID, tc.childID)
			}
		})
	}
}

func TestHasReservedChildCounter_NoReservationInContext(t *testing.T) {
	if HasReservedChildCounter(context.Background(), "beads-abc", 1) {
		t.Fatal("HasReservedChildCounter = true on a bare context, want false")
	}
}

func TestHasReservedChildCounter_Mismatch(t *testing.T) {
	ctx := WithReservedChildCounter(context.Background(), "beads-abc", "beads-abc.3")
	tests := []struct {
		name     string
		parentID string
		childNum int
	}{
		{"wrong child number", "beads-abc", 4},
		{"wrong parent", "beads-xyz", 3},
		{"both wrong", "beads-xyz", 9},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if HasReservedChildCounter(ctx, tc.parentID, tc.childNum) {
				t.Fatalf("HasReservedChildCounter(%q, %d) = true, want false", tc.parentID, tc.childNum)
			}
		})
	}
}

func TestWithReservedChildCounter_MultiDigitAndZero(t *testing.T) {
	ctx := WithReservedChildCounter(context.Background(), "p", "p.42")
	if !HasReservedChildCounter(ctx, "p", 42) {
		t.Fatal("multi-digit child number not matched")
	}
	ctx0 := WithReservedChildCounter(context.Background(), "p", "p.0")
	if !HasReservedChildCounter(ctx0, "p", 0) {
		t.Fatal("zero child number not matched")
	}
}

func TestWithReservedChildCounter_ParentWithDots(t *testing.T) {
	// A dotted parent id (nested child) must still parse the trailing number
	// off the last "<parent>." boundary.
	ctx := WithReservedChildCounter(context.Background(), "beads-abc.1", "beads-abc.1.5")
	if !HasReservedChildCounter(ctx, "beads-abc.1", 5) {
		t.Fatal("dotted parent id not matched")
	}
}
