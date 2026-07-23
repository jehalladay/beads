//go:build cgo

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedImportParentDemoteGuard_ts7vq is the proxied-server twin of
// TestEmbeddedImportParentDemoteGuard_ts7vq (beads-ts7vq): `bd import` under a
// proxied-server backend flows through the SAME importIssuesCore/upsert +
// guardImportParentDemote path as the embedded backend (import has no separate
// proxied handler — the global store is proxied-backed), so the import
// close-guard-bypass fix must hold on the proxied path too. Mirrors the 29tyj /
// j8ekq direct+proxied twin convention for the close-guard family.
//
// MUTATION-VERIFIED with the embedded test (they share guardImportParentDemote):
// neuter the guard → the demote lands and the parent close succeeds with an open
// child (RED) on both backends.
func TestProxiedImportParentDemoteGuard_ts7vq(t *testing.T) {
	requireProxiedServerEnv(t)
	t.Parallel()

	bd := buildEmbeddedBD(t)

	const newerTS = "2027-06-01T00:00:00Z"

	// (1) Import demote of a proxied epic-with-open-child is reverted, and the
	//     parent close is still refused.
	t.Run("epic_with_open_child_demote_reverted", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pig")
		epic := bdProxiedCreate(t, bd, p.dir, "pig epic", "-t", "epic")
		// Open child attached parent-child to the epic via --deps.
		bdProxiedCreate(t, bd, p.dir, "pig child", "--deps", "parent-child:"+epic.ID)

		jsonl := filepath.Join(t.TempDir(), "demote.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"pig epic","issue_type":"task","updated_at":%q}`, epic.ID, newerTS))
		out, err := bdProxiedRun(t, bd, p.dir, "import", jsonl)
		if err != nil {
			t.Fatalf("proxied bd import failed: %v\n%s", err, out)
		}

		if got := bdProxiedShow(t, bd, p.dir, epic.ID); got.IssueType != types.TypeEpic {
			t.Errorf("beads-ts7vq (proxied): import silently demoted epic w/ open child to %q — must revert to epic [BUG]", got.IssueType)
		}
		if !strings.Contains(string(out), epic.ID) || !strings.Contains(string(out), "Kept parent type") {
			t.Errorf("beads-ts7vq (proxied): import demote-revert must report the id %s; got:\n%s", epic.ID, out)
		}
		closeOut := bdProxiedCloseFail(t, bd, p.dir, epic.ID)
		if !strings.Contains(closeOut, "open child") && !strings.Contains(closeOut, "--force") {
			t.Errorf("beads-ts7vq (proxied): after the reverted demote, `bd close` on the epic w/ open child must still be refused; got:\n%s", closeOut)
		}
	})

	// (2) --allow-stale demote is reverted on the proxied path too.
	t.Run("allow_stale_demote_reverted", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pis")
		epic := bdProxiedCreate(t, bd, p.dir, "pis epic", "-t", "epic")
		bdProxiedCreate(t, bd, p.dir, "pis child", "--deps", "parent-child:"+epic.ID)

		const olderTS = "2000-01-01T00:00:00Z"
		jsonl := filepath.Join(t.TempDir(), "stale-demote.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"pis epic","issue_type":"task","updated_at":%q}`, epic.ID, olderTS))
		if out, err := bdProxiedRun(t, bd, p.dir, "import", jsonl, "--allow-stale"); err != nil {
			t.Fatalf("proxied bd import --allow-stale failed: %v\n%s", err, out)
		}

		if got := bdProxiedShow(t, bd, p.dir, epic.ID); got.IssueType != types.TypeEpic {
			t.Errorf("beads-ts7vq (proxied): --allow-stale import silently demoted epic w/ open child to %q — must revert [BUG]", got.IssueType)
		}
		bdProxiedCloseFail(t, bd, p.dir, epic.ID)
	})

	// (3) CONTROL / regression: an epic with no open children is demotable by
	//     import on the proxied path (the guard must not over-block).
	t.Run("epic_no_open_children_demote_allowed", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pin")
		epic := bdProxiedCreate(t, bd, p.dir, "pin epic", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "pin child", "--deps", "parent-child:"+epic.ID)
		bdProxiedClose(t, bd, p.dir, child.ID)

		jsonl := filepath.Join(t.TempDir(), "safe-demote.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"pin epic","issue_type":"task","updated_at":%q}`, epic.ID, newerTS))
		if out, err := bdProxiedRun(t, bd, p.dir, "import", jsonl); err != nil {
			t.Fatalf("proxied bd import failed: %v\n%s", err, out)
		}
		if got := bdProxiedShow(t, bd, p.dir, epic.ID); got.IssueType != types.TypeTask {
			t.Errorf("beads-ts7vq (proxied): epic with no open children must be demotable by import, got %q", got.IssueType)
		}
	})
}
