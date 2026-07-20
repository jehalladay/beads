package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var duplicateCmd = &cobra.Command{
	Use:     "duplicate <id> --of <canonical>",
	GroupID: "deps",
	Short:   "Mark an issue as a duplicate of another",
	Long: `Mark an issue as a duplicate of a canonical issue.

The duplicate issue is automatically closed with a reference to the canonical.
This is essential for large issue databases with many similar reports.

Examples:
  bd duplicate bd-abc --of bd-xyz    # Mark bd-abc as duplicate of bd-xyz`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runDuplicate,
}

var supersedeCmd = &cobra.Command{
	Use:     "supersede <id> --with <new>",
	GroupID: "deps",
	Short:   "Mark an issue as superseded by a newer one",
	Long: `Mark an issue as superseded by a newer version.

The superseded issue is automatically closed with a reference to the replacement.
Useful for design docs, specs, and evolving artifacts.

Examples:
  bd supersede bd-old --with bd-new    # Mark bd-old as superseded by bd-new`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runSupersede,
}

var (
	duplicateOf    string
	supersededWith string
)

func init() {
	duplicateCmd.Flags().StringVar(&duplicateOf, "of", "", "Canonical issue ID (required)")
	_ = duplicateCmd.MarkFlagRequired("of") // Only fails if flag missing (caught in tests)
	duplicateCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(duplicateCmd)

	supersedeCmd.Flags().StringVar(&supersededWith, "with", "", "Replacement issue ID (required)")
	_ = supersedeCmd.MarkFlagRequired("with") // Only fails if flag missing (caught in tests)
	supersedeCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(supersedeCmd)
}

func runDuplicate(cmd *cobra.Command, args []string) error {
	CheckReadonly("duplicate")

	evt := metrics.NewCommandEvent("duplicate")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := getRootContext()

	// beads-crys: in proxied-server mode the global store is nil; route to the
	// UOW-backed handler (which stages the edge + close on one tx, preserving
	// njnw atomicity) instead of nil-panicking.
	if usesProxiedServer() {
		return runDuplicateProxiedServer(ctx, args[0], duplicateOf)
	}

	store := getStore()
	actor := getActor()

	// Resolve partial IDs
	var duplicateID, canonicalID string
	var err error
	duplicateID, err = utils.ResolvePartialID(ctx, store, args[0])
	if err != nil {
		return HandleErrorRespectJSON("failed to resolve %s: %v", args[0], err)
	}
	canonicalID, err = utils.ResolvePartialID(ctx, store, duplicateOf)
	if err != nil {
		return HandleErrorRespectJSON("failed to resolve %s: %v", duplicateOf, err)
	}

	if duplicateID == canonicalID {
		return HandleErrorRespectJSON("cannot mark an issue as duplicate of itself")
	}

	// Verify canonical issue exists
	var canonical *types.Issue
	canonical, err = store.GetIssue(ctx, canonicalID)
	if err != nil || canonical == nil {
		return HandleErrorRespectJSON("canonical issue not found: %s", canonicalID)
	}

	// beads-wqrfi: reject marking an issue as a duplicate of a canonical that is
	// ITSELF a closed duplicate — this prevents both a dup-of-a-dup CHAIN (LEAF →
	// MID → ROOT, where MID is closed-as-dup, leaving LEAF pointed at a dead
	// canonical instead of the live ROOT) and a mutual CYCLE (A dup-of B, then B
	// dup-of A, leaving both closed and naming each other so tracing loops
	// forever). The prior guards were only self-ref + existence. The tell that a
	// canonical is itself a duplicate: it is closed AND has an outgoing
	// "duplicates" edge. A normally-closed non-duplicate issue is still a valid
	// canonical (unchanged). Symmetric with dep add's blocks-cycle guard, which
	// is type-scoped to blocking deps and so misses the duplicates edge.
	if canonical.Status == types.StatusClosed {
		canonicalDeps, derr := store.GetDependenciesWithMetadata(ctx, canonicalID)
		if derr != nil {
			return HandleErrorRespectJSON("checking canonical %s: %v", canonicalID, derr)
		}
		for _, d := range canonicalDeps {
			if d.DependencyType == types.DepDuplicates {
				return HandleErrorRespectJSON("canonical %s is itself a closed duplicate (of %s) — mark %s as a duplicate of the live canonical instead, not a duplicate-of-a-duplicate", canonicalID, d.ID, duplicateID)
			}
		}
	}

	// Add a "duplicates" dependency edge (duplicate → canonical) AND close the
	// duplicate atomically (beads-njnw): a mid-sequence failure must not leave
	// the edge added while the issue stays open.
	dep := &types.Dependency{
		IssueID:     duplicateID,
		DependsOnID: canonicalID,
		Type:        types.DepDuplicates,
	}
	if err := store.LinkAndClose(ctx, dep, actor); err != nil {
		return HandleErrorRespectJSON("failed to mark duplicate: %v", err)
	}

	commandDidWrite.Store(true)

	if isJSONOutput() {
		return outputJSON(map[string]interface{}{
			"duplicate": duplicateID,
			"canonical": canonicalID,
			"status":    "closed",
		})
	}

	fmt.Printf("%s Marked %s as duplicate of %s (closed)\n", ui.RenderPass("✓"), duplicateID, canonicalID)
	return nil
}

