package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestLabelCasingCoherence_9jjj8 (beads-9jjj8) proves the label subsystem
// agrees with itself on casing across add / query / remove.
//
// Before the fix the three verbs diverged: ADD (write) stored verbatim
// case-SENSITIVELY (so 'FOO' and 'foo' could coexist), QUERY folded
// case-INSENSITIVELY (LOWER(label)=LOWER(?)), and REMOVE was case-EXACT. The
// sharp trap: an issue labelled 'FOO' matched `--label foo` (query folds) yet
// `label remove foo` errored/no-op'd because the DELETE was case-exact — the
// remove verb could not remove what the query surfaced. Fix (option a):
// case-fold labels at every WRITE chokepoint (AddLabelInTx / PersistLabels /
// SetLabelsInTx-desired) to match the case-insensitive query, and fold REMOVE
// so it clears legacy mixed-case rows too.
//
// Drives a real store so the ON-DISK casing + the LOWER() query/DELETE SQL are
// validated for real. MUTATION-VERIFY: drop `label = strings.ToLower(label)`
// from AddLabelInTx and "stored_lower"/"query_finds_mixedcase_add" go RED;
// revert RemoveLabelInTx's LOWER()/fold and "remove_is_case_insensitive" goes
// RED.
func TestLabelCasingCoherence_9jjj8(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	mk := func(title string) string {
		iss := &types.Issue{Title: title, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		return iss.ID
	}

	t.Run("add_folds_stored_lower", func(t *testing.T) {
		id := mk("mixed-add")
		if err := store.AddLabel(ctx, id, "FOO", "tester"); err != nil {
			t.Fatalf("AddLabel FOO: %v", err)
		}
		got, err := store.GetLabels(ctx, id)
		if err != nil {
			t.Fatalf("GetLabels: %v", err)
		}
		if len(got) != 1 || got[0] != "foo" {
			t.Errorf("REGRESSION (beads-9jjj8): AddLabel(\"FOO\") stored %v, want [\"foo\"] (write must case-fold to match the case-insensitive query)", got)
		}
	})

	t.Run("no_coexisting_case_variants", func(t *testing.T) {
		id := mk("dup-case")
		if err := store.AddLabel(ctx, id, "Bar", "tester"); err != nil {
			t.Fatalf("AddLabel Bar: %v", err)
		}
		if err := store.AddLabel(ctx, id, "BAR", "tester"); err != nil {
			t.Fatalf("AddLabel BAR: %v", err)
		}
		got, err := store.GetLabels(ctx, id)
		if err != nil {
			t.Fatalf("GetLabels: %v", err)
		}
		if len(got) != 1 || got[0] != "bar" {
			t.Errorf("REGRESSION (beads-9jjj8): 'Bar' then 'BAR' produced %v, want a single [\"bar\"] (case variants must not coexist)", got)
		}
	})

	t.Run("query_finds_mixedcase_add", func(t *testing.T) {
		id := mk("query-fold")
		if err := store.AddLabel(ctx, id, "Baz", "tester"); err != nil {
			t.Fatalf("AddLabel Baz: %v", err)
		}
		// query by a DIFFERENT casing than supplied — must still match.
		got, err := store.GetIssuesByLabel(ctx, "BAZ")
		if err != nil {
			t.Fatalf("GetIssuesByLabel: %v", err)
		}
		found := false
		for _, iss := range got {
			if iss.ID == id {
				found = true
			}
		}
		if !found {
			t.Errorf("REGRESSION (beads-9jjj8): GetIssuesByLabel(\"BAZ\") did not find issue labelled via AddLabel(\"Baz\")")
		}
	})

	t.Run("remove_is_case_insensitive", func(t *testing.T) {
		// The sharp trap: label the issue, then remove with a DIFFERENT casing
		// than the query would surface — must succeed and clear the label.
		id := mk("remove-trap")
		if err := store.AddLabel(ctx, id, "Qux", "tester"); err != nil {
			t.Fatalf("AddLabel Qux: %v", err)
		}
		if err := store.RemoveLabel(ctx, id, "QUX", "tester"); err != nil {
			t.Fatalf("RemoveLabel QUX (case-insensitive remove): %v", err)
		}
		got, err := store.GetLabels(ctx, id)
		if err != nil {
			t.Fatalf("GetLabels after remove: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("REGRESSION (beads-9jjj8): RemoveLabel(\"QUX\") left %v — remove could not clear a label a differently-cased query surfaces (find-then-cannot-remove trap)", got)
		}
	})

	t.Run("create_time_labels_fold", func(t *testing.T) {
		iss := &types.Issue{Title: "create-fold", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Labels: []string{"Frontend"}}
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("create with label: %v", err)
		}
		got, err := store.GetLabels(ctx, iss.ID)
		if err != nil {
			t.Fatalf("GetLabels: %v", err)
		}
		if len(got) != 1 || got[0] != "frontend" {
			t.Errorf("REGRESSION (beads-9jjj8): create -l \"Frontend\" stored %v, want [\"frontend\"] (create path must fold like AddLabel)", got)
		}
	})
}
