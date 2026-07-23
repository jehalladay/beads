package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var (
	molShowParallel bool // --parallel flag for parallel detection
)

var molShowCmd = &cobra.Command{
	Use:   "show <molecule-id>",
	Short: "Show molecule details",
	Long: `Show molecule structure and details.

The --parallel flag highlights parallelizable steps:
  - Steps with no blocking dependencies can run in parallel
  - Shows which steps are ready to start now
  - Identifies parallel groups (steps that can run concurrently)

Example:
  bd mol show bd-patrol --parallel`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("mol-show")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		ctx := rootCtx

		// beads-ztu1e (ojyjj/mgjco/aocj fail-loud class, read-side): in
		// proxied-server mode main.go's PersistentPreRunE returns before
		// newDoltStore (main.go:1147-1155) leaving the global store nil, so the
		// bare store==nil check below misdiagnoses the proxied config as a local
		// "no database connection" (reads as an infra outage). Guard BEFORE the
		// nil check with an accurate message, mirroring mol burn (ojyjj).
		if usesProxiedServer() {
			return HandleErrorRespectJSON("mol show is not supported in proxied-server mode (connect directly with an embedded/dolt store)")
		}
		if store == nil {
			return HandleErrorRespectJSON("no database connection")
		}

		moleculeID, err := utils.ResolvePartialID(ctx, store, args[0])
		if err != nil {
			return HandleErrorRespectJSON("molecule '%s' not found", args[0])
		}

		subgraph, err := loadTemplateSubgraph(ctx, store, moleculeID)
		if err != nil {
			return HandleErrorRespectJSON("loading molecule: %v", err)
		}

		if molShowParallel {
			return showMoleculeWithParallel(subgraph, doneCategoryStatusSet(ctx, store))
		}
		return showMolecule(subgraph)
	},
}

func showMolecule(subgraph *MoleculeSubgraph) error {
	if jsonOutput {
		// beads-1sq7f: normalize the three nil-able slices to [] so a plain
		// molecule emits []-not-null under --json. dependencies is nil for a
		// root-only/no-internal-dep molecule, variables (extractAllVariables ->
		// template.go's `var vars []string`) is nil with no {{handlebars}}, and
		// bonded_from is nil for a non-compound root. All three feed no-omitempty
		// json fields via this raw map, so a nil slice marshals to null while the
		// sibling --parallel path (analyzeMoleculeParallel) inits every slice to
		// [] — the guib/5fv3/036h/4mkg json-ARRAY nil-slice contract. Distinct
		// emit site from 036h (internal/formula + mol_distill) and 4mkg
		// (ParallelInfo struct).
		deps := subgraph.Dependencies
		if deps == nil {
			deps = []*types.Dependency{}
		}
		vars := extractAllVariables(subgraph)
		if vars == nil {
			vars = []string{}
		}
		bondedFrom := subgraph.Root.BondedFrom
		if bondedFrom == nil {
			bondedFrom = []types.BondRef{}
		}
		return outputJSON(map[string]interface{}{
			"root":         subgraph.Root,
			"issues":       subgraph.Issues,
			"dependencies": deps,
			"variables":    vars,
			"is_compound":  subgraph.Root.IsCompound(),
			"bonded_from":  bondedFrom,
		})
	}

	// Determine molecule type label
	moleculeType := "Molecule"
	if subgraph.Root.IsCompound() {
		moleculeType = "Compound"
	}

	fmt.Printf("\n%s %s: %s\n", ui.RenderAccent("🧪"), moleculeType, displayTitle(subgraph.Root.Title))
	fmt.Printf("   ID: %s\n", subgraph.Root.ID)
	fmt.Printf("   Steps: %d\n", len(subgraph.Issues))

	// Show compound bonding info if this is a compound molecule
	if subgraph.Root.IsCompound() {
		showCompoundBondingInfo(subgraph.Root)
	}

	vars := extractAllVariables(subgraph)
	if len(vars) > 0 {
		fmt.Printf("\n%s Variables:\n", ui.RenderWarn("📝"))
		for _, v := range vars {
			fmt.Printf("   {{%s}}\n", v)
		}
	}

	fmt.Printf("\n%s Structure:\n", ui.RenderPass("🌲"))
	printMoleculeTree(subgraph, subgraph.Root.ID, 0, true)
	fmt.Println()
	return nil
}

