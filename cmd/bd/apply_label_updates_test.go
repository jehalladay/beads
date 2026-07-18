package main

import (
	"context"
	"sort"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// labelDiffFakeStore records the calls applyLabelUpdates makes. Since beads-idvy
// moved the set-diff into the storage layer (SetLabels → issueops.SetLabelsInTx),
// applyLabelUpdates now delegates a --set-labels to ONE SetLabels call instead of
// looping AddLabel/RemoveLabel itself; the fake records setCalls to assert that
// delegation (and still records add/remove for the --add/--remove legs). It
// embeds storage.DoltStorage so it satisfies the full interface; only the label
// methods applyLabelUpdates touches are implemented.
type labelDiffFakeStore struct {
	storage.DoltStorage
	current  []string
	added    []string
	removed  []string
	setCalls [][]string
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

func (f *labelDiffFakeStore) SetLabels(_ context.Context, _ string, labels []string, _ string) error {
	f.setCalls = append(f.setCalls, append([]string(nil), labels...))
	return nil
}

// TestApplyLabelUpdatesDelegatesSetToStorage guards beads-idvy: a --set-labels
// must be forwarded verbatim to the storage layer's atomic SetLabels (which owns
// the diff-not-churn logic in one transaction, issueops.SetLabelsInTx), NOT
// diffed at the cmd layer via a loop of AddLabel/RemoveLabel (the hu8z impl that
// churned across N transactions). The diff behavior itself is now guarded at the
// issueops level (TestSetLabelsInTx*).
func TestApplyLabelUpdatesDelegatesSetToStorage(t *testing.T) {
	ctx := context.Background()

	t.Run("set-labels forwards the desired set to SetLabels", func(t *testing.T) {
		f := &labelDiffFakeStore{current: []string{"a", "b"}}
		if err := applyLabelUpdates(ctx, f, "bd-1", "tester", []string{"b", "c"}, nil, nil); err != nil {
			t.Fatalf("applyLabelUpdates: %v", err)
		}
		if len(f.setCalls) != 1 {
			t.Fatalf("expected exactly one SetLabels call, got %d", len(f.setCalls))
		}
		if got := sorted(f.setCalls[0]); len(got) != 2 || got[0] != "b" || got[1] != "c" {
			t.Errorf("SetLabels got %v, want [b c] (forwarded verbatim)", f.setCalls[0])
		}
		// The cmd layer must NOT do its own add/remove diff loop for --set-labels.
		if len(f.added) != 0 || len(f.removed) != 0 {
			t.Errorf("cmd layer diffed instead of delegating: added=%v removed=%v", f.added, f.removed)
		}
	})

	t.Run("no set-labels means no SetLabels call", func(t *testing.T) {
		f := &labelDiffFakeStore{current: []string{"a"}}
		if err := applyLabelUpdates(ctx, f, "bd-1", "tester", nil, []string{"x"}, []string{"a"}); err != nil {
			t.Fatalf("applyLabelUpdates: %v", err)
		}
		if len(f.setCalls) != 0 {
			t.Errorf("expected no SetLabels call for add/remove-only, got %v", f.setCalls)
		}
		if got := sorted(f.added); len(got) != 1 || got[0] != "x" {
			t.Errorf("added = %v, want [x]", f.added)
		}
		if got := sorted(f.removed); len(got) != 1 || got[0] != "a" {
			t.Errorf("removed = %v, want [a]", f.removed)
		}
	})
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
