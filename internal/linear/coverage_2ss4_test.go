package linear

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// --- fieldmapper method-level type-assertion fallbacks --------------------

func TestFieldMapperPriorityToBeadsNonIntFallback(t *testing.T) {
	m := &linearFieldMapper{config: DefaultMappingConfig()}
	// A non-int tracker priority hits the default (Medium/2) branch.
	if got := m.PriorityToBeads("urgent"); got != 2 {
		t.Errorf("PriorityToBeads(non-int) = %d, want 2", got)
	}
	if got := m.PriorityToBeads(nil); got != 2 {
		t.Errorf("PriorityToBeads(nil) = %d, want 2", got)
	}
}

func TestFieldMapperStatusToBeadsNonStateFallback(t *testing.T) {
	m := &linearFieldMapper{config: DefaultMappingConfig()}
	// A non-*State tracker state hits the StatusOpen default branch.
	if got := m.StatusToBeads("started"); got != types.StatusOpen {
		t.Errorf("StatusToBeads(non-state) = %q, want %q", got, types.StatusOpen)
	}
	if got := m.StatusToBeads(nil); got != types.StatusOpen {
		t.Errorf("StatusToBeads(nil) = %q, want %q", got, types.StatusOpen)
	}
}

// --- mapping.go PushFieldsEqual false-branch coverage ---------------------

func TestPushFieldsEqualFalseBranches(t *testing.T) {
	config := DefaultMappingConfig()
	base := func() (*types.Issue, *Issue) {
		local := &types.Issue{
			Title:       "Ship the fix",
			Description: "Main body",
			Status:      types.StatusInProgress,
			Priority:    1,
		}
		remote := &Issue{
			Title:       "Ship the fix",
			Description: "Main body",
			Priority:    PriorityToLinear(1, config),
			State:       &State{Type: "started", Name: "In Progress"},
		}
		return local, remote
	}

	// Sanity: the base pair is equal.
	if l, r := base(); !PushFieldsEqual(l, r, config) {
		t.Fatal("base pair should compare equal")
	}

	// nil local / nil remote.
	if l, _ := base(); PushFieldsEqual(nil, nil, config) {
		t.Error("nil inputs should not be equal")
	} else if PushFieldsEqual(l, nil, config) {
		t.Error("nil remote should not be equal")
	}
	if _, r := base(); PushFieldsEqual(nil, r, config) {
		t.Error("nil local should not be equal")
	}

	// Title mismatch.
	if l, r := base(); func() bool { r.Title = "Different"; return PushFieldsEqual(l, r, config) }() {
		t.Error("title mismatch should not be equal")
	}

	// Description mismatch.
	if l, r := base(); func() bool { r.Description = "Changed body"; return PushFieldsEqual(l, r, config) }() {
		t.Error("description mismatch should not be equal")
	}

	// Priority mismatch.
	if l, r := base(); func() bool { r.Priority = PriorityToLinear(4, config); return PushFieldsEqual(l, r, config) }() {
		t.Error("priority mismatch should not be equal")
	}

	// Status mismatch (remote state maps to a different beads status).
	if l, r := base(); func() bool { r.State = &State{Type: "completed", Name: "Done"}; return PushFieldsEqual(l, r, config) }() {
		t.Error("status mismatch should not be equal")
	}
}

func TestPushFieldsEqualToBeadsFalseBranches(t *testing.T) {
	base := func() (*types.Issue, *types.Issue) {
		local := &types.Issue{
			Title:       "Ship the fix",
			Description: "Main body",
			Status:      types.StatusInProgress,
			Priority:    1,
		}
		remote := &types.Issue{
			Title:       "Ship the fix",
			Description: "Main body",
			Status:      types.StatusInProgress,
			Priority:    1,
		}
		return local, remote
	}

	if l, r := base(); !PushFieldsEqualToBeads(l, r) {
		t.Fatal("base beads pair should compare equal")
	}

	if PushFieldsEqualToBeads(nil, nil) {
		t.Error("nil inputs should not be equal")
	}
	if l, _ := base(); PushFieldsEqualToBeads(l, nil) {
		t.Error("nil remote should not be equal")
	}
	if _, r := base(); PushFieldsEqualToBeads(nil, r) {
		t.Error("nil local should not be equal")
	}

	if l, r := base(); func() bool { r.Title = "Different"; return PushFieldsEqualToBeads(l, r) }() {
		t.Error("title mismatch should not be equal")
	}
	if l, r := base(); func() bool { r.Description = "Changed"; return PushFieldsEqualToBeads(l, r) }() {
		t.Error("description mismatch should not be equal")
	}
	if l, r := base(); func() bool { r.Priority = 4; return PushFieldsEqualToBeads(l, r) }() {
		t.Error("priority mismatch should not be equal")
	}
	if l, r := base(); func() bool { r.Status = types.StatusClosed; return PushFieldsEqualToBeads(l, r) }() {
		t.Error("status mismatch should not be equal")
	}
}
