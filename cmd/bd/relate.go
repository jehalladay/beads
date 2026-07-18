package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var relateCmd = &cobra.Command{
	Use:   "relate <id1> <id2>",
	Short: "Create a bidirectional relates_to link between issues",
	Long: `Create a loose 'see also' relationship between two issues.

The relates_to link is bidirectional - both issues will reference each other.
This enables knowledge graph connections without blocking or hierarchy.

Examples:
  bd relate bd-abc bd-xyz    # Link two related issues
  bd relate bd-123 bd-456    # Create see-also connection`,
	Args: cobra.ExactArgs(2),
	RunE: runRelate,
}

var unrelateCmd = &cobra.Command{
	Use:   "unrelate <id1> <id2>",
	Short: "Remove a relates_to link between issues",
	Long: `Remove a relates_to relationship between two issues.

Removes the link in both directions.

Example:
  bd unrelate bd-abc bd-xyz`,
	Args: cobra.ExactArgs(2),
	RunE: runUnrelate,
}

func init() {
	// Issue ID completions
	relateCmd.ValidArgsFunction = issueIDCompletion
	unrelateCmd.ValidArgsFunction = issueIDCompletion

	// Add as subcommands of dep
	depCmd.AddCommand(relateCmd)
	depCmd.AddCommand(unrelateCmd)
}

