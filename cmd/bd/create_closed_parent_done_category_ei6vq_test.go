//go:build cgo

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedCreateClosedParentDoneCategory_ei6vq is the beads-ei6vq teeth for
// the CREATE/authoring axis of the done-category closed-parent-with-open-child
// family (beads-4u7d). The create-time guards (create.go single `--parent`;
// create.go `--deps parent-child:`; markdown `- parent-child:`) all keyed on the
// LITERAL `parent.Status == types.StatusClosed`. A parent moved to a custom
// done-category status (`bd config set status.custom "verified:done"` →
// CategoryDone) is TERMINAL but not literally closed, so it bypassed every create
// guard — and a freshly minted child is always OPEN — silently recreating the
// forbidden "terminal parent with an open child" state that ulsg4 now flags and
// u9lkx blocks on dep-add.
//
// The fix mirrors the family: swap the literal `== StatusClosed` for the shared
// parentStatusIsTerminal(status, done) helper (close.go), with done resolved via
// doneCategoryStatusNames(ctx, store) on the direct path. An empty done-set
// reduces to byte-identical literal-'closed' behavior (degraded-safe); FROZEN
// (parked) is deliberately NOT terminal; --force still overrides.
//
// MUTATION-VERIFY: revert any guarded site to a bare `== types.StatusClosed` →
// that site's done-category subtest goes RED (the child + parent-child edge land
// at rc=0) while the literal-closed control and the FROZEN negative stay green.
//
// The subprocess (embedded-dolt) legs here cover the create.go flag axis and the
// markdown axis; the two create-FORM legs run in-process (see
// TestCreateFormClosedParentDoneCategory_ei6vq below) because
// CreateIssueFromFormValues is a library entry point, matching o8h79's model.
func TestEmbeddedCreateClosedParentDoneCategory_ei6vq(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt create tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// makeDoneParent creates an auto-closing parent of the given type, registers a
	// custom done-category status, and moves the parent into it — leaving it
	// TERMINAL (done) but NOT literally closed. It is childless when parked, so
	// the forward close-guard permits the transition.
	makeDoneParent := func(t *testing.T, dir, title, typ string) string {
		t.Helper()
		bdConfig(t, bd, dir, "set", "status.custom", "verified:done")
		p := bdCreate(t, bd, dir, title, "--type", typ)
		bdUpdate(t, bd, dir, p.ID, "--status", "verified")
		if got := bdShow(t, bd, dir, p.ID); got.Status != types.Status("verified") {
			t.Fatalf("setup: %s %s should be in done-category status 'verified', got %s", typ, p.ID, got.Status)
		}
		return p.ID
	}

	// --- create.go single `--parent <done-category>` axis (czu1s) ---

	t.Run("parent_flag_under_done_category_epic_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "epa")
		epic := makeDoneParent(t, dir, "done epic parent", "epic")
		out := bdCreateFail(t, bd, dir, "child under done epic", "--parent", epic)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on `create --parent <done-category epic>`, got:\n%s", out)
		}
	})

	t.Run("parent_flag_under_done_category_molecule_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "epm")
		mol := makeDoneParent(t, dir, "done mol parent", "molecule")
		out := bdCreateFail(t, bd, dir, "child under done mol", "--parent", mol)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on `create --parent <done-category molecule>`, got:\n%s", out)
		}
	})

	t.Run("parent_flag_under_done_category_epic_force_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "epf")
		epic := makeDoneParent(t, dir, "done epic force", "epic")
		child := bdCreate(t, bd, dir, "forced child", "--parent", epic, "--force")
		if child.ID == "" {
			t.Errorf("--force should land the child under a done-category epic via --parent, got empty id")
		}
	})

	// --- create.go `--deps parent-child:<done-category>` axis (p1p9n) ---

	t.Run("deps_parent_child_under_done_category_epic_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "eda")
		epic := makeDoneParent(t, dir, "done epic deps", "epic")
		out := bdCreateFail(t, bd, dir, "deps child", "--deps", "parent-child:"+epic)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on `create --deps parent-child:<done-category epic>`, got:\n%s", out)
		}
	})

	t.Run("deps_parent_child_under_done_category_epic_force_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "edf")
		epic := makeDoneParent(t, dir, "done epic deps force", "epic")
		child := bdCreate(t, bd, dir, "forced deps child", "--deps", "parent-child:"+epic, "--force")
		if child.ID == "" {
			t.Errorf("--force should land the child under a done-category epic via --deps, got empty id")
		}
	})

	// --- markdown `- parent-child:<done-category>` axis (p1p9n/markdown seam) ---

	writeMD := func(t *testing.T, dir, title, dep string) string {
		t.Helper()
		body := "## " + title + "\n\nBody.\n\n### Dependencies\n" + dep + "\n"
		p := filepath.Join(dir, "batch-"+title+".md")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write markdown: %v", err)
		}
		return p
	}

	t.Run("markdown_parent_child_under_done_category_epic_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "mda")
		epic := makeDoneParent(t, dir, "done epic md", "epic")
		md := writeMD(t, dir, "mdchild", "parent-child:"+epic)
		out := bdCreateFail(t, bd, dir, "--file", md)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on markdown create parent-child under done-category epic, got:\n%s", out)
		}
	})

	t.Run("markdown_parent_child_under_done_category_epic_force_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "mdf")
		epic := makeDoneParent(t, dir, "done epic md force", "epic")
		md := writeMD(t, dir, "mdforce", "parent-child:"+epic)
		out, err := bdRunWithFlockRetry(t, bd, dir, "create", "--file", md, "--force")
		if err != nil {
			t.Errorf("--force should land the markdown batch under a done-category epic, got err: %v\n%s", err, out)
		}
	})

	// --- NEGATIVES / regression controls (guard must not over- or under-fire) ---

	// (N1) literal-closed parent still refused WITH a done-set registered — proves
	//      the done-aware guard is a strict superset of the literal-closed guard,
	//      not a replacement that dropped the original case.
	t.Run("literal_closed_epic_still_refused_with_done_set_present", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lce")
		bdConfig(t, bd, dir, "set", "status.custom", "verified:done")
		epic := bdCreate(t, bd, dir, "literal closed epic", "--type", "epic")
		bdClose(t, bd, dir, epic.ID)
		out := bdCreateFail(t, bd, dir, "child under literal-closed epic", "--parent", epic.ID)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("a literally-closed epic must still be refused when a done-set is registered, got:\n%s", out)
		}
	})

	// (N2) FROZEN-category parent is ALLOWED — parked != done. A deferred/frozen
	//      status is NOT terminal, so a child under it must land, proving frozen is
	//      excluded from parentStatusIsTerminal.
	t.Run("frozen_category_parent_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "frz")
		bdConfig(t, bd, dir, "set", "status.custom", "parked:frozen")
		epic := bdCreate(t, bd, dir, "frozen epic", "--type", "epic")
		bdUpdate(t, bd, dir, epic.ID, "--status", "parked")
		if got := bdShow(t, bd, dir, epic.ID); got.Status != types.Status("parked") {
			t.Fatalf("setup: frozen epic should be 'parked', got %s", got.Status)
		}
		child := bdCreate(t, bd, dir, "child under frozen epic", "--parent", epic.ID)
		if child.ID == "" {
			t.Errorf("a FROZEN-category (parked, not done) parent must allow a child — parked != terminal, got empty id")
		}
	})

	// (N3) OPEN epic parent (baseline regression control): a child under an OPEN
	//      auto-closing parent must always land.
	t.Run("open_epic_parent_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "opn")
		bdConfig(t, bd, dir, "set", "status.custom", "verified:done")
		epic := bdCreate(t, bd, dir, "open epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "child under open epic", "--parent", epic.ID)
		if child.ID == "" {
			t.Errorf("child under an OPEN epic must land, got empty id")
		}
	})
}

