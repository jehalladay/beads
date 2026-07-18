package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// beads-aocj: proxied-server handlers for `bd label add` / `bd label remove`.
//
// The direct path resolves+mutates via the global `store`, which is NIL in
// proxiedServerMode (main.go PersistentPreRun returns early, before store init,
// once uowProvider is set) — so both failed "storage is nil" for hub-connected
// crew, unlike `bd update --add-label/--remove-label` which route to a proxied
// handler. Route them to the SAME update proxied core (applyUpdateProxiedOne),
// which resolves via the UOW, enforces the NotTemplate guard (validateIssueUpdatable,
// beads-dwlg), and applies AddLabels/RemoveLabels through the update spec — so
// the shorthands stay in lockstep with their long form under proxied-server
// mode. Mirrors beads-1zuh (relate/unrelate), beads-qwez (assign/tag), and
// beads-8xb7 (defer).

// runLabelAddProxiedServer applies `bd label add [ids...] [label]` (and the
// repeatable --label form) via the proxied update core. Each id is applied
// through applyUpdateProxiedOne with AddLabels set, mirroring the direct add
// path (provides: rejected, template molecules skipped by the shared
// validateIssueUpdatable guard, partial-resolution → non-zero exit).
func runLabelAddProxiedServer(ctx context.Context, issueIDs, labels []string) error {
	for _, label := range labels {
		if strings.HasPrefix(label, "provides:") {
			return HandleErrorRespectJSON("'provides:' labels are reserved for cross-project capabilities. Hint: use 'bd ship %s' instead", strings.TrimPrefix(label, "provides:"))
		}
	}
	return applyLabelBatchProxied(ctx, issueIDs, labels, "added")
}

// runLabelRemoveProxiedServer applies `bd label remove [ids...] [label]` via the
// proxied update core with RemoveLabels set, mirroring the direct remove path.
func runLabelRemoveProxiedServer(ctx context.Context, issueIDs []string, label string) error {
	return applyLabelBatchProxied(ctx, issueIDs, []string{label}, "removed")
}

// applyLabelBatchProxied loops applyUpdateProxiedOne over every id, adding or
// removing every label in one update spec per issue. It preserves the direct
// label batch semantics: a per-id resolution/mutation failure is reported to
// stderr and skipped; if EVERY requested id fails, the command exits non-zero
// so scripts don't read false success (partial success keeps rc=0). operation
// is "added" or "removed".
func applyLabelBatchProxied(ctx context.Context, issueIDs, labels []string, operation string) error {
	if len(labels) == 0 {
		return HandleErrorRespectJSON("no label given")
	}
	if len(issueIDs) == 0 {
		return HandleErrorRespectJSON("no issue id given")
	}

	okCount := 0
	var results []map[string]interface{}
	var mutated []*types.Issue

	verb := "Added"
	prep := "to"
	if operation == "removed" {
		verb = "Removed"
		prep = "from"
	}

	for _, id := range issueIDs {
		in := &updateInput{}
		if operation == "removed" {
			in.removeLabels = labels
		} else {
			in.addLabels = labels
		}

		issue, ok := applyUpdateProxiedOne(ctx, id, in)
		if !ok {
			// applyUpdateProxiedOne already printed the per-item error to stderr.
			continue
		}
		okCount++
		mutated = append(mutated, issue)

		if jsonOutput {
			for _, label := range labels {
				results = append(results, map[string]interface{}{
					"status":   operation,
					"issue_id": issue.ID,
					"label":    label,
				})
			}
		} else {
			for _, label := range labels {
				fmt.Printf("%s %s label '%s' %s %s\n", ui.RenderPass("✓"), verb, label, prep, issue.ID)
			}
		}
	}

	if jsonOutput && len(results) > 0 {
		if err := outputJSON(results); err != nil {
			return err
		}
	}
	if okCount > 0 {
		commandDidWrite.Store(true)
		SetLastTouchedID(mutated[0].ID)
	}

	// Every requested ID failed → non-zero exit so scripts don't read false
	// success; partial success keeps rc=0. Mirrors the direct label path and the
	// update/close/defer batch paths.
	if okCount == 0 {
		if jsonOutput {
			return HandleErrorRespectJSON("no issues labeled matching the provided IDs")
		}
		return SilentExit()
	}
	return nil
}
