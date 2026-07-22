//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedCreateDepsParentChildClosedParentGuard is the beads-p1p9n teeth
// for the DIRECT (embedded) create path. The closed-parent guard family
// (create.go a8a1b/czu1s single `--parent`; t39ph graph; aw9x8/j8ekq/if6s0
// dep-add) refuses an OPEN child under a CLOSED auto-closing parent (epic OR
// molecule OR wisp). But the CREATE-via-generic-dep axis was a straggler: the
// create-time guard is gated on parentID (from --parent), while a parent-child
// edge supplied via `--deps parent-child:<closed-parent>` flows through the
// generic edgeSpecs loop (create.go, tx.AddDependency) with NO guard — and the
// dep-add guard lives at the CLI dep.go layer, not at storage AddDependency —
// so an open child linked under a closed epic/molecule via --deps landed
// silently (rc=0), recreating the forbidden "closed parent with open child".
//
// Fix mirrors the guard at the parent-child dep-spec seam on the direct path,
// honoring --force, using the shared isAutoClosingParentType. Mutation:
// removing the edgeSpecs-loop guard turns the refuse cases below RED (child
// minted + edge added, rc=0).
func TestEmbeddedCreateDepsParentChildClosedParentGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt create tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	makeClosedParent := func(t *testing.T, dir, title, typ string) string {
		t.Helper()
		p := bdCreate(t, bd, dir, title, "--type", typ)
		bdClose(t, bd, dir, p.ID)
		if got := bdShow(t, bd, dir, p.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: %s %s should be closed, got %s", typ, p.ID, got.Status)
		}
		return p.ID
	}

	// (1) --deps parent-child:<closed EPIC> is refused — the same guard the
	//     single `create --parent <closed-epic>` path enforces, now on the
	//     generic-dep axis too.
	t.Run("deps_parent_child_under_closed_epic_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cde")
		epic := makeClosedParent(t, dir, "closed epic deps", "epic")
		out := bdCreateFail(t, bd, dir, "deps child epic", "--deps", "parent-child:"+epic)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on create --deps parent-child under closed epic, got:\n%s", out)
		}
	})

	// (2) MOLECULE parent (aw9x8 widening axis) — a closed molecule root must be
	//     guarded on this axis too, not just epics.
	t.Run("deps_parent_child_under_closed_molecule_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cdm")
		mol := makeClosedParent(t, dir, "closed molecule deps", "molecule")
		out := bdCreateFail(t, bd, dir, "deps child mol", "--deps", "parent-child:"+mol)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on create --deps parent-child under closed molecule, got:\n%s", out)
		}
	})

	// (3) --force overrides the guard and lands the child + edge.
	t.Run("deps_parent_child_under_closed_epic_force_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cdf")
		epic := makeClosedParent(t, dir, "closed epic deps force", "epic")
		child := bdCreate(t, bd, dir, "forced deps child", "--deps", "parent-child:"+epic, "--force")
		if child.ID == "" {
			t.Errorf("--force should land the child under a closed epic via --deps, got empty id")
		}
	})

	// (4) OPEN epic parent (regression control): a child under an OPEN parent via
	//     --deps must still succeed — the guard fires only on a CLOSED parent.
	t.Run("deps_parent_child_under_open_epic_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cdo")
		epic := bdCreate(t, bd, dir, "open epic deps", "--type", "epic")
		child := bdCreate(t, bd, dir, "deps child open", "--deps", "parent-child:"+epic.ID)
		if child.ID == "" {
			t.Errorf("child under an OPEN epic via --deps should land, got empty id")
		}
	})

	// (5) a non-parent-child edge (blocks) to a CLOSED issue must NOT trip the
	//     closed-PARENT guard — proves the guard is scoped to parent-child. The
	//     blocks target is a plain closed TASK (not an epic) so the unrelated
	//     "epics can only block epics" type rule doesn't interfere.
	t.Run("deps_blocks_to_closed_issue_unaffected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cdb")
		blocker := bdCreate(t, bd, dir, "closed blocker task")
		bdClose(t, bd, dir, blocker.ID)
		child := bdCreate(t, bd, dir, "deps blocks child", "--deps", "blocks:"+blocker.ID)
		if child.ID == "" {
			t.Errorf("a blocks: edge to a closed issue must not trip the closed-parent guard, got empty id")
		}
	})

	// --- MARKDOWN axis (beads-p1p9n names --from-markdown explicitly) ---
	// The direct markdown path (createIssuesFromMarkdown → CreateIssuesWithFullOptions
	// → issueops.CreateIssuesInTx) is a SEPARATE seam from both the flag edgeSpecs
	// loop and the domain create() loop — and its storage callee is shared with
	// JSONL import, so the guard lives at the markdown authoring seam (markdown.go).
	// Dependencies are authored in a "### Dependencies" section (markdown.go
	// processIssueSection); the depType:id colon form is parsed in the loop at
	// create-from-markdown time. A bare bullet under the title is NOT a dep.
	writeMD := func(t *testing.T, dir, title, dep string) string {
		t.Helper()
		body := "## " + title + "\n\nBody.\n\n### Dependencies\n" + dep + "\n"
		p := filepath.Join(dir, "batch-"+title+".md")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write markdown: %v", err)
		}
		return p
	}

	// (6) markdown `- parent-child:<closed EPIC>` is refused.
	t.Run("markdown_parent_child_under_closed_epic_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cme")
		epic := makeClosedParent(t, dir, "closed epic md", "epic")
		md := writeMD(t, dir, "mdchild", "parent-child:"+epic)
		out := bdCreateFail(t, bd, dir, "--file", md)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on markdown create parent-child under closed epic, got:\n%s", out)
		}
	})

	// (7) MOLECULE parent on the markdown axis.
	t.Run("markdown_parent_child_under_closed_molecule_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cmm")
		mol := makeClosedParent(t, dir, "closed mol md", "molecule")
		md := writeMD(t, dir, "mdchildmol", "parent-child:"+mol)
		out := bdCreateFail(t, bd, dir, "--file", md)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on markdown create parent-child under closed molecule, got:\n%s", out)
		}
	})

	// (8) --force overrides the guard on the markdown axis and lands the batch.
	t.Run("markdown_parent_child_under_closed_epic_force_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cmf")
		epic := makeClosedParent(t, dir, "closed epic md force", "epic")
		md := writeMD(t, dir, "mdforce", "parent-child:"+epic)
		out, err := bdRunWithFlockRetry(t, bd, dir, "create", "--file", md, "--force")
		if err != nil {
			t.Errorf("--force should land the markdown batch under a closed epic, got err: %v\n%s", err, out)
		}
	})

	// (9) OPEN epic parent on the markdown axis (regression control).
	t.Run("markdown_parent_child_under_open_epic_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cmo")
		epic := bdCreate(t, bd, dir, "open epic md", "--type", "epic")
		md := writeMD(t, dir, "mdopen", "parent-child:"+epic.ID)
		out, err := bdRunWithFlockRetry(t, bd, dir, "create", "--file", md)
		if err != nil {
			t.Errorf("markdown child under an OPEN epic should land, got err: %v\n%s", err, out)
		}
	})
}