// TestCreateFormClosedParentDoneCategory_ei6vq covers the two create-FORM legs of
// beads-ei6vq in-process (CreateIssueFromFormValues is a library entry point, so
// it is driven directly like o8h79 rather than via the embedded subprocess):
//
//   - the form's `--parent` leg (fv.ParentID, 3jdex)
//   - the form's Dependencies FIELD parent-child edge (fv.Dependencies, o8h79)
//
// Both keyed on literal StatusClosed and are now done-aware via
// parentStatusIsTerminal + doneCategoryStatusNames.
//
// MUTATION-VERIFY: revert either form guard to a bare `== types.StatusClosed` →
// the corresponding done-category subtest goes RED (returns nil, child created).
func TestCreateFormClosedParentDoneCategory_ei6vq(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	// Register a custom done-category status on this store.
	if err := s.SetConfig(ctx, "status.custom", "verified:done"); err != nil {
		t.Fatalf("register done-category status: %v", err)
	}

	// makeDoneParent creates an auto-closing parent then moves it into the
	// done-category status (terminal, not literally closed). It is childless at
	// transition time, so the forward close-guard permits it.
	makeDoneParent := func(t *testing.T, title, issueType string) string {
		t.Helper()
		parent, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title:     title,
			Priority:  1,
			IssueType: issueType,
		}, "test")
		if err != nil {
			t.Fatalf("create %s parent: %v", issueType, err)
		}
		if err := s.UpdateIssue(ctx, parent.ID, map[string]interface{}{"status": "verified"}, "test"); err != nil {
			t.Fatalf("move %s parent to done-category status: %v", issueType, err)
		}
		return parent.ID
	}

	// --- form `--parent` leg (3jdex) ---

	t.Run("form_parent_under_done_category_epic_refused", func(t *testing.T) {
		pid := makeDoneParent(t, "form done epic", "epic")
		_, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title:     "form child under done epic",
			Priority:  2,
			IssueType: "task",
			ParentID:  pid,
		}, "test")
		if err == nil {
			t.Fatalf("form --parent under a done-category epic must be refused, got nil")
		}
		if !strings.Contains(err.Error(), "closed parent") {
			t.Errorf("expected 'closed parent' guard error, got: %v", err)
		}
	})

	t.Run("form_parent_under_done_category_epic_force_succeeds", func(t *testing.T) {
		pid := makeDoneParent(t, "form done epic force", "epic")
		if _, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title:     "forced form child",
			Priority:  2,
			IssueType: "task",
			ParentID:  pid,
			Force:     true,
		}, "test"); err != nil {
			t.Fatalf("--force should override the form --parent done-category guard, got: %v", err)
		}
	})

	// --- form Dependencies FIELD parent-child leg (o8h79) ---

	t.Run("form_deps_field_under_done_category_molecule_refused", func(t *testing.T) {
		pid := makeDoneParent(t, "form done mol", "molecule")
		_, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title:        "form deps child under done mol",
			Priority:     2,
			IssueType:    "task",
			Dependencies: []string{"parent-child:" + pid},
		}, "test")
		if err == nil {
			t.Fatalf("form Dependencies-field parent-child under a done-category molecule must be refused, got nil")
		}
		if !strings.Contains(err.Error(), "closed parent") {
			t.Errorf("expected 'closed parent' guard error, got: %v", err)
		}
	})

	t.Run("form_deps_field_under_done_category_epic_force_succeeds", func(t *testing.T) {
		pid := makeDoneParent(t, "form deps done epic force", "epic")
		if _, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title:        "forced form deps child",
			Priority:     2,
			IssueType:    "task",
			Dependencies: []string{"parent-child:" + pid},
			Force:        true,
		}, "test"); err != nil {
			t.Fatalf("--force should override the form Dependencies-field done-category guard, got: %v", err)
		}
	})

	// --- NEGATIVES ---

	// (N1) literal-closed parent still refused with the done-set registered.
	t.Run("form_literal_closed_epic_still_refused", func(t *testing.T) {
		parent, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title: "form literal closed epic", Priority: 1, IssueType: "epic",
		}, "test")
		if err != nil {
			t.Fatalf("create epic: %v", err)
		}
		if err := s.CloseIssue(ctx, parent.ID, "done", "test", ""); err != nil {
			t.Fatalf("close epic: %v", err)
		}
		if _, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title: "form child under literal closed", Priority: 2, IssueType: "task", ParentID: parent.ID,
		}, "test"); err == nil || !strings.Contains(err.Error(), "closed parent") {
			t.Errorf("a literally-closed epic must still be refused via the form with a done-set present, got: %v", err)
		}
	})

	// (N2) OPEN epic parent allowed (regression control).
	t.Run("form_open_epic_parent_allowed", func(t *testing.T) {
		parent, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title: "form open epic", Priority: 1, IssueType: "epic",
		}, "test")
		if err != nil {
			t.Fatalf("create open epic: %v", err)
		}
		if _, err := CreateIssueFromFormValues(ctx, s, &createFormValues{
			Title: "form child under open epic", Priority: 2, IssueType: "task", ParentID: parent.ID,
		}, "test"); err != nil {
			t.Errorf("child under an OPEN epic via the form must land, got: %v", err)
		}
	})
}
