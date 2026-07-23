package main

import (
	"context"
	"fmt"
	"os"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// runShipProxiedServer publishes a capability via the proxied unit-of-work
// stack, for hub-connected crew where the global `store` is nil (beads-kjda,
// fszd/aocj umbrella). ship reads issues by the export:<cap> label then adds a
// provides:<cap> label — the by-label read (GetIssuesByLabel) was NOT on any
// UOW use-case (only GetLabels/AddLabel were), so this is an interface-extension
// leg: GetIssuesByLabel added to IssueUseCase (backed by
// issueops.GetIssuesByLabelInTx widened *sql.Tx→DBTX). Mirrors cmd/bd/ship.go.
func runShipProxiedServer(ctx context.Context, capability string, force, dryRun bool) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()
	labelUC := uw.LabelUseCase()

	exportLabel := "export:" + capability
	providesLabel := "provides:" + capability

	issues, err := issueUC.GetIssuesByLabel(ctx, exportLabel)
	if err != nil {
		return HandleErrorRespectJSON("listing issues: %v", err)
	}

	if len(issues) == 0 {
		return HandleErrorWithHintRespectJSON(
			fmt.Sprintf("no issue found with label '%s'", exportLabel),
			fmt.Sprintf("add the label first: bd label add <issue-id> %s", exportLabel))
	}

	if len(issues) > 1 {
		printShipMultiLabelMatches(os.Stderr, issues, exportLabel)
		return HandleErrorRespectJSON("only one issue should have this label")
	}

	issue := issues[0]

	// beads-6yt1m: proxied twin of the ship.go done-category widening — accept a
	// custom done-category status as satisfying the "work is complete" ship
	// precondition, not just literal-closed. Uses the tx-scoped proxied done-set
	// resolver (doneCategoryStatusSetProxied) so it reads the same config the
	// direct path reads via doneCategoryStatusNames. Degraded-safe (empty set →
	// literal-closed); Frozen excluded.
	done := doneCategoryStatusSetProxied(ctx, uw)
	if issue.Status != types.StatusClosed && !done[string(issue.Status)] && !force {
		return HandleErrorWithHintRespectJSON(
			fmt.Sprintf("issue %s is not closed (status: %s)", issue.ID, issue.Status),
			"close the issue first, or use --force to override")
	}

	labels, err := labelUC.GetLabels(ctx, issue.ID)
	if err != nil {
		return HandleErrorRespectJSON("getting labels: %v", err)
	}
	for _, l := range labels {
		if l == providesLabel {
			if jsonOutput {
				return outputJSON(map[string]interface{}{
					"status":     "already_shipped",
					"capability": capability,
					"issue_id":   issue.ID,
				})
			}
			fmt.Printf("%s Capability '%s' already shipped (%s)\n",
				ui.RenderPass("✓"), capability, issue.ID)
			return nil
		}
	}

	if dryRun {
		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"status":     "dry_run",
				"capability": capability,
				"issue_id":   issue.ID,
				"would_add":  providesLabel,
			})
		}
		fmt.Printf("%s Would ship '%s' on %s (dry run)\n",
			ui.RenderAccent("→"), capability, issue.ID)
		return nil
	}

	if err := labelUC.AddLabel(ctx, issue.ID, providesLabel, actor); err != nil {
		return HandleErrorRespectJSON("adding label: %v", err)
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: ship %s", capability)); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		return outputJSON(map[string]interface{}{
			"status":     "shipped",
			"capability": capability,
			"issue_id":   issue.ID,
			"label":      providesLabel,
		})
	}
	fmt.Printf("%s Shipped %s (%s)\n",
		ui.RenderPass("✓"), capability, issue.ID)
	fmt.Printf("  Added label: %s\n", providesLabel)
	fmt.Printf("\nExternal projects can now depend on: external:%s:%s\n",
		"<this-project>", capability)
	return nil
}