// showCompoundBondingInfo displays the bonding lineage for compound molecules.
// Caller must ensure root.IsCompound() is true.
func showCompoundBondingInfo(root *types.Issue) {
	constituents := root.GetConstituents()
	fmt.Printf("\n%s Bonded from:\n", ui.RenderAccent("🔗"))

	for i, ref := range constituents {
		connector := "├──"
		if i == len(constituents)-1 {
			connector = "└──"
		}

		// Format bond type for display
		bondTypeDisplay := formatBondType(ref.BondType)

		// Show source ID with bond type
		if ref.BondPoint != "" {
			fmt.Printf("   %s %s (%s, at %s)\n", connector, ref.SourceID, bondTypeDisplay, ref.BondPoint)
		} else {
			fmt.Printf("   %s %s (%s)\n", connector, ref.SourceID, bondTypeDisplay)
		}
	}
}

// formatBondType returns a human-readable bond type description
func formatBondType(bondType string) string {
	switch bondType {
	case types.BondTypeSequential:
		return "sequential"
	case types.BondTypeParallel:
		return "parallel"
	case types.BondTypeConditional:
		return "on-failure"
	case types.BondTypeRoot:
		return "root"
	default:
		if bondType == "" {
			return "default"
		}
		return bondType
	}
}

// ParallelInfo holds parallel analysis information for a step
type ParallelInfo struct {
	StepID        string   `json:"step_id"`
	Status        string   `json:"status"`
	IsReady       bool     `json:"is_ready"`       // Can start now (no blocking deps)
	ParallelGroup string   `json:"parallel_group"` // Group ID (steps with same group can parallelize)
	BlockedBy     []string `json:"blocked_by"`     // IDs of open steps blocking this one
	Blocks        []string `json:"blocks"`         // IDs of steps this one blocks
	CanParallel   []string `json:"can_parallel"`   // IDs of steps that can run in parallel with this
}

// ParallelAnalysis holds the complete parallel analysis for a molecule
type ParallelAnalysis struct {
	MoleculeID     string                   `json:"molecule_id"`
	TotalSteps     int                      `json:"total_steps"`
	ReadySteps     int                      `json:"ready_steps"`
	ParallelGroups map[string][]string      `json:"parallel_groups"` // group ID -> step IDs
	Steps          map[string]*ParallelInfo `json:"steps"`
}

// stepIsComplete reports whether a molecule step counts as terminally complete
// for readiness/blocker analysis: a literal close OR a configured custom
// done-category status (beads-ruc6a). done carries the DONE-category custom
// status names (DONE-only, matching beads-x463g's resolveDoneStatusNamesInTx =
// CustomStatusesByCategory(CategoryDone); a FROZEN-category status is parked, not
// done, and deliberately does NOT count). A nil/empty done map is
// degraded-safe — only StatusClosed completes, byte-identical to the pre-ruc6a
// literal-closed behavior.
func stepIsComplete(status types.Status, done map[string]bool) bool {
	return status == types.StatusClosed || done[string(status)]
}

// doneCategoryStatusSet resolves the configured custom DONE-category status names
// into a name->true set for analyzeMoleculeParallel (beads-ruc6a), reading via the
// direct-store config accessor (the same GetCustomStatusesDetailed used by
// list_filter.go's directConfigSource / count.go). DONE-only, matching x463g's
// resolveDoneStatusNamesInTx: FROZEN is parked, not done. Degraded-safe — a nil
// store or a config-read error yields an empty set (only literal-closed
// completes), byte-identical to pre-ruc6a behavior.
func doneCategoryStatusSet(ctx context.Context, s storage.ConfigMetadataStore) map[string]bool {
	done := map[string]bool{}
	if s == nil {
		return done
	}
	detailed, err := s.GetCustomStatusesDetailed(ctx)
	if err != nil {
		return done
	}
	for _, cs := range detailed {
		if cs.Category == types.CategoryDone {
			done[cs.Name] = true
		}
	}
	return done
}