func runRelate(cmd *cobra.Command, args []string) error {
	CheckReadonly("relate")

	// beads-1zuh: route to the proxied handler in proxied-server mode. Without
	// this, runRelate uses the direct global `store` — which is nil under
	// proxiedServerMode (PersistentPreRunE returns early before store init) —
	// so `bd dep relate` failed "storage is nil" for every hub-connected crew,
	// unlike every other dep/write verb which routes via usesProxiedServer().
	if usesProxiedServer() {
		return runRelateProxiedServer(rootCtx, args)
	}

	evt := metrics.NewCommandEvent("relate")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	// Resolve partial IDs
	var id1, id2 string
	var err error
	id1, err = utils.ResolvePartialID(ctx, store, args[0])
	if err != nil {
		return HandleErrorRespectJSON("failed to resolve %s: %v", args[0], err)
	}
	id2, err = utils.ResolvePartialID(ctx, store, args[1])
	if err != nil {
		return HandleErrorRespectJSON("failed to resolve %s: %v", args[1], err)
	}

	if id1 == id2 {
		return HandleErrorRespectJSON("cannot relate an issue to itself")
	}

	// Get both issues
	var issue1, issue2 *types.Issue
	issue1, err = store.GetIssue(ctx, id1)
	if err != nil {
		return HandleErrorRespectJSON("failed to get issue %s: %v", id1, err)
	}
	issue2, err = store.GetIssue(ctx, id2)
	if err != nil {
		return HandleErrorRespectJSON("failed to get issue %s: %v", id2, err)
	}

	if issue1 == nil {
		return HandleErrorRespectJSON("issue not found: %s", id1)
	}
	if issue2 == nil {
		return HandleErrorRespectJSON("issue not found: %s", id2)
	}

	// beads-57nt: AddDependency is idempotent, so re-relating an already-related
	// pair would print "✓ Linked" as if it changed something. Report an honest
	// "already related, no change" (rc=0 — a benign no-op, the relate-side
	// sibling of the unrelate fix beads-piud above and the bwla dep-add re-add
	// case), reusing the same relatesToLinkExists helper the unrelate path uses.
	if alreadyRelated, checkErr := relatesToLinkExists(ctx, id1, id2); checkErr == nil && alreadyRelated {
		if jsonOutput {
			result := map[string]interface{}{"id1": id1, "id2": id2, "related": true, "unchanged": true}
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(result)
		}
		fmt.Printf("%s Already related, no change: %s ↔ %s\n", ui.RenderPass("✓"), id1, id2)
		return nil
	}

	// Add relates-to dependency: id1 -> id2 (bidirectional, so also id2 -> id1)
	// Per Decision 004, relates-to links are now stored in dependencies table.
	// Both directions are written in ONE transaction so the relation is
	// atomic — a mid-op failure can never leave a half (asymmetric) relation
	// where id1->id2 exists but id2->id1 doesn't. (beads-oyy1)
	dep1 := &types.Dependency{
		IssueID:     id1,
		DependsOnID: id2,
		Type:        types.DepRelatesTo,
	}
	dep2 := &types.Dependency{
		IssueID:     id2,
		DependsOnID: id1,
		Type:        types.DepRelatesTo,
	}
	if err := store.RunInTransaction(ctx, fmt.Sprintf("bd: relate %s <-> %s", id1, id2), func(tx storage.Transaction) error {
		if err := tx.AddDependency(ctx, dep1, actor); err != nil {
			return fmt.Errorf("failed to add relates-to %s -> %s: %w", id1, id2, err)
		}
		if err := tx.AddDependency(ctx, dep2, actor); err != nil {
			return fmt.Errorf("failed to add relates-to %s -> %s: %w", id2, id1, err)
		}
		return nil
	}); err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	if jsonOutput {
		result := map[string]interface{}{
			"id1":     id1,
			"id2":     id2,
			"related": true,
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Printf("%s Linked %s ↔ %s\n", ui.RenderPass("✓"), id1, id2)
	return nil
}

func runUnrelate(cmd *cobra.Command, args []string) error {
	CheckReadonly("unrelate")

	// beads-1zuh: route to the proxied handler in proxied-server mode (see runRelate).
	if usesProxiedServer() {
		return runUnrelateProxiedServer(rootCtx, args)
	}

	evt := metrics.NewCommandEvent("unrelate")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	// Resolve partial IDs
	var id1, id2 string
	var err error
	id1, err = utils.ResolvePartialID(ctx, store, args[0])
	if err != nil {
		return HandleErrorRespectJSON("failed to resolve %s: %v", args[0], err)
	}
	id2, err = utils.ResolvePartialID(ctx, store, args[1])
	if err != nil {
		return HandleErrorRespectJSON("failed to resolve %s: %v", args[1], err)
	}

	// Get both issues
	var issue1, issue2 *types.Issue
	issue1, err = store.GetIssue(ctx, id1)
	if err != nil {
		return HandleErrorRespectJSON("failed to get issue %s: %v", id1, err)
	}
	issue2, err = store.GetIssue(ctx, id2)
	if err != nil {
		return HandleErrorRespectJSON("failed to get issue %s: %v", id2, err)
	}

	if issue1 == nil {
		return HandleErrorRespectJSON("issue not found: %s", id1)
	}
	if issue2 == nil {
		return HandleErrorRespectJSON("issue not found: %s", id2)
	}

	// Pre-check that a relates-to link actually exists (in EITHER direction) so
	// we report honestly (beads-piud, w2tk-class). RemoveDependency is idempotent
	// — it returns nil whether or not an edge was removed (the storage layer
	// detects the no-op but discards the bit at the interface, and other callers
	// rely on idempotent cleanup) — so without this check `bd unrelate A B` on a
	// never-related pair printed "✓ Unlinked" / unrelated:true, a false-success
	// that a CI/agent gate reads as proof the link is gone. Scope the check to
	// the relates-to TYPE so a blocks/parent edge between the same pair doesn't
	// mask a missing relates-to link. Keep RemoveDependency idempotent for
	// programmatic callers; only the CLI verb reports the distinction.
	linkExists, checkErr := relatesToLinkExists(ctx, id1, id2)
	if checkErr != nil {
		return HandleErrorRespectJSON("checking relates-to link %s <-> %s: %v", id1, id2, checkErr)
	}
	if !linkExists {
		return HandleErrorRespectJSON("no relates-to link to remove: %s is not related to %s", id1, id2)
	}

	// Remove relates-to dependency in both directions
	// Per Decision 004, relates-to links are now stored in dependencies table.
	// Both directions are removed in ONE transaction so unrelate is atomic —
	// a mid-op failure can never leave a half relation where one direction is
	// removed but the reciprocal lingers. (beads-oyy1, mirror of relate)
	if err := store.RunInTransaction(ctx, fmt.Sprintf("bd: unrelate %s <-> %s", id1, id2), func(tx storage.Transaction) error {
		if err := tx.RemoveDependency(ctx, id1, id2, actor); err != nil {
			return fmt.Errorf("failed to remove relates-to %s -> %s: %w", id1, id2, err)
		}
		if err := tx.RemoveDependency(ctx, id2, id1, actor); err != nil {
			return fmt.Errorf("failed to remove relates-to %s -> %s: %w", id2, id1, err)
		}
		return nil
	}); err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	if jsonOutput {
		result := map[string]interface{}{
			"id1":       id1,
			"id2":       id2,
			"unrelated": true,
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Printf("%s Unlinked %s ↔ %s\n", ui.RenderPass("✓"), id1, id2)
	return nil
}

// relatesToLinkExists reports whether a relates-to dependency edge exists
// between id1 and id2 in EITHER direction. Used by unrelate to distinguish a
// real removal from a no-op (beads-piud). Scoped to types.DepRelatesTo so an
// unrelated blocks/parent edge between the same pair is not mistaken for a
// relates-to link.
func relatesToLinkExists(ctx context.Context, id1, id2 string) (bool, error) {
	recs, err := store.GetDependencyRecords(ctx, id1)
	if err != nil {
		return false, err
	}
	for _, rec := range recs {
		if rec != nil && rec.DependsOnID == id2 && rec.Type == types.DepRelatesTo {
			return true, nil
		}
	}
	// Check the reciprocal direction too: a half-removed link (only one
	// direction present) should still be treated as removable, not a no-op.
	recs, err = store.GetDependencyRecords(ctx, id2)
	if err != nil {
		return false, err
	}
	for _, rec := range recs {
		if rec != nil && rec.DependsOnID == id1 && rec.Type == types.DepRelatesTo {
			return true, nil
		}
	}
	return false, nil
}

// Note: contains, remove, formatRelatesTo functions removed per Decision 004
// relates-to links now use dependencies API instead of Issue.RelatesTo field
