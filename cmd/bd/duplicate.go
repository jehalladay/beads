package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

// isMigratableSupersedeEdge reports whether an incoming dependency edge on a
// superseded issue must be re-pointed to the replacement (beads-0c9d1). These
// are the STRUCTURAL edge types the ready/blocked + tree engines act on
// (issueops.AffectedByDepChangeInTx): a blocking edge left on the closed source
// silently unblocks its dependent, and a parent-child edge orphans structure.
// Provenance / knowledge edges (related, relates-to, duplicates, supersedes,
// discovered-from, tracks, …) are intentionally excluded — they legitimately
// keep referring to the historical source.
func isMigratableSupersedeEdge(t types.DependencyType) bool {
	switch t {
	case types.DepBlocks, types.DepConditionalBlocks, types.DepWaitsFor, types.DepParentChild:
		return true
	default:
		return false
	}
}

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

	// beads-cjl9y (duplicate-side twin of beads-pmaud): reject re-marking an issue
	// that ALREADY has a live canonical by a DIFFERENT target. LinkAndClose is
	// idempotent for an identical (duplicateID -duplicates-> canonicalID) edge, so
	// a same-target re-duplicate correctly dedups to one edge — but a DIFFERENT
	// canonicalID would UNCONDITIONALLY add a SECOND outgoing duplicates edge,
	// leaving duplicateID a duplicate of two live canonicals ("duplicate of [C D]").
	// That makes "duplicate of" ambiguous and can resurface duplicateID via either
	// canonical; reopen/tracer logic (reopen.go duplicatesTargets, beads-8nugc)
	// assumes ≤1 live duplicates edge. The wqrfi guard above only rejects a
	// canonical that is ITSELF a closed duplicate; this is the uncovered
	// multiple-live-canonicals sibling on the source side. Same-target → idempotent
	// no-op notice (mirror close.go's beads-dr3 / the pmaud supersede guard: rc0,
	// reflect the STORED target); different-target → reject and point the operator
	// at the existing link.
	dupDeps, derr := store.GetDependenciesWithMetadata(ctx, duplicateID)
	if derr != nil {
		return HandleErrorRespectJSON("checking %s: %v", duplicateID, derr)
	}
	for _, d := range dupDeps {
		if d.DependencyType != types.DepDuplicates {
			continue
		}
		if d.ID == canonicalID {
			// Idempotent: already a duplicate of exactly this canonical. No second
			// write, no false "✓ Marked ..." transition glyph.
			if isJSONOutput() {
				return outputJSON(map[string]interface{}{
					"duplicate": duplicateID,
					"canonical": canonicalID,
					"status":    "closed",
				})
			}
			fmt.Printf("%s %s is already a duplicate of %s (no change)\n", ui.RenderInfoIcon(), duplicateID, canonicalID)
			return nil
		}
		return HandleErrorRespectJSON("%s is already a duplicate of %s — remove the existing duplicates link or reopen %s first (a second canonical would leave %s a duplicate of multiple live issues)", duplicateID, d.ID, duplicateID, duplicateID)
	}

	// beads-r3m8v: capture the source's pre-close status for the GC-survivable
	// audit-file trail emitted after the close. The guards above load deps but
	// not the source issue itself; a status audit entry must record the real
	// old→new transition (mirrors close.go:213, which reads issue.Status).
	dupPre, _ := store.GetIssue(ctx, duplicateID)
	dupOldStatus := "open"
	if dupPre != nil {
		dupOldStatus = string(dupPre.Status)
	}

	// beads-d5q19: migrate the duplicate's incoming STRUCTURAL edges (blocks /
	// conditional-blocks / waits-for / parent-child — the set
	// AffectedByDepChangeInTx acts on) to the canonical BEFORE closing it, at
	// parity with runSupersede (beads-0c9d1) and `bd duplicates --auto-merge`
	// (beads-706mw/zcq86, which transfers blocking edges to the canonical). The
	// canonical is the live successor of the duplicate, exactly as the
	// replacement is of the superseded source; without this a dependent
	// blocked-BY the duplicate silently UNBLOCKS when the duplicate closes (the
	// ready/blocked engine treats the closed source as satisfied) even though the
	// canonical is not done → premature-actionable, and a child parented to the
	// duplicate orphans onto the closed source instead of reparenting to the
	// canonical. Provenance / knowledge edges (related / relates-to / duplicates /
	// supersedes / discovered-from / tracks / …) are intentionally NOT migrated —
	// they legitimately keep pointing at the historical source.
	//
	// The migration + duplicates edge + close run inside ONE transaction,
	// preserving the beads-njnw atomicity (a mid-sequence failure must not leave
	// the edge added or an incoming edge re-pointed while the issue stays open).
	// The transaction seam also auto-routes wisp sources (pega7) and, via
	// HookFiringStore.RunInTransaction, fires on_close after commit (usumn) — so
	// the separate post-close EventClose leg the pre-atomic path needed is no
	// longer required here (matching runSupersede).
	incoming, incErr := store.GetDependentsWithMetadata(ctx, duplicateID)
	if incErr != nil {
		return HandleErrorRespectJSON("failed to read dependents of %s: %v", duplicateID, incErr)
	}

	commitMsg := fmt.Sprintf("bd: duplicate %s of %s", duplicateID, canonicalID)
	txErr := transact(ctx, store, commitMsg, func(tx storage.Transaction) error {
		// Migrate incoming structural edges duplicate → canonical. Re-point each
		// dependent from the duplicate to the canonical for the same edge type
		// before the close, so the dependent tracks the live canonical instead of
		// the closed source.
		for _, dep := range incoming {
			if !isMigratableSupersedeEdge(dep.DependencyType) {
				continue
			}
			dependentID := dep.Issue.ID
			// A self-edge to the canonical would be created if the dependent IS the
			// canonical; skip so the canonical never depends on / parents itself.
			if dependentID == canonicalID {
				continue
			}
			if err := tx.RemoveDependency(ctx, dependentID, duplicateID, actor); err != nil {
				return fmt.Errorf("remove incoming %s %s→%s: %w", dep.DependencyType, dependentID, duplicateID, err)
			}
			migrated := &types.Dependency{
				IssueID:     dependentID,
				DependsOnID: canonicalID,
				Type:        dep.DependencyType,
			}
			if err := tx.AddDependency(ctx, migrated, actor); err != nil {
				return fmt.Errorf("reattach incoming %s %s→%s: %w", dep.DependencyType, dependentID, canonicalID, err)
			}
		}

		// Add the "duplicates" dependency edge (duplicate → canonical).
		dupEdge := &types.Dependency{
			IssueID:     duplicateID,
			DependsOnID: canonicalID,
			Type:        types.DepDuplicates,
		}
		if err := tx.AddDependency(ctx, dupEdge, actor); err != nil {
			return fmt.Errorf("link duplicates %s→%s: %w", duplicateID, canonicalID, err)
		}

		// Close the duplicate source (preserving a meaningful reason). The
		// hook-tracking tx records the on_close so it fires after commit.
		if err := tx.CloseIssue(ctx, duplicateID, fmt.Sprintf("duplicate of %s", canonicalID), actor, ""); err != nil {
			return fmt.Errorf("close duplicate %s: %w", duplicateID, err)
		}
		return nil
	})
	if txErr != nil {
		return HandleErrorRespectJSON("failed to mark duplicate: %v", txErr)
	}

	commandDidWrite.Store(true)

	// beads-r3m8v: the transaction seam closes via CloseIssueWithoutEventInTx,
	// whose DB EventClosed row does not survive a Dolt GC flatten. bd close/update
	// write a GC-survivable audit-FILE entry (.beads/interactions.jsonl); emit the
	// same status field_change so a duplicated issue's close stays in the durable
	// record (the tx already committed the close durably). dupOldStatus records
	// the real prior status; the guards above reject re-linking an already-linked
	// source, so reaching here is a genuine open→closed transition.
	auditStatusChange(duplicateID, dupOldStatus, "closed", actor, fmt.Sprintf("duplicate of %s", canonicalID))

	// beads-26gea: duplicating closes the source via the transaction seam,
	// bypassing the cmd-layer completed-molecule auto-close cascade `bd close`
	// runs (same class as the supersede leg below and beads-zzp26). Run it
	// post-close so duplicating a molecule's FINAL step auto-closes the completed
	// root. Identical function bd close/batch/update use, so completion detection
	// can't drift.
	autoCloseCompletedMolecule(ctx, store, duplicateID, actor, "")

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

	// beads-r3m8v: capture the source's pre-close status for the GC-survivable
	// audit-file trail emitted after the close (see the duplicate leg above).
	oldPre, _ := store.GetIssue(ctx, oldID)
	oldOldStatus := "open"
	if oldPre != nil {
		oldOldStatus = string(oldPre.Status)
	}

	// beads-0c9d1: supersede's contract is "newID replaces oldID", so the
	// dependents of oldID must FOLLOW the replacement — not evaporate onto the
	// closed source. The pre-fix path called store.LinkAndClose, which added the
	// supersedes edge + closed oldID but migrated NO incoming edges. That left
	// every edge incident on the now-CLOSED oldID dangling while newID (which
	// inherits oldID's work) stays OPEN:
	//   • an incoming blocks/waits-for edge → the ready/blocked engine treats the
	//     closed blocker as satisfied → the dependent silently UNBLOCKS even
	//     though the real successor newID is not done (premature-actionable).
	//   • an incoming parent-child edge → the child dangles on the closed oldID,
	//     never reparenting to newID → newID has no children, structure lost.
	// Fix: inside ONE transaction (preserving beads-njnw atomicity — a
	// mid-sequence failure must not leave the edge added while the issue stays
	// open) migrate oldID's incoming STRUCTURAL edges (blocks / conditional-
	// blocks / waits-for / parent-child — the set AffectedByDepChangeInTx acts
	// on) to newID, then add the supersedes edge and close oldID. Provenance /
	// knowledge edges (related / relates-to / duplicates / supersedes /
	// discovered-from / etc.) are deliberately NOT migrated — they legitimately
	// point at the historical source (a "related to the old spec" note should
	// stay about the old spec). This mirrors performMerge's per-source reparent
	// (beads-zcq86), extended from parent-child-only to all blocking edge types.
	// The transaction seam auto-routes wisp sources (pega7) and, via
	// HookFiringStore.RunInTransaction, fires on_close after commit (usumn) — so
	// the separate post-close EventClose leg is no longer needed here.
	incoming, incErr := store.GetDependentsWithMetadata(ctx, oldID)
	if incErr != nil {
		return HandleErrorRespectJSON("failed to read dependents of %s: %v", oldID, incErr)
	}

	commitMsg := fmt.Sprintf("bd: supersede %s with %s", oldID, newID)
	txErr := transact(ctx, store, commitMsg, func(tx storage.Transaction) error {
		// Migrate incoming structural edges oldID → newID. Re-point each
		// dependent from oldID to newID for the same edge type before the close,
		// so the dependent tracks the live replacement instead of the closed
		// source.
		for _, dep := range incoming {
			if !isMigratableSupersedeEdge(dep.DependencyType) {
				continue
			}
			dependentID := dep.Issue.ID
			// A self-edge to newID would be created if the dependent IS newID;
			// skip so newID never depends on / parents itself.
			if dependentID == newID {
				continue
			}
			if err := tx.RemoveDependency(ctx, dependentID, oldID, actor); err != nil {
				return fmt.Errorf("remove incoming %s %s→%s: %w", dep.DependencyType, dependentID, oldID, err)
			}
			migrated := &types.Dependency{
				IssueID:     dependentID,
				DependsOnID: newID,
				Type:        dep.DependencyType,
			}
			if err := tx.AddDependency(ctx, migrated, actor); err != nil {
				return fmt.Errorf("reattach incoming %s %s→%s: %w", dep.DependencyType, dependentID, newID, err)
			}
		}

		// Add the "supersedes" dependency edge (old → new).
		superEdge := &types.Dependency{
			IssueID:     oldID,
			DependsOnID: newID,
			Type:        types.DepSupersedes,
		}
		if err := tx.AddDependency(ctx, superEdge, actor); err != nil {
			return fmt.Errorf("link supersedes %s→%s: %w", oldID, newID, err)
		}

		// Close the superseded source (preserving a meaningful reason). The
		// hook-tracking tx records the on_close so it fires after commit.
		if err := tx.CloseIssue(ctx, oldID, fmt.Sprintf("superseded by %s", newID), actor, ""); err != nil {
			return fmt.Errorf("close superseded %s: %w", oldID, err)
		}
		return nil
	})
	if txErr != nil {
		return HandleErrorRespectJSON("failed to mark superseded: %v", txErr)
	}

	commandDidWrite.Store(true)

	// beads-r3m8v: the transaction seam closes via CloseIssueWithoutEventInTx,
	// whose DB EventClosed row does not survive a Dolt GC flatten. bd close/update
	// write a GC-survivable audit-FILE entry (.beads/interactions.jsonl); emit the
	// same status field_change so a superseded issue's close stays in the durable
	// record (the tx already committed the close durably).
	auditStatusChange(oldID, oldOldStatus, "closed", actor, fmt.Sprintf("superseded by %s", newID))

	// beads-26gea: superseding closes the source via the transaction seam,
	// bypassing the cmd-layer completed-molecule auto-close cascade `bd close`
	// runs (same class as the duplicate leg above and beads-zzp26). Run it
	// post-close so superseding a molecule's FINAL step auto-closes the completed
	// root.
	autoCloseCompletedMolecule(ctx, store, oldID, actor, "")

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
