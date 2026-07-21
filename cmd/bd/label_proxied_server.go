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
//
// beads-4zy65: proxied twin of the direct no-op-honesty guards (add: qi8t
// addLabelsHonoringNoChange; remove: yaux present/missing split). The shared
// applyUpdateProxiedOne writes+commits unconditionally and this loop reported a
// blanket "added"/"removed" for every (issue,label), so under proxied-server
// mode `bd label add <existing>` printed a fake "✓ Added" / JSON status:"added"
// and `bd label remove <never-had>` printed a fake "✓ Removed" / status:"removed"
// — the very CI/agent-gate false-success qi8t/yaux exist to kill, live on the
// hub-connected path. AddLabelInTx/RemoveLabelInTx are idempotent so the write
// is harmless (no updated_at bump), but the verb must report the distinction.
// Pre-read each id's current labels via the UOW (proxied read, mirroring the
// direct store.GetLabels), compute honest per-(issue,label) status, and only
// pass genuinely-changing labels into the update spec. Match the DIRECT path's
// contract exactly (twin-parity), NOT a divergent "not present" in-band status:
//   - add: "unchanged" for a present label (rc0, JSON status:"unchanged");
//   - remove: a never-present label is a FAILURE (yaux: rc!=0, "no label ... to
//     remove"), reported per-id, aligning with the direct labelPartialFailure /
//     HandleErrorRespectJSON semantics — not swallowed as an in-band success.
func applyLabelBatchProxied(ctx context.Context, issueIDs, labels []string, operation string) error {
	if len(labels) == 0 {
		return HandleErrorRespectJSON("no label given")
	}
	if len(issueIDs) == 0 {
		return HandleErrorRespectJSON("no issue id given")
	}
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}

	verb := "Added"
	prep := "to"
	if operation == "removed" {
		verb = "Removed"
		prep = "from"
	}

	trimmed := make([]string, len(labels))
	for i, l := range labels {
		trimmed[i] = strings.TrimSpace(l)
	}

	// proxiedIssueLabels reads an issue's (or wisp's) current labels through a
	// short-lived read UOW, mirroring the direct path's store.GetLabels. Returns
	// (nil, false) when the id doesn't resolve so the caller can defer to the
	// shared applyUpdateProxiedOne for a consistent not-found error.
	proxiedIssueLabels := func(id string) ([]string, bool) {
		uw, err := uowProvider.NewUOW(ctx)
		if err != nil {
			return nil, false
		}
		defer uw.Close(ctx)
		cur, isWisp := proxiedResolveIssueOrWisp(ctx, uw, id)
		if cur == nil {
			return nil, false
		}
		var existing []string
		if isWisp {
			existing, _ = uw.LabelUseCase().GetWispLabels(ctx, cur.ID)
		} else {
			existing, _ = uw.LabelUseCase().GetLabels(ctx, cur.ID)
		}
		return existing, true
	}

	okCount := 0        // ids that resolved + mutated (or were a clean no-op)
	changedAny := false // at least one genuine write happened
	var results []map[string]interface{}
	var mutated []*types.Issue
	var missingRemovals []string // ids for a never-present remove (yaux failure)

	for _, id := range issueIDs {
		existing, resolved := proxiedIssueLabels(id)
		if !resolved {
			// Not-found (or read failure): defer to the shared core, which emits
			// the consistent per-item not-found error to stderr and skips the id.
			in := &updateInput{}
			if operation == "removed" {
				in.removeLabels = labels
			} else {
				in.addLabels = labels
			}
			if _, ok := applyUpdateProxiedOne(ctx, id, in, false); ok {
				okCount++
			}
			continue
		}
		have := make(map[string]struct{}, len(existing))
		for _, l := range existing {
			have[l] = struct{}{}
		}

		// Partition this id's labels into genuinely-changing vs no-op.
		var changing []string
		type pair struct {
			label  string
			status string
		}
		var pairs []pair
		allPresentForRemove := true
		for _, label := range trimmed {
			_, present := have[label]
			if operation == "removed" {
				if present {
					changing = append(changing, label)
					pairs = append(pairs, pair{label, "removed"})
				} else {
					allPresentForRemove = false // yaux: never-present = failure
				}
			} else {
				if present {
					pairs = append(pairs, pair{label, "unchanged"})
				} else {
					changing = append(changing, label)
					pairs = append(pairs, pair{label, "added"})
				}
			}
		}

		// Apply only the genuinely-changing labels (skip the write entirely when
		// this id is a pure no-op — no spurious commit).
		var issue *types.Issue
		if len(changing) > 0 {
			in := &updateInput{}
			if operation == "removed" {
				in.removeLabels = changing
			} else {
				in.addLabels = changing
			}
			var ok bool
			issue, ok = applyUpdateProxiedOne(ctx, id, in, false)
			if !ok {
				// applyUpdateProxiedOne already reported the per-item error.
				continue
			}
			changedAny = true
			mutated = append(mutated, issue)
		}
		okCount++

		// Emit the honest per-(issue,label) status (matching the direct qi8t/yaux
		// output). For a remove, only present labels produce a per-pair line; a
		// never-present label falls into missingRemovals below (yaux failure).
		if jsonOutput {
			for _, p := range pairs {
				results = append(results, map[string]interface{}{
					"status":   p.status,
					"issue_id": id,
					"label":    p.label,
				})
			}
		} else {
			for _, p := range pairs {
				switch p.status {
				case "unchanged":
					fmt.Printf("%s label '%s' already present on %s (no change)\n", ui.RenderInfoIcon(), p.label, id)
				default:
					fmt.Printf("%s %s label '%s' %s %s\n", ui.RenderPass("✓"), verb, p.label, prep, id)
				}
			}
		}

		if operation == "removed" && !allPresentForRemove {
			missingRemovals = append(missingRemovals, id)
		}
	}

	if jsonOutput && len(results) > 0 {
		if err := outputJSON(results); err != nil {
			return err
		}
	}
	if changedAny {
		commandDidWrite.Store(true)
	}
	if len(mutated) > 0 {
		SetLastTouchedID(mutated[0].ID)
	}

	// Every requested ID failed to resolve → the single stdout JSON error object
	// is the sole output (stderr already carries the per-item errors).
	if okCount == 0 {
		if jsonOutput {
			return HandleErrorRespectJSON("no issues labeled matching the provided IDs")
		}
		return SilentExit()
	}

	// beads-4zy65 / yaux: a remove that named a label an issue never carried is a
	// failure (not a silent success), matching the direct remove path. When some
	// output already went to stdout, route the summary to stderr + rc1 (the uctf/
	// en28 partial-batch contract); otherwise the single stdout error is correct.
	partial := len(results) > 0 || len(mutated) > 0
	fail := func(format string, a ...interface{}) error {
		if partial {
			return labelPartialFailure(format, a...)
		}
		return HandleErrorRespectJSON(format, a...)
	}
	if len(missingRemovals) > 0 {
		return fail("no label '%s' to remove on %d issue(s): %s",
			strings.Join(trimmed, "', '"), len(missingRemovals), strings.Join(missingRemovals, ", "))
	}

	// beads-uctf: a PARTIAL batch (some ids labeled, at least one failed to
	// resolve). The per-item errors were already printed to stderr by
	// applyUpdateProxiedOne and the results array is on stdout — exit non-zero
	// (rc1) WITHOUT adding a second stdout doc.
	if okCount < len(issueIDs) {
		return SilentExit()
	}
	return nil
}
