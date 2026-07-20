package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var molSquashCmd = &cobra.Command{
	Use:   "squash <molecule-id>",
	Short: "Compress molecule execution into a digest",
	Long: `Squash a molecule's ephemeral children into a single digest issue.

This command collects all ephemeral child issues of a molecule (Ephemeral=true),
generates a summary digest, and by default deletes the wisps once their work is
captured in the digest. Pass --keep-children to instead promote the wisps to
persistent (clearing their Wisp flag) so the trace survives wisp garbage
collection.

The squash operation:
  1. Loads the molecule and all its children
  2. Filters to only wisps (ephemeral issues with Ephemeral=true)
  3. Generates a digest (summary of work done)
  4. Creates a permanent digest issue (Ephemeral=false)
  5. Default: deletes the ephemeral children after the digest is created.
     With --keep-children: clears each child's Wisp flag instead, promoting
     them to persistent (they survive "bd mol wisp gc").

AGENT INTEGRATION:
Use --summary to provide an AI-generated summary. This keeps bd as a pure
tool - the calling agent (orchestrator worker, Claude Code, etc.) is responsible
for generating intelligent summaries. Without --summary, a basic concatenation
of child issue content is used.

This is part of the wisp workflow: spawn creates wisps,
execution happens, squash compresses the trace into an outcome (digest).

Example:
  bd mol squash bd-abc123                    # Squash and promote children
  bd mol squash bd-abc123 --dry-run          # Preview what would be squashed
  bd mol squash bd-abc123 --keep-children    # Keep wisps after digest
  bd mol squash bd-abc123 --summary "Agent-generated summary of work done"`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMolSquash,
}

// squashSummaryProvided reports whether an agent-supplied --summary should be
// used as the digest content. A whitespace-only value is treated as NOT
// provided (beads-au0rt), so it collapses to the auto-generated generateDigest()
// output rather than overwriting the permanent digest's Description with blank
// whitespace. A genuine summary is used verbatim (its formatting preserved),
// matching the collapse-to-empty semantics of the stored-blank-reason class
// (beads-in93a close / beads-5rix3 reopen).
func squashSummaryProvided(summary string) bool {
	return strings.TrimSpace(summary) != ""
}

// SquashResult holds the result of a squash operation
type SquashResult struct {
	MoleculeID    string   `json:"molecule_id"`
	DigestID      string   `json:"digest_id"`
	SquashedIDs   []string `json:"squashed_ids"`
	SquashedCount int      `json:"squashed_count"`
	DeletedCount  int      `json:"deleted_count"`
	KeptChildren  bool     `json:"kept_children"`
	WispSquash    bool     `json:"wisp_squash,omitempty"` // True if this was a wisp→digest squash
}