func runSupersede(cmd *cobra.Command, args []string) error {
	CheckReadonly("supersede")

	evt := metrics.NewCommandEvent("supersede")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := getRootContext()

	// beads-crys: in proxied-server mode the global store is nil; route to the
	// UOW-backed handler instead of nil-panicking.
	if usesProxiedServer() {
		return runSupersedeProxiedServer(ctx, args[0], supersededWith)
	}

	store := getStore()
	actor := getActor()

	// Resolve partial IDs
	var oldID, newID string
	var err error
	oldID, err = utils.ResolvePartialID(ctx, store, args[0])
	if err != nil {
		return HandleErrorRespectJSON("failed to resolve %s: %v", args[0], err)
	}
	newID, err = utils.ResolvePartialID(ctx, store, supersededWith)
	if err != nil {
		return HandleErrorRespectJSON("failed to resolve %s: %v", supersededWith, err)
	}

	if oldID == newID {
		return HandleErrorRespectJSON("cannot mark an issue as superseded by itself")
	}

	// Verify new issue exists
	var newIssue *types.Issue
	newIssue, err = store.GetIssue(ctx, newID)
	if err != nil || newIssue == nil {
		return HandleErrorRespectJSON("replacement issue not found: %s", newID)
	}

	// beads-02v2k: reject a supersede MUTUAL cycle (A superseded-by B, then B
	// superseded-by A) — that closes both issues each naming the other as its
	// live successor, so a "superseded by" tracer loops forever with no live
	// replacement. Tell: the replacement (newID) already has an outgoing
	// "supersedes" edge back to oldID. This is a NARROW reciprocal-edge check at
	// the supersede seam only — it deliberately does NOT touch cycleCheckTypesFor
	// (the DepSupersedes exclusion is eng_2/dfzre's deliberate contract, so a
	// legitimate acyclic version chain v1→v2→v3 stays legal: v3 has no back-edge
	// to v2). The forward general-cycle case via `dep add --type supersedes` is a
	// separate gap tracked as its own bead against the contract owner.
	newDeps, derr := store.GetDependenciesWithMetadata(ctx, newID)
	if derr != nil {
		return HandleErrorRespectJSON("checking replacement %s: %v", newID, derr)
	}
	for _, d := range newDeps {
		if d.DependencyType == types.DepSupersedes && d.ID == oldID {
			return HandleErrorRespectJSON("%s is already superseded by %s — marking %s as superseded by %s would create a supersede cycle (neither has a live successor)", newID, oldID, oldID, newID)
		}
	}

	// beads-pmaud: reject re-superseding an issue that ALREADY has a live
	// successor by a DIFFERENT target. LinkAndClose is idempotent for an
	// identical (oldID -supersedes-> newID) edge, so a same-target re-supersede
	// correctly dedups to one edge — but a DIFFERENT newID would UNCONDITIONALLY
	// add a SECOND outgoing supersedes edge, leaving oldID with two live
	// successors ("superseded by [C D]"). That violates the single-canonical-
	// replacement invariant the supersede/reopen tracers assume (reopen.go
	// supersededByTargets + its guard are written for ONE target; the plural in
	// the reopen error is the visible tell). 02v2k above covers only the mutual
	// reciprocal cycle; this is the uncovered multiple-live-successors sibling.
	// Same-target → idempotent no-op notice (mirror close.go's beads-dr3 /
	// gate add-waiter pattern: rc0, reflect the STORED target); different-target
	// → reject and point the operator at the existing link.
	oldDeps, derr := store.GetDependenciesWithMetadata(ctx, oldID)
	if derr != nil {
		return HandleErrorRespectJSON("checking %s: %v", oldID, derr)
	}
	for _, d := range oldDeps {
		if d.DependencyType != types.DepSupersedes {
			continue
		}
		if d.ID == newID {
			// Idempotent: already superseded by exactly this target. No second
			// write, no false "✓ Marked ..." transition glyph.
			if isJSONOutput() {
				return outputJSON(map[string]interface{}{
					"superseded":  oldID,
					"replacement": newID,
					"status":      "closed",
				})
			}
			fmt.Printf("%s %s is already superseded by %s (no change)\n", ui.RenderInfoIcon(), oldID, newID)
			return nil
		}
		return HandleErrorRespectJSON("%s is already superseded by %s — remove the existing supersedes link or reopen %s first (a second replacement would leave %s with multiple live successors)", oldID, d.ID, oldID, oldID)
	}

	// Add a "supersedes" dependency edge (old → new) AND close the superseded
	// issue atomically (beads-njnw): a mid-sequence failure must not leave the
	// edge added while the issue stays open.
	dep := &types.Dependency{
		IssueID:     oldID,
		DependsOnID: newID,
		Type:        types.DepSupersedes,
	}
	if err := store.LinkAndClose(ctx, dep, actor); err != nil {
		return HandleErrorRespectJSON("failed to mark superseded: %v", err)
	}

	commandDidWrite.Store(true)

	if isJSONOutput() {
		return outputJSON(map[string]interface{}{
			"superseded":  oldID,
			"replacement": newID,
			"status":      "closed",
		})
	}

	fmt.Printf("%s Marked %s as superseded by %s (closed)\n", ui.RenderPass("✓"), oldID, newID)
	return nil
}
