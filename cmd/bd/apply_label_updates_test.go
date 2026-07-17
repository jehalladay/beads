package main

import (
	"context"
	"sort"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// labelDiffFakeStore records AddLabel/RemoveLabel calls and serves GetLabels
// from a seeded set. It embeds storage.DoltStorage so it satisfies the full
// interface; only the three label methods applyLabelUpdates touches are
// implemented (any other call would nil-panic, which the tests never trigger).
type labelDiffFakeStore struct {
	storage.DoltStorage
	current []string
	added   []string
	removed []string
}

func (f *labelDiffFakeStore) GetLabels(_ context.Context, _ string) ([]string, error) {
	return f.current, nil
}

func (f *labelDiffFakeStore) AddLabel(_ context.Context, _, label, _ string) error {
	f.added = append(f.added, label)
	return nil
}

func (f *labelDiffFakeStore) RemoveLabel(_ context.Context, _, label, _ string) error {
	f.removed = append(f.removed, label)
	return nil
}

// TestApplyLabelUpdatesSetLabelsDiffsInsteadOfChurning guards beads-hu8z: a
// --set-labels that overlaps the current set must remove only current-not-desired
// and add only desired-not-present, NOT remove-all-then-add-all (which churned
// unchanged labels — spurious history — and risked a partial-failure broken set).
func TestApplyLabelUpdatesSetLabelsDiffsInsteadOfChurning(t *testing.T) {
	ctx := context.Background()

	t.Run("overlap only touches the delta", func(t *testing.T) {
		f := &labelDiffFakeStore{current: []string{"a", "b"}}
		// {a,b} -> {b,c}: remove only a, add only c; b is untouched.
		if err := applyLabelUpdates(ctx, f, "bd-1", "tester", []string{"b", "c"}, nil, nil); err != nil {
			t.Fatalf("applyLabelUpdates: %v", err)
		}
		if got := sorted(f.removed); len(got) != 1 || got[0] != "a" {
			t.Errorf("removed = %v, want [a] (only current-not-desired)", f.removed)
		}
		if got := sorted(f.added); len(got) != 1 || got[0] != "c" {
			t.Errorf("added = %v, want [c] (only desired-not-present)", f.added)
		}
	})

	t.Run("identical set is a no-op", func(t *testing.T) {
		f := &labelDiffFakeStore{current: []string{"a", "b"}}
		if err := applyLabelUpdates(ctx, f, "bd-1", "tester", []string{"a", "b"}, nil, nil); err != nil {
			t.Fatalf("applyLabelUpdates: %v", err)
		}
		if len(f.removed) != 0 || len(f.added) != 0 {
			t.Errorf("identical set churned labels: removed=%v added=%v", f.removed, f.added)
		}
	})

	t.Run("disjoint set removes all old and adds all new", func(t *testing.T) {
		f := &labelDiffFakeStore{current: []string{"a", "b"}}
		if err := applyLabelUpdates(ctx, f, "bd-1", "tester", []string{"c", "d"}, nil, nil); err != nil {
			t.Fatalf("applyLabelUpdates: %v", err)
		}
		if got := sorted(f.removed); len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("removed = %v, want [a b]", f.removed)
		}
		if got := sorted(f.added); len(got) != 2 || got[0] != "c" || got[1] != "d" {
			t.Errorf("added = %v, want [c d]", f.added)
		}
	})
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
