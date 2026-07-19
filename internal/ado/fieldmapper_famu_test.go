package ado

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-famu: ado StatusToBeads + TypeToBeads reverse lookups must be
// DETERMINISTIC on a non-injective map (Go map iteration order is randomized).
func TestStatusAndTypeToBeadsDeterministicOnNonInjectiveMap(t *testing.T) {
	t.Parallel()
	m := &adoFieldMapper{
		stateMap: map[string]string{
			string(types.StatusOpen):       "Active",
			string(types.StatusInProgress): "Active",
			string(types.StatusClosed):     "Done",
		},
		typeMap: map[string]string{
			string(types.TypeBug):  "Issue",
			string(types.TypeTask): "Issue",
			string(types.TypeEpic): "Epic",
		},
	}
	// Sorted beadsStatus keys: "in_progress" < "open" → in_progress wins.
	firstS := m.StatusToBeads("Active")
	if firstS != types.StatusInProgress {
		t.Fatalf("StatusToBeads(\"Active\") = %v, want deterministic %v", firstS, types.StatusInProgress)
	}
	// Sorted beadsType keys: "bug" < "task" → bug wins.
	firstT := m.TypeToBeads("Issue")
	if firstT != types.TypeBug {
		t.Fatalf("TypeToBeads(\"Issue\") = %v, want deterministic %v", firstT, types.TypeBug)
	}
	for i := 0; i < 200; i++ {
		if got := m.StatusToBeads("Active"); got != firstS {
			t.Fatalf("StatusToBeads non-deterministic: iter %d got %v", i, got)
		}
		if got := m.TypeToBeads("Issue"); got != firstT {
			t.Fatalf("TypeToBeads non-deterministic: iter %d got %v", i, got)
		}
	}
}