// analyzeMoleculeParallel performs parallel detection on a molecule subgraph.
// Returns analysis of which steps can run in parallel.
//
// done carries the configured custom done-category status names (beads-ruc6a) so
// a step blocked only by a sibling in a done-category status is reported ready,
// matching bd ready / is_blocked / getMoleculeProgress (beads-x463g). Callers
// resolve it via their existing config source (GetCustomStatusesDetailed direct /
// ConfigUseCase().GetCustomStatuses proxied); a nil map keeps pre-ruc6a behavior
// (only literal-closed completes).
func analyzeMoleculeParallel(subgraph *MoleculeSubgraph, done map[string]bool) *ParallelAnalysis {
	analysis := &ParallelAnalysis{
		MoleculeID:     subgraph.Root.ID,
		TotalSteps:     len(subgraph.Issues),
		ParallelGroups: make(map[string][]string),
		Steps:          make(map[string]*ParallelInfo),
	}

	// Build dependency maps
	// blockedBy[id] = set of issue IDs that block this issue
	// blocks[id] = set of issue IDs that this issue blocks
	blockedBy := make(map[string]map[string]bool)
	blocks := make(map[string]map[string]bool)
	parentChildren := make(map[string][]string)

	for _, issue := range subgraph.Issues {
		blockedBy[issue.ID] = make(map[string]bool)
		blocks[issue.ID] = make(map[string]bool)
	}

	// Build child index for waits-for gate evaluation.
	for _, dep := range subgraph.Dependencies {
		if dep.Type == types.DepParentChild {
			parentChildren[dep.DependsOnID] = append(parentChildren[dep.DependsOnID], dep.IssueID)
		}
	}

	// Process dependencies to find blocking relationships
	for _, dep := range subgraph.Dependencies {
		switch dep.Type {
		case types.DepBlocks, types.DepConditionalBlocks:
			// dep.IssueID depends on (is blocked by) dep.DependsOnID
			if _, ok := blockedBy[dep.IssueID]; ok {
				blockedBy[dep.IssueID][dep.DependsOnID] = true
			}
			if _, ok := blocks[dep.DependsOnID]; ok {
				blocks[dep.DependsOnID][dep.IssueID] = true
			}
		case types.DepWaitsFor:
			children := parentChildren[dep.DependsOnID]
			if len(children) == 0 {
				continue
			}

			gate := types.ParseWaitsForGateMetadata(dep.Metadata)
			if gate == types.WaitsForAnyChildren {
				hasClosedChild := false
				for _, childID := range children {
					child := subgraph.IssueMap[childID]
					// beads-ruc6a: a done-category child satisfies the any-children
					// gate exactly like a literal-closed child.
					if child != nil && stepIsComplete(child.Status, done) {
						hasClosedChild = true
						break
					}
				}
				if hasClosedChild {
					continue
				}
			}

			// For all-children (and unresolved any-children), each open child blocks the gate.
			for _, childID := range children {
				child := subgraph.IssueMap[childID]
				// beads-ruc6a: a done-category child counts complete like a closed one.
				if child == nil || stepIsComplete(child.Status, done) {
					continue
				}

				if _, ok := blockedBy[dep.IssueID]; ok {
					blockedBy[dep.IssueID][childID] = true
				}
				if _, ok := blocks[childID]; ok {
					blocks[childID][dep.IssueID] = true
				}
			}
		}
	}

	// Identify which steps are ready (no open blockers)
	readySteps := make(map[string]bool)
	for _, issue := range subgraph.Issues {
		info := &ParallelInfo{
			StepID:    issue.ID,
			Status:    string(issue.Status),
			BlockedBy: []string{},
			Blocks:    []string{},
			// beads-4mkg: init CanParallel too so a step OUTSIDE any parallel
			// group emits can_parallel:[] not null. It's only appended at the
			// parallel-group pass below (~L364), so without this a lone/serial
			// step's []string json field (no omitempty) marshals to null while
			// its sibling blocked_by/blocks correctly emit [] — the guib/036h/
			// 5fv3/jxel nil-slice asymmetry. Emitted via outputJSON("parallel")
			// and shared by analyzeMoleculeParallel's consumers (mol show/
			// current, ready, ready/close --proxied), so this one root covers all.
			CanParallel: []string{},
		}

		// Check what blocks this step
		for blockerID := range blockedBy[issue.ID] {
			blocker := subgraph.IssueMap[blockerID]
			// beads-ruc6a: a blocker in a done-category status is complete and no
			// longer active, matching bd ready / is_blocked (x463g).
			if blocker != nil && !stepIsComplete(blocker.Status, done) {
				info.BlockedBy = append(info.BlockedBy, blockerID)
			}
		}

		// Check what this step blocks
		for blockedID := range blocks[issue.ID] {
			info.Blocks = append(info.Blocks, blockedID)
		}

		// A step is ready if it's open/in_progress and has no open blockers
		info.IsReady = (issue.Status == types.StatusOpen || issue.Status == types.StatusInProgress) &&
			len(info.BlockedBy) == 0

		if info.IsReady {
			readySteps[issue.ID] = true
			analysis.ReadySteps++
		}

		// Sort for consistent output
		sort.Strings(info.BlockedBy)
		sort.Strings(info.Blocks)

		analysis.Steps[issue.ID] = info
	}

	// Identify parallel groups: steps that can run concurrently
	// Two steps can parallelize if:
	// 1. Both are ready (or will be ready at same time)
	// 2. Neither blocks the other (directly or transitively)
	// 3. They share the same blocking depth (distance from root)

	// Calculate blocking depth for each step
	depths := calculateBlockingDepths(subgraph, blockedBy, done)

	// Group steps by depth - steps at same depth can potentially parallelize
	depthGroups := make(map[int][]string)
	for id, depth := range depths {
		depthGroups[depth] = append(depthGroups[depth], id)
	}

	// For each depth level, identify parallel groups
	groupCounter := 0
	for depth := 0; depth <= len(subgraph.Issues); depth++ {
		stepsAtDepth := depthGroups[depth]
		if len(stepsAtDepth) == 0 {
			continue
		}

		// Group steps that can parallelize (no blocking between them)
		// Use union-find approach: start with each step in its own group
		parent := make(map[string]string)
		for _, id := range stepsAtDepth {
			parent[id] = id
		}

		find := func(x string) string {
			for parent[x] != x {
				parent[x] = parent[parent[x]]
				x = parent[x]
			}
			return x
		}

		union := func(x, y string) {
			px, py := find(x), find(y)
			if px != py {
				parent[px] = py
			}
		}

		// Merge steps that CAN parallelize (no mutual blocking)
		for i, id1 := range stepsAtDepth {
			for j := i + 1; j < len(stepsAtDepth); j++ {
				id2 := stepsAtDepth[j]
				// Can parallelize if neither blocks the other
				if !blocks[id1][id2] && !blocks[id2][id1] &&
					!blockedBy[id1][id2] && !blockedBy[id2][id1] {
					union(id1, id2)
				}
			}
		}

		// Collect groups
		groups := make(map[string][]string)
		for _, id := range stepsAtDepth {
			root := find(id)
			groups[root] = append(groups[root], id)
		}

		// Assign group names and record can_parallel relationships
		for _, members := range groups {
			if len(members) > 1 {
				groupCounter++
				groupName := fmt.Sprintf("group-%d", groupCounter)
				analysis.ParallelGroups[groupName] = members

				// Update each step's parallel info
				for _, id := range members {
					info := analysis.Steps[id]
					info.ParallelGroup = groupName
					// Record all other members as can_parallel
					for _, otherId := range members {
						if otherId != id {
							info.CanParallel = append(info.CanParallel, otherId)
						}
					}
					sort.Strings(info.CanParallel)
				}
			}
		}
	}

	return analysis
}