func runMolSquash(cmd *cobra.Command, args []string) error {
	CheckReadonly("mol squash")

	evt := metrics.NewCommandEvent("mol-squash")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	// beads-ojyjj (aocj fail-loud class): in proxied-server mode main.go's
	// PersistentPreRunE returns before newDoltStore (main.go:1147-1155) leaving
	// the global store nil, so mol squash's store.GetIssue + transact would
	// nil-panic — and the bare store==nil check misdiagnoses the proxied config
	// as a local "no database connection". Squash writes via a raw
	// storage.Transaction the proxied UOW does not yield, so fail loud with an
	// accurate message (mirrors mgjco/merge-slot). Guard BEFORE the nil check.
	if usesProxiedServer() {
		return HandleErrorRespectJSON("mol squash is not supported in proxied-server mode (connect directly with an embedded/dolt store)")
	}
	if err := ensureStoreActive(); err != nil {
		return HandleErrorWithHintRespectJSON(err.Error(), diagHint())
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	keepChildren, _ := cmd.Flags().GetBool("keep-children")
	summary, _ := cmd.Flags().GetString("summary")

	moleculeID, err := utils.ResolvePartialID(ctx, store, args[0])
	if err != nil {
		return HandleErrorRespectJSON("resolving molecule ID %s: %v", args[0], err)
	}

	subgraph, err := loadTemplateSubgraph(ctx, store, moleculeID)
	if err != nil {
		return HandleErrorRespectJSON("loading molecule: %v", err)
	}

	var wispChildren []*types.Issue
	for _, issue := range subgraph.Issues {
		if issue.ID == subgraph.Root.ID {
			continue
		}
		if issue.Ephemeral {
			wispChildren = append(wispChildren, issue)
		}
	}

	if len(wispChildren) == 0 {
		if jsonOutput {
			// beads-lf0l3: SquashedIDs is []string with no omitempty, so the bare
			// SquashResult{} literal emits "squashed_ids":null while the success
			// path (:261 SquashedIDs: childIDs) emits an array. Init to [] so the
			// no-wisp-children leg matches the array contract (guib/tamf/history
			// null-slice class). Reachable for a molecule with no ephemeral children.
			return outputJSON(SquashResult{
				MoleculeID:    moleculeID,
				SquashedIDs:   []string{},
				SquashedCount: 0,
			})
		}
		fmt.Printf("No ephemeral children found for molecule %s\n", moleculeID)
		return nil
	}

	if dryRun {
		printMolSquashDryRunPreview(subgraph.Root, wispChildren, moleculeID, keepChildren)
		return nil
	}

	result, err := squashMolecule(ctx, store, subgraph.Root, wispChildren, keepChildren, summary, actor)
	if err != nil {
		return HandleErrorRespectJSON("squashing molecule: %v", err)
	}

	if jsonOutput {
		return outputJSON(result)
	}

	fmt.Printf("%s Squashed molecule: %d children → 1 digest\n", ui.RenderPass("✓"), result.SquashedCount)
	fmt.Printf("  Digest ID: %s\n", result.DigestID)
	if result.DeletedCount > 0 {
		fmt.Printf("  Deleted: %d wisps\n", result.DeletedCount)
	} else if result.KeptChildren {
		fmt.Printf("  Children preserved (--keep-children)\n")
	}
	if result.WispSquash {
		fmt.Printf("  Root auto-closed: %s\n", result.MoleculeID)
	}
	return nil
}

// generateDigest creates a summary from the molecule execution
// Tier 2: Simple concatenation of titles and descriptions
// Tier 3 (future): AI-powered summarization using Haiku
// printMolSquashDryRunPreview renders the `bd mol squash --dry-run` preview.
// Root/child titles are routed through displayTitle (ui.SanitizeForTerminal)
// because a molecule/step title can originate from an untrusted import
// (JSONL/markdown/SCM) carrying OSC/CSI terminal-control escapes (OSC 0
// window-title / OSC 52 clipboard); printing them raw would inject control
// sequences onto the preview lines. 7n9y sink-class slice (beads-uhiqz).
func printMolSquashDryRunPreview(root *types.Issue, wispChildren []*types.Issue, moleculeID string, keepChildren bool) {
	fmt.Printf("\nDry run: would squash %d ephemeral children of %s\n\n", len(wispChildren), moleculeID)
	fmt.Printf("Root: %s\n", displayTitle(root.Title))
	fmt.Printf("\nWisp children to squash:\n")
	for _, issue := range wispChildren {
		status := string(issue.Status)
		fmt.Printf("  - [%s] %s (%s)\n", status, displayTitle(issue.Title), issue.ID)
	}
	fmt.Printf("\nDigest preview:\n")
	// Sanitize the preview copy only (display-only): the stored digest is built
	// independently in squashMolecule, so this does not alter persisted content.
	// The digest embeds root/child titles, which can carry terminal escapes.
	digest := displayTitle(generateDigest(root, wispChildren))
	if len(digest) > 500 {
		fmt.Printf("%s...\n", digest[:500])
	} else {
		fmt.Printf("%s\n", digest)
	}
	if keepChildren {
		fmt.Printf("\n--keep-children: children would NOT be deleted\n")
	} else {
		fmt.Printf("\nChildren would be deleted after digest creation.\n")
	}
}

func generateDigest(root *types.Issue, children []*types.Issue) string {
	var sb strings.Builder

	sb.WriteString("## Molecule Execution Summary\n\n")
	sb.WriteString(fmt.Sprintf("**Molecule**: %s\n", root.Title))
	sb.WriteString(fmt.Sprintf("**Steps**: %d\n\n", len(children)))

	// Count completed vs other statuses
	completed := 0
	inProgress := 0
	for _, c := range children {
		switch c.Status {
		case types.StatusClosed:
			completed++
		case types.StatusInProgress:
			inProgress++
		}
	}
	sb.WriteString(fmt.Sprintf("**Completed**: %d/%d\n", completed, len(children)))
	if inProgress > 0 {
		sb.WriteString(fmt.Sprintf("**In Progress**: %d\n", inProgress))
	}
	sb.WriteString("\n---\n\n")

	// List each step with its outcome
	sb.WriteString("### Steps\n\n")
	for i, child := range children {
		status := string(child.Status)
		sb.WriteString(fmt.Sprintf("%d. **[%s]** %s\n", i+1, status, child.Title))
		if child.Description != "" {
			// Include first 200 chars of description
			desc := child.Description
			if len(desc) > 200 {
				desc = desc[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("   %s\n", desc))
		}
		if child.CloseReason != "" {
			sb.WriteString(fmt.Sprintf("   *Outcome: %s*\n", child.CloseReason))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// squashMolecule performs the squash operation
// If summary is provided (non-empty), it's used as the digest content.
// Otherwise, generateDigest() creates a basic concatenation.
// This enables agents to provide AI-generated summaries while keeping bd as a pure tool.
func squashMolecule(ctx context.Context, s storage.DoltStorage, root *types.Issue, children []*types.Issue, keepChildren bool, summary string, actorName string) (*SquashResult, error) {
	if s == nil {
		return nil, fmt.Errorf("no database connection")
	}

	// Collect child IDs
	childIDs := make([]string, len(children))
	for i, c := range children {
		childIDs[i] = c.ID
	}

	// Use agent-provided summary if available, otherwise generate basic digest.
	// A whitespace-only --summary is treated as NOT provided (beads-au0rt): the
	// plain `summary != ""` guard would otherwise overwrite the permanent digest's
	// Description with blank whitespace, silently discarding the rich
	// generateDigest() content. --summary is optional ("bypasses auto-generation"),
	// so a blank value collapses to the auto-generated digest rather than storing
	// blank content — the mol-squash sibling of the stored-blank-reason class
	// (beads-in93a close / beads-5rix3 reopen). A genuine summary is used VERBATIM.
	var digestContent string
	if squashSummaryProvided(summary) {
		digestContent = summary
	} else {
		digestContent = generateDigest(root, children)
	}

	// Create digest issue (permanent, not a wisp)
	now := time.Now()
	digestIssue := &types.Issue{
		Title:       fmt.Sprintf("Digest: %s", root.Title),
		Description: digestContent,
		Status:      types.StatusClosed,
		CloseReason: fmt.Sprintf("Squashed from %d wisps", len(children)),
		Priority:    root.Priority,
		IssueType:   types.TypeTask,
		Ephemeral:   false, // Digest is permanent, not a wisp
		ClosedAt:    &now,
	}

	result := &SquashResult{
		MoleculeID:    root.ID,
		SquashedIDs:   childIDs,
		SquashedCount: len(children),
		KeptChildren:  keepChildren,
	}

	// All squash operations in a single transaction for atomicity (bd-4kgbq):
	// digest creation, child deletion, and root close
	err := transact(ctx, s, fmt.Sprintf("bd: squash molecule %s", root.ID), func(tx storage.Transaction) error {
		// Create digest issue
		if err := tx.CreateIssue(ctx, digestIssue, actorName); err != nil {
			return fmt.Errorf("failed to create digest issue: %w", err)
		}
		result.DigestID = digestIssue.ID

		// Link digest to root as parent-child
		dep := &types.Dependency{
			IssueID:     digestIssue.ID,
			DependsOnID: root.ID,
			Type:        types.DepParentChild,
		}
		if err := tx.AddDependency(ctx, dep, actorName); err != nil {
			return fmt.Errorf("failed to link digest to root: %w", err)
		}

		// Delete ephemeral children within the same transaction
		if !keepChildren {
			for _, id := range childIDs {
				if err := tx.DeleteIssue(ctx, id); err != nil {
					return fmt.Errorf("failed to delete child %s: %w", id, err)
				}
				result.DeletedCount++
			}
		} else {
			// --keep-children: promote each preserved child to persistent by
			// clearing its Wisp/Ephemeral flag (beads-ho61c). The help promises
			// squash "promotes the wisps to persistent by clearing their Wisp
			// flag"; the default path fulfills that by deleting, but the
			// preserve path previously ONLY skipped deletion — it never cleared
			// the flag, so kept children stayed ephemeral=true and a later
			// `bd mol wisp gc` silently reaped the very trace the user asked to
			// keep (data-loss worse than the explicit default delete). Uses the
			// same storage seam + actorName as the root-clear below, inside the
			// one atomic transact (bd-4kgbq).
			for _, id := range childIDs {
				if err := tx.UpdateIssue(ctx, id, map[string]interface{}{"wisp": false}, actorName); err != nil {
					return fmt.Errorf("failed to clear ephemeral flag on kept child %s: %w", id, err)
				}
			}
		}

		// Auto-close the root if it's a wisp — squash completes the molecule lifecycle
		if root.Ephemeral {
			reason := fmt.Sprintf("Squashed: %d steps → digest %s", len(children), result.DigestID)
			if err := tx.CloseIssue(ctx, root.ID, reason, actorName, ""); err != nil {
				return fmt.Errorf("failed to close wisp root %s: %w", root.ID, err)
			}
			// Clear ephemeral so the closed root stops being re-emitted by every wisp-table export cycle.
			if err := tx.UpdateIssue(ctx, root.ID, map[string]interface{}{"wisp": false}, actorName); err != nil {
				return fmt.Errorf("failed to clear ephemeral flag on root %s: %w", root.ID, err)
			}
			result.WispSquash = true
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return result, nil
}

func init() {
	molSquashCmd.Flags().Bool("dry-run", false, "Preview what would be squashed")
	molSquashCmd.Flags().Bool("keep-children", false, "Don't delete ephemeral children after squash")
	molSquashCmd.Flags().String("summary", "", "Agent-provided summary (bypasses auto-generation)")

	molCmd.AddCommand(molSquashCmd)
}
