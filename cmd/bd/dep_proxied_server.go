package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

func openDepProxiedUOW(ctx context.Context) uow.UnitOfWork {
	if uowProvider == nil {
		FatalErrorRespectJSON("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		FatalErrorRespectJSON("open unit of work: %v", err)
	}
	return uw
}

func proxiedLookupTitle(ctx context.Context, uw uow.UnitOfWork, id string) string {
	if IsExternalRef(id) {
		return ""
	}
	issue, err := uw.IssueUseCase().GetIssue(ctx, id)
	if err == nil && issue != nil {
		return issue.Title
	}
	wisp, err := uw.IssueUseCase().GetWisp(ctx, id)
	if err == nil && wisp != nil {
		return wisp.Title
	}
	return ""
}

// proxiedDepEdgeExistsSameType reports whether a fromID -> toID edge of the
// given type already exists (beads-epuz). Mirrors the direct path's bwla
// precheck: AddDependencies is idempotent (a same-type re-add just refreshes
// metadata), so without this the proxied verb printed a false "✓ Added" on a
// no-op. A lookup error falls through (returns false) so the normal add path
// still runs — best-effort, never blocks (matching the direct path).
func proxiedDepEdgeExistsSameType(ctx context.Context, uw uow.UnitOfWork, fromID, toID string, dt types.DependencyType) bool {
	recs, err := uw.DependencyUseCase().GetIssueDependencyRecords(ctx, []string{fromID})
	if err != nil {
		return false
	}
	for _, rec := range recs[fromID] {
		if rec != nil && rec.DependsOnID == toID && rec.Type == dt {
			return true
		}
	}
	return false
}

func proxiedWarnCycles(ctx context.Context, uw uow.UnitOfWork) {
	cycles, err := uw.DependencyUseCase().DetectCycles(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to check for cycles: %v\n", err)
		return
	}
	if len(cycles) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\n%s Warning: Dependency cycle detected!\n", ui.RenderWarn("⚠"))
	fmt.Fprintf(os.Stderr, "This can hide issues from the ready work list and cause confusion.\n\n")
	fmt.Fprintf(os.Stderr, "Cycle path:\n")
	for _, cycle := range cycles {
		for j, issue := range cycle {
			if j == 0 {
				fmt.Fprintf(os.Stderr, "  %s", issue.ID)
			} else {
				fmt.Fprintf(os.Stderr, " → %s", issue.ID)
			}
		}
		if len(cycle) > 0 {
			fmt.Fprintf(os.Stderr, " → %s", cycle[0].ID)
		}
		fmt.Fprintf(os.Stderr, "\n")
	}
	fmt.Fprintf(os.Stderr, "\nRun 'bd dep cycles' for detailed analysis.\n\n")
}

func runDepBlocksProxiedServer(cmd *cobra.Command, ctx context.Context, blockerID, blockedID string) {
	if isChildOf(blockedID, blockerID) {
		FatalErrorRespectJSON("cannot add dependency: %s is already a child of %s. Children inherit dependency on parent completion via hierarchy. Adding an explicit dependency would create a deadlock", blockedID, blockerID)
	}

	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	// beads-epuz: bwla honest-no-op parity for the --blocks shorthand too. The
	// blocks edge is blockedID -> blockerID (type blocks); a same-type re-add is
	// idempotent, so guard against a false "✓ Added".
	if proxiedDepEdgeExistsSameType(ctx, uw, blockedID, blockerID, types.DepBlocks) {
		if jsonOutput {
			// beads-xcujl: align --blocks vocabulary with `dep add` (issue_id=
			// blocked/depending=blockedID, depends_on_id=blocker=blockerID).
			_ = outputJSON(map[string]interface{}{
				"status":        "unchanged",
				"issue_id":      blockedID,
				"depends_on_id": blockerID,
				"type":          string(types.DepBlocks),
			})
			return
		}
		fmt.Printf("%s Dependency already present, no change: %s blocks %s\n",
			ui.RenderPass("✓"),
			formatFeedbackIDParen(blockerID, proxiedLookupTitle(ctx, uw, blockerID)),
			formatFeedbackIDParen(blockedID, proxiedLookupTitle(ctx, uw, blockedID)))
		return
	}

	dep := &types.Dependency{
		IssueID:     blockedID,
		DependsOnID: blockerID,
		Type:        types.DepBlocks,
	}
	if _, err := uw.DependencyUseCase().AddDependencies(ctx, []*types.Dependency{dep}, actor, domain.BulkAddDepsOpts{}); err != nil {
		FatalErrorRespectJSON("%v", err)
	}

	noCycleCheck, _ := cmd.Flags().GetBool("no-cycle-check")
	if !noCycleCheck {
		proxiedWarnCycles(ctx, uw)
	}

	blockerTitle := proxiedLookupTitle(ctx, uw, blockerID)
	blockedTitle := proxiedLookupTitle(ctx, uw, blockedID)

	if err := uw.Commit(ctx, fmt.Sprintf("bd: dep add %s %s", blockedID, blockerID)); err != nil && !isDoltNothingToCommit(err) {
		FatalErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		// beads-xcujl: align --blocks vocabulary with `dep add` (issue_id=
		// blocked/depending=blockedID, depends_on_id=blocker=blockerID).
		_ = outputJSON(map[string]interface{}{
			"status":        "added",
			"issue_id":      blockedID,
			"depends_on_id": blockerID,
			"type":          string(types.DepBlocks),
		})
		return
	}

	fmt.Printf("%s Added dependency: %s blocks %s\n",
		ui.RenderPass("✓"),
		formatFeedbackIDParen(blockerID, blockerTitle),
		formatFeedbackIDParen(blockedID, blockedTitle))
}

func runDepAddProxiedServer(cmd *cobra.Command, ctx context.Context, args []string) {
	depType, _ := cmd.Flags().GetString("type")
	file, _ := cmd.Flags().GetString("file")

	if file != "" {
		runDepAddBulkProxied(cmd, ctx, file, depType)
		return
	}

	blockedBy, _ := cmd.Flags().GetString("blocked-by")
	dependsOn, _ := cmd.Flags().GetString("depends-on")

	var dependsOnArg string
	switch {
	case blockedBy != "":
		dependsOnArg = blockedBy
	case dependsOn != "":
		dependsOnArg = dependsOn
	default:
		dependsOnArg = args[1]
	}

	fromID := args[0]
	var toID string
	if strings.HasPrefix(dependsOnArg, "external:") {
		if err := validateExternalRef(dependsOnArg); err != nil {
			FatalErrorRespectJSON("%v", err)
		}
		toID = dependsOnArg
	} else {
		toID = dependsOnArg
	}

	if isChildOf(fromID, toID) {
		FatalErrorRespectJSON("cannot add dependency: %s is already a child of %s. Children inherit dependency on parent completion via hierarchy. Adding an explicit dependency would create a deadlock", fromID, toID)
	}

	dt := types.DependencyType(depType)
	if !dt.IsValid() {
		FatalErrorRespectJSON("invalid dependency type %q: must be non-empty and at most 32 characters", depType)
	}
	// beads-qfka: reject unknown types for parity with the direct path and
	// `bd create --deps` (both gate on IsWellKnown). iu9f un-gated the proxied
	// path, so this validation asymmetry is now live.
	if !dt.IsWellKnown() {
		FatalErrorRespectJSON("unknown dependency type %q; valid types: %s", depType, createDepsAcceptedTypeList())
	}
	// beads-hf1c6: refuse a relates-to link (bidirectional — use bd dep relate),
	// mirroring the direct path. A plain dep-add would mint a one-sided edge.
	if dt == types.DepRelatesTo {
		FatalErrorRespectJSON("cannot add a relates-to link with 'dep add' (it is bidirectional); use 'bd dep relate %s %s'", fromID, toID)
	}

	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	// beads-epuz: mirror the direct path's bwla honest-no-op report. A same-type
	// re-add is idempotent (just refreshes metadata), so an unconditional
	// "✓ Added" is a false success on a no-op. External refs are not resolvable
	// via the local dep records, so skip the precheck for them (best-effort,
	// matches the direct path's same-store-only guard).
	if !strings.HasPrefix(toID, "external:") && proxiedDepEdgeExistsSameType(ctx, uw, fromID, toID, dt) {
		if jsonOutput {
			_ = outputJSON(map[string]interface{}{
				"status":        "unchanged",
				"issue_id":      fromID,
				"depends_on_id": toID,
				"type":          depType,
			})
			return
		}
		fmt.Printf("%s Dependency already present, no change: %s depends on %s (%s)\n",
			ui.RenderPass("✓"),
			formatFeedbackIDParen(fromID, proxiedLookupTitle(ctx, uw, fromID)),
			formatFeedbackIDParen(toID, proxiedLookupTitle(ctx, uw, toID)),
			depType)
		return
	}

	dep := &types.Dependency{IssueID: fromID, DependsOnID: toID, Type: dt}
	if _, err := uw.DependencyUseCase().AddDependencies(ctx, []*types.Dependency{dep}, actor, domain.BulkAddDepsOpts{}); err != nil {
		FatalErrorRespectJSON("%v", err)
	}

	noCycleCheck, _ := cmd.Flags().GetBool("no-cycle-check")
	if !noCycleCheck {
		proxiedWarnCycles(ctx, uw)
	}

	fromTitle := proxiedLookupTitle(ctx, uw, fromID)
	toTitle := proxiedLookupTitle(ctx, uw, toID)

	if err := uw.Commit(ctx, fmt.Sprintf("bd: dep add %s %s", fromID, toID)); err != nil && !isDoltNothingToCommit(err) {
		FatalErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		_ = outputJSON(map[string]interface{}{
			"status":        "added",
			"issue_id":      fromID,
			"depends_on_id": toID,
			"type":          depType,
		})
		return
	}

	fmt.Printf("%s Added dependency: %s depends on %s (%s)\n",
		ui.RenderPass("✓"),
		formatFeedbackIDParen(fromID, fromTitle),
		formatFeedbackIDParen(toID, toTitle),
		depType)
}

func runDepAddBulkProxied(cmd *cobra.Command, ctx context.Context, file, defaultType string) {
	edges, err := readBulkDepEdges(file, defaultType)
	if err != nil {
		FatalErrorRespectJSON("%v", err)
	}
	if len(edges) == 0 {
		FatalErrorRespectJSON("no dependency edges found")
	}

	deps := make([]*types.Dependency, 0, len(edges))
	for _, edge := range edges {
		if isChildOf(edge.IssueID, edge.DependsOnID) {
			FatalErrorRespectJSON("line %d: cannot add dependency: %s is already a child of %s", edge.Line, edge.IssueID, edge.DependsOnID)
		}
		if strings.HasPrefix(edge.DependsOnID, "external:") {
			if err := validateExternalRef(edge.DependsOnID); err != nil {
				FatalErrorRespectJSON("line %d: %v", edge.Line, err)
			}
		}
		deps = append(deps, &types.Dependency{
			IssueID:     edge.IssueID,
			DependsOnID: edge.DependsOnID,
			Type:        edge.Type,
		})
	}

	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	noCycleCheck, _ := cmd.Flags().GetBool("no-cycle-check")
	if _, err := uw.DependencyUseCase().AddDependencies(ctx, deps, actor, domain.BulkAddDepsOpts{
		SkipPerEdgeCycleCheck: noCycleCheck,
	}); err != nil {
		FatalErrorRespectJSON("%v", err)
	}

	if !noCycleCheck {
		proxiedWarnCycles(ctx, uw)
	}

	if err := uw.Commit(ctx, fmt.Sprintf("dependency: add %d edges", len(deps))); err != nil && !isDoltNothingToCommit(err) {
		FatalErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		out := make([]map[string]interface{}, 0, len(deps))
		for _, dep := range deps {
			out = append(out, map[string]interface{}{
				"issue_id":      dep.IssueID,
				"depends_on_id": dep.DependsOnID,
				"type":          string(dep.Type),
			})
		}
		_ = outputJSON(map[string]interface{}{
			"status":       "added",
			"count":        len(deps),
			"dependencies": out,
		})
		return
	}

	fmt.Printf("%s Added %d dependencies\n", ui.RenderPass("✓"), len(deps))
}

func runDepRemoveProxiedServer(_ *cobra.Command, ctx context.Context, args []string) {
	fromID := args[0]
	toID := args[1]
	if strings.HasPrefix(toID, "external:") {
		if err := validateExternalRef(toID); err != nil {
			FatalErrorRespectJSON("%v", err)
		}
	}

	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	// beads-byh6: mirror the direct path's w2tk guard. RemoveDependency is
	// idempotent-nil (the domain removeDep discards depRepo.Delete's count and
	// returns nil whether or not an edge existed), so without a precheck a
	// nonexistent edge printed "✓ Removed" / status:removed rc=0 on a hub-
	// connected crew — a false success a CI/agent gate reads as proof the edge
	// is gone (the direct path guards this at cmd/bd/dep.go). Keep the
	// idempotent contract for programmatic callers; only the CLI verb reports
	// the distinction.
	depRecords, lookupErr := uw.DependencyUseCase().GetIssueDependencyRecords(ctx, []string{fromID})
	if lookupErr != nil {
		FatalErrorRespectJSON("checking dependency %s -> %s: %v", fromID, toID, lookupErr)
	}
	edgeExists := false
	var edgeType types.DependencyType
	for _, rec := range depRecords[fromID] {
		if rec != nil && rec.DependsOnID == toID {
			edgeExists = true
			edgeType = rec.Type
			break
		}
	}
	if !edgeExists {
		FatalErrorRespectJSON("no dependency to remove: %s does not depend on %s", fromID, toID)
	}

	// beads-xlplm: refuse a relates-to link (bidirectional — use bd unrelate),
	// mirroring the direct path. A single-edge remove would orphan the
	// reciprocal (remove-side sibling of ri535).
	if edgeType == types.DepRelatesTo {
		FatalErrorRespectJSON("cannot remove a relates-to link with 'dep remove' (it is bidirectional); use 'bd dep unrelate %s %s'", fromID, toID)
	}

	if err := uw.DependencyUseCase().RemoveDependency(ctx, fromID, toID, actor); err != nil {
		FatalErrorRespectJSON("%v", err)
	}

	fromTitle := proxiedLookupTitle(ctx, uw, fromID)
	toTitle := proxiedLookupTitle(ctx, uw, toID)

	if err := uw.Commit(ctx, fmt.Sprintf("bd: dep remove %s %s", fromID, toID)); err != nil && !isDoltNothingToCommit(err) {
		FatalErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		_ = outputJSON(map[string]interface{}{
			"status":        "removed",
			"issue_id":      fromID,
			"depends_on_id": toID,
		})
		return
	}

	fmt.Printf("%s Removed dependency: %s no longer depends on %s\n",
		ui.RenderPass("✓"),
		formatFeedbackIDParen(fromID, fromTitle),
		formatFeedbackIDParen(toID, toTitle))
}

func runDepListProxiedServer(cmd *cobra.Command, ctx context.Context, args []string) {
	direction, _ := cmd.Flags().GetString("direction")
	typeFilter, _ := cmd.Flags().GetString("type")
	if direction == "" {
		direction = "down"
	}
	// Reject an invalid --direction (mirrors the direct path, beads-etz9): dep
	// list only branches on == "down" / == "up", so a typo'd value silently
	// returned wrong-direction results with rc=0. Valid set is {down, up}.
	if direction != "down" && direction != "up" {
		FatalErrorRespectJSON("--direction must be 'down' or 'up'")
	}

	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	depUC := uw.DependencyUseCase()

	// beads-ez8b: resolve every id up front and signal a non-zero exit on any
	// unresolvable id — mirroring the direct path (cmd/bd/dep.go, which resolves
	// each arg and returns depListExit -> exitError{Code:1} on any failed id)
	// and the qtw9 proxied-show fix. Without this a ghost id just yields an
	// empty "has no dependencies" with rc=0, so `bd dep list <ghost> || fail`
	// reads false-clean in proxied mode. Single-id not-found is a hard error
	// (no listing); a batch warns+skips the bogus id, lists the rest, then exits
	// non-zero — the same partial-failure contract as the direct path.
	batchMode := len(args) > 1
	validArgs := make([]string, 0, len(args))
	failedCount := 0
	for _, id := range args {
		issue, _, err := proxiedGetIssueOrWisp(ctx, uw, id)
		if err != nil {
			if batchMode {
				// beads-7kxly: JSON-aware per-item skip (mirrors the direct path
				// in cmd/bd/dep.go) — reportItemError emits a parseable JSON object
				// to stderr under --json, keeps the plaintext warning otherwise.
				reportItemError("warning: resolving %s: %v (skipped)", id, err)
				failedCount++
				continue
			}
			FatalErrorRespectJSON("resolving %s: %v", id, err)
		}
		if issue == nil {
			if batchMode {
				// beads-7kxly: JSON-aware per-item skip (see above).
				reportItemError("warning: no issue found: %s (skipped)", id)
				failedCount++
				continue
			}
			FatalErrorRespectJSON("no issue found: %s", id)
		}
		validArgs = append(validArgs, id)
	}
	args = validArgs

	if len(args) > 1 && direction == "down" {
		depMap, err := depUC.GetIssueDependencyRecords(ctx, args)
		if err != nil {
			FatalErrorRespectJSON("%v", err)
		}
		var allDeps []*types.Dependency
		for _, id := range args {
			for _, dep := range depMap[id] {
				if typeFilter == "" || string(dep.Type) == typeFilter {
					allDeps = append(allDeps, dep)
				}
			}
		}
		if jsonOutput {
			if allDeps == nil {
				allDeps = []*types.Dependency{}
			}
			_ = outputJSON(allDeps)
			depListProxiedExit(failedCount)
			return
		}
		for _, id := range args {
			deps := depMap[id]
			if len(deps) == 0 {
				fmt.Printf("\n%s has no dependencies\n", id)
				continue
			}
			fmt.Printf("\n%s %s depends on:\n\n", ui.RenderAccent("📋"), id)
			for _, dep := range deps {
				if typeFilter != "" && string(dep.Type) != typeFilter {
					continue
				}
				fmt.Printf("  %s via %s\n", dep.DependsOnID, dep.Type)
			}
		}
		fmt.Println()
		depListProxiedExit(failedCount)
		return
	}

	var allIssues []*types.IssueWithDependencyMetadata
	listDirection := domain.DepDirectionOut
	if direction == "up" {
		listDirection = domain.DepDirectionIn
	}
	for _, id := range args {
		issues, err := depUC.ListWithIssueMetadata(ctx, id, domain.DepListFilter{Direction: listDirection})
		if err != nil {
			FatalErrorRespectJSON("%v", err)
		}
		if typeFilter != "" {
			filtered := issues[:0]
			for _, iss := range issues {
				if string(iss.DependencyType) == typeFilter {
					filtered = append(filtered, iss)
				}
			}
			issues = filtered
		}
		allIssues = append(allIssues, issues...)
	}

	if jsonOutput {
		if allIssues == nil {
			allIssues = []*types.IssueWithDependencyMetadata{}
		}
		_ = outputJSON(allIssues)
		depListProxiedExit(failedCount)
		return
	}

	if len(allIssues) == 0 {
		if len(args) == 1 {
			if direction == "up" {
				fmt.Printf("\nNo issues depend on %s\n", args[0])
			} else {
				fmt.Printf("\n%s has no dependencies\n", args[0])
			}
		} else {
			fmt.Println("\nNo dependencies found")
		}
		depListProxiedExit(failedCount)
		return
	}

	printProxiedDepList(allIssues)
	depListProxiedExit(failedCount)
}

// printProxiedDepList renders the proxied dep-list view. Each issue title is
// routed through displayTitle (ui.SanitizeForTerminal) because a title can
// originate from an untrusted import (JSONL/markdown/SCM) carrying OSC/CSI
// terminal-control escapes; printing it raw would inject control sequences onto
// the line. Display-only — stored titles are unchanged. Proxied twin of the
// direct dep.go dep-list sink. 7n9y sink-class slice (beads-2ktwm).
func printProxiedDepList(allIssues []*types.IssueWithDependencyMetadata) {
	for _, iss := range allIssues {
		var idStr string
		switch iss.Status {
		case types.StatusOpen:
			idStr = ui.StatusOpenStyle.Render(iss.ID)
		case types.StatusInProgress:
			idStr = ui.StatusInProgressStyle.Render(iss.ID)
		case types.StatusBlocked:
			idStr = ui.StatusBlockedStyle.Render(iss.ID)
		case types.StatusClosed:
			idStr = ui.StatusClosedStyle.Render(iss.ID)
		default:
			idStr = iss.ID
		}
		fmt.Printf("  %s: %s [P%d] (%s) via %s\n",
			idStr, displayTitle(iss.Title), iss.Priority, iss.Status, iss.DependencyType)
	}
	fmt.Println()
}

// depListProxiedExit exits non-zero when any id failed to resolve (beads-ez8b),
// mirroring the direct path's depListExit. Kept as a helper so every return
// site in runDepListProxiedServer shares the one partial-failure contract.
func depListProxiedExit(failedCount int) {
	if failedCount > 0 {
		os.Exit(1)
	}
}

func runDepTreeProxiedServer(cmd *cobra.Command, ctx context.Context, args []string) {
	fullID := args[0]
	showAllPaths, _ := cmd.Flags().GetBool("show-all-paths")
	maxDepth, _ := cmd.Flags().GetInt("max-depth")
	reverse, _ := cmd.Flags().GetBool("reverse")
	direction, _ := cmd.Flags().GetString("direction")
	statusFilter, _ := cmd.Flags().GetString("status")
	formatStr, _ := cmd.Flags().GetString("format")
	if strings.EqualFold(formatStr, "json") {
		jsonOutput = true
		formatStr = ""
	}
	if direction == "" && reverse {
		direction = "up"
	} else if direction == "" {
		direction = "down"
	}
	if direction != "down" && direction != "up" && direction != "both" {
		FatalErrorRespectJSON("--direction must be 'down', 'up', or 'both'")
	}
	if maxDepth < 1 {
		FatalErrorRespectJSON("--max-depth must be >= 1")
	}

	// beads-n95d: validate --format (parity with the direct path + with
	// --direction/--max-depth above). Only json (consumed to "") and mermaid
	// are supported; anything else previously fell through to default text.
	if formatStr != "" && !strings.EqualFold(formatStr, "mermaid") {
		FatalErrorRespectJSON("invalid --format %q (valid: json, mermaid)", formatStr)
	}

	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	depUC := uw.DependencyUseCase()
	var tree []*types.TreeNode

	if direction == "both" {
		downTree, err := depUC.GetDependencyTree(ctx, fullID, domain.DepTreeOpts{
			MaxDepth:     maxDepth,
			ShowAllPaths: showAllPaths,
			Direction:    domain.DepDirectionOut,
		})
		if err != nil {
			FatalErrorRespectJSON("%v", err)
		}
		upTree, err := depUC.GetDependencyTree(ctx, fullID, domain.DepTreeOpts{
			MaxDepth:     maxDepth,
			ShowAllPaths: showAllPaths,
			Direction:    domain.DepDirectionIn,
		})
		if err != nil {
			FatalErrorRespectJSON("%v", err)
		}
		tree = mergeBidirectionalTrees(downTree, upTree, fullID)
	} else {
		treeDir := domain.DepDirectionOut
		if direction == "up" {
			treeDir = domain.DepDirectionIn
		}
		var err error
		tree, err = depUC.GetDependencyTree(ctx, fullID, domain.DepTreeOpts{
			MaxDepth:     maxDepth,
			ShowAllPaths: showAllPaths,
			Direction:    treeDir,
		})
		if err != nil {
			FatalErrorRespectJSON("%v", err)
		}
	}

	if statusFilter != "" {
		tree = filterTreeByStatus(tree, types.Status(statusFilter))
	}

	if strings.EqualFold(formatStr, "mermaid") {
		outputMermaidTree(tree, args[0])
		return
	}

	if jsonOutput {
		if tree == nil {
			tree = []*types.TreeNode{}
		}
		_ = outputJSON(tree)
		return
	}

	if len(tree) == 0 {
		switch direction {
		case "up":
			fmt.Printf("\n%s has no dependents\n", fullID)
		case "both":
			fmt.Printf("\n%s has no dependencies or dependents\n", fullID)
		default:
			fmt.Printf("\n%s has no dependencies\n", fullID)
		}
		return
	}

	switch direction {
	case "up":
		fmt.Printf("\n%s Dependent tree for %s:\n\n", ui.RenderAccent("🌲"), fullID)
	case "both":
		fmt.Printf("\n%s Full dependency graph for %s:\n\n", ui.RenderAccent("🌲"), fullID)
	default:
		fmt.Printf("\n%s Dependency tree for %s:\n\n", ui.RenderAccent("🌲"), fullID)
	}

	// beads-8xwpb: mirror the direct path (dep.go:1336-1342). Compute the
	// root's READY/BLOCKED verdict from ground truth (DependencyUseCase.IsBlocked,
	// the same source `bd blocked` uses) for EVERY direction, rather than deriving
	// it from the (possibly --max-depth-truncated or --reverse) children slice.
	// The tree-derived verdict was wrong in two directions on the direct path
	// (x2e9: truncated blocker → false [READY]; wucv: dependents ≠ blockers →
	// false [BLOCKED]) and was still un-mirrored on this proxied twin, which
	// called renderTree(...) forwarding a nil rootBlockedOverride.
	var rootBlockedOverride *bool
	if blocked, _, berr := depUC.IsBlocked(ctx, fullID); berr == nil {
		rootBlockedOverride = &blocked
	}
	renderTreeWithVerdict(tree, maxDepth, direction, rootBlockedOverride, showAllPaths)
	fmt.Println()
}

func runDepCyclesProxiedServer(_ *cobra.Command, ctx context.Context) {
	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	cycles, err := uw.DependencyUseCase().DetectCycles(ctx)
	if err != nil {
		FatalErrorRespectJSON("%v", err)
	}

	if jsonOutput {
		if cycles == nil {
			cycles = [][]*types.Issue{}
		}
		_ = outputJSON(cycles)
		return
	}

	if len(cycles) == 0 {
		fmt.Printf("\n%s No dependency cycles detected\n\n", ui.RenderPass("✓"))
		return
	}

	fmt.Printf("\n%s Found %d dependency cycles:\n\n", ui.RenderFail("⚠"), len(cycles))
	printProxiedDepCycles(cycles)
}

// printProxiedDepCycles renders the proxied dependency-cycle list. Each issue
// title is routed through displayTitle (ui.SanitizeForTerminal) for the same
// untrusted-import escape reason as printProxiedDepList. Display-only — stored
// titles are unchanged. Proxied twin of the direct dep.go cycle sink. 7n9y
// sink-class slice (beads-2ktwm).
func printProxiedDepCycles(cycles [][]*types.Issue) {
	for i, cycle := range cycles {
		fmt.Printf("%d. Cycle involving:\n", i+1)
		for _, issue := range cycle {
			fmt.Printf("   - %s: %s\n", issue.ID, displayTitle(issue.Title))
		}
		fmt.Println()
	}
}