// calculateBlockingDepths calculates the "blocking depth" of each step.
// Depth 0 = no blockers, Depth 1 = blocked by depth-0 steps, etc.
func calculateBlockingDepths(subgraph *MoleculeSubgraph, blockedBy map[string]map[string]bool, done map[string]bool) map[string]int {
	depths := make(map[string]int)
	visited := make(map[string]bool)

	var calculateDepth func(id string) int
	calculateDepth = func(id string) int {
		if d, ok := depths[id]; ok {
			return d
		}
		if visited[id] {
			// Cycle detected, return 0 to break
			return 0
		}
		visited[id] = true

		maxBlockerDepth := -1
		for blockerID := range blockedBy[id] {
			// Only count open blockers; beads-ruc6a: a done-category blocker is
			// complete and doesn't contribute to blocking depth.
			blocker := subgraph.IssueMap[blockerID]
			if blocker != nil && !stepIsComplete(blocker.Status, done) {
				blockerDepth := calculateDepth(blockerID)
				if blockerDepth > maxBlockerDepth {
					maxBlockerDepth = blockerDepth
				}
			}
		}

		depth := maxBlockerDepth + 1
		depths[id] = depth
		return depth
	}

	for _, issue := range subgraph.Issues {
		calculateDepth(issue.ID)
	}

	return depths
}

func showMoleculeWithParallel(subgraph *MoleculeSubgraph, done map[string]bool) error {
	analysis := analyzeMoleculeParallel(subgraph, done)

	if jsonOutput {
		// beads-wgvo1: normalize the same three nil-able slices to [] that the
		// default showMolecule path does (beads-1sq7f), so `bd mol show --parallel
		// --json` matches `bd mol show --json` on the JSON-array nil-slice contract.
		// dependencies is nil for a root-only/no-internal-dep molecule, variables
		// (extractAllVariables) is nil with no {{handlebars}}, and bonded_from is
		// nil for a non-compound root; all three feed no-omitempty json fields via
		// this raw map, so a nil slice would marshal to null. This sibling emit
		// site was missed by 1sq7f (guib/5fv3/036h/4mkg/1sq7f json-ARRAY contract).
		deps := subgraph.Dependencies
		if deps == nil {
			deps = []*types.Dependency{}
		}
		vars := extractAllVariables(subgraph)
		if vars == nil {
			vars = []string{}
		}
		bondedFrom := subgraph.Root.BondedFrom
		if bondedFrom == nil {
			bondedFrom = []types.BondRef{}
		}
		return outputJSON(map[string]interface{}{
			"root":         subgraph.Root,
			"issues":       subgraph.Issues,
			"dependencies": deps,
			"variables":    vars,
			"parallel":     analysis,
			"is_compound":  subgraph.Root.IsCompound(),
			"bonded_from":  bondedFrom,
		})
	}

	// Determine molecule type label
	moleculeType := "Molecule"
	if subgraph.Root.IsCompound() {
		moleculeType = "Compound"
	}

	fmt.Printf("\n%s %s: %s\n", ui.RenderAccent("🧪"), moleculeType, displayTitle(subgraph.Root.Title))
	fmt.Printf("   ID: %s\n", subgraph.Root.ID)
	fmt.Printf("   Steps: %d (%d ready)\n", analysis.TotalSteps, analysis.ReadySteps)

	// Show compound bonding info if this is a compound molecule
	if subgraph.Root.IsCompound() {
		showCompoundBondingInfo(subgraph.Root)
	}

	// Show parallel groups summary
	if len(analysis.ParallelGroups) > 0 {
		fmt.Printf("\n%s Parallel Groups:\n", ui.RenderPass("⚡"))
		for groupName, members := range analysis.ParallelGroups {
			fmt.Printf("   %s: %s\n", groupName, strings.Join(members, ", "))
		}
	}

	vars := extractAllVariables(subgraph)
	if len(vars) > 0 {
		fmt.Printf("\n%s Variables:\n", ui.RenderWarn("📝"))
		for _, v := range vars {
			fmt.Printf("   {{%s}}\n", v)
		}
	}

	fmt.Printf("\n%s Structure:\n", ui.RenderPass("🌲"))
	printMoleculeTreeWithParallel(subgraph, analysis, subgraph.Root.ID, 0, true)
	fmt.Println()
	return nil
}

// printMoleculeTreeWithParallel prints the molecule structure with parallel annotations.
// Uses a visited set to detect cycles (GH#2719) and avoid infinite recursion.
func printMoleculeTreeWithParallel(subgraph *MoleculeSubgraph, analysis *ParallelAnalysis, parentID string, depth int, isRoot bool) {
	visited := make(map[string]bool)
	printMoleculeTreeWithParallelVisited(subgraph, analysis, parentID, depth, isRoot, visited)
}

// printMoleculeTreeWithParallelVisited is the internal recursive implementation with cycle tracking.
func printMoleculeTreeWithParallelVisited(subgraph *MoleculeSubgraph, analysis *ParallelAnalysis, parentID string, depth int, isRoot bool, visited map[string]bool) {
	indent := strings.Repeat("  ", depth)

	// Print root with parallel info
	if isRoot {
		rootInfo := analysis.Steps[subgraph.Root.ID]
		annotation := getParallelAnnotation(rootInfo)
		fmt.Printf("%s   %s%s\n", indent, displayTitle(subgraph.Root.Title), annotation)
		visited[parentID] = true
	}

	// Find children of this parent
	var children []*types.Issue
	for _, dep := range subgraph.Dependencies {
		if dep.DependsOnID == parentID && dep.Type == types.DepParentChild {
			if child, ok := subgraph.IssueMap[dep.IssueID]; ok {
				children = append(children, child)
			}
		}
	}

	// Print children
	for i, child := range children {
		connector := "├──"
		if i == len(children)-1 {
			connector = "└──"
		}

		info := analysis.Steps[child.ID]
		annotation := getParallelAnnotation(info)

		// Cycle detection (GH#2719)
		if visited[child.ID] {
			fmt.Printf("%s   %s %s%s (cycle detected, skipping)\n", indent, connector, displayTitle(child.Title), annotation)
			continue
		}
		fmt.Printf("%s   %s %s%s\n", indent, connector, displayTitle(child.Title), annotation)
		visited[child.ID] = true
		printMoleculeTreeWithParallelVisited(subgraph, analysis, child.ID, depth+1, false, visited)
	}
}

// getParallelAnnotation returns the annotation string for a step's parallel status
func getParallelAnnotation(info *ParallelInfo) string {
	if info == nil {
		return ""
	}

	parts := []string{}

	// Status indicator
	switch info.Status {
	case string(types.StatusOpen):
		if info.IsReady {
			parts = append(parts, ui.RenderPass("ready"))
		} else {
			parts = append(parts, ui.RenderFail("blocked"))
		}
	case string(types.StatusInProgress):
		parts = append(parts, ui.RenderWarn("in_progress"))
	case string(types.StatusClosed):
		parts = append(parts, ui.RenderPass("completed"))
	}

	// Parallel group
	if info.ParallelGroup != "" {
		parts = append(parts, ui.RenderAccent(info.ParallelGroup))
	}

	// Blocking info
	if len(info.BlockedBy) > 0 {
		parts = append(parts, fmt.Sprintf("needs: %s", strings.Join(info.BlockedBy, ", ")))
	}

	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, " | ") + "]"
}

func init() {
	molShowCmd.Flags().BoolVarP(&molShowParallel, "parallel", "p", false, "Show parallel step analysis")
	molCmd.AddCommand(molShowCmd)
}
