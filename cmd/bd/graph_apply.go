package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// GraphApplyPlan describes a symbolic bead graph to create atomically.
type GraphApplyPlan struct {
	CommitMessage string           `json:"commit_message,omitempty"`
	Nodes         []GraphApplyNode `json:"nodes"`
	Edges         []GraphApplyEdge `json:"edges,omitempty"`
}

// GraphApplyNode describes a single bead to create.
type GraphApplyNode struct {
	Key               string            `json:"key"`
	Title             string            `json:"title"`
	Type              string            `json:"type,omitempty"`
	Description       string            `json:"description,omitempty"`
	Assignee          string            `json:"assignee,omitempty"`
	AssignAfterCreate bool              `json:"assign_after_create,omitempty"`
	Priority          *int              `json:"priority,omitempty"` // nil defaults to P2
	Labels            []string          `json:"labels,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	MetadataRefs      map[string]string `json:"metadata_refs,omitempty"`
	ParentKey         string            `json:"parent_key,omitempty"`
	ParentID          string            `json:"parent_id,omitempty"`
}

// GraphApplyEdge describes a dependency edge.
type GraphApplyEdge struct {
	FromKey string `json:"from_key,omitempty"`
	FromID  string `json:"from_id,omitempty"`
	ToKey   string `json:"to_key,omitempty"`
	ToID    string `json:"to_id,omitempty"`
	Type    string `json:"type,omitempty"`
}

// GraphApplyResult returns the concrete bead IDs assigned to each symbolic key.
type GraphApplyResult struct {
	IDs map[string]string `json:"ids"`
}

// GraphApplyOptions carries CLI-level storage options that apply to every node
// in the graph.
type GraphApplyOptions struct {
	Ephemeral bool
	NoHistory bool
	// NoInheritLabels, when true, suppresses inheriting parent labels onto graph
	// children (parity with `bd create --no-inherit-labels`). Zero value inherits,
	// matching single create's default (beads-l8qsn).
	NoInheritLabels bool
	// Force, when true, overrides the closed-parent guard (beads-t39ph) so a
	// graph child may be linked under a CLOSED auto-closing parent — parity with
	// `bd create --parent <closed> --force`.
	Force bool
}

func (opts GraphApplyOptions) Validate() error {
	if opts.Ephemeral && opts.NoHistory {
		return fmt.Errorf("ephemeral and no_history are mutually exclusive")
	}
	return nil
}

// GraphApplyDryRun describes the actions that would be taken by a graph plan,
// without performing any writes. Emitted by `bd create --graph --dry-run`.
type GraphApplyDryRun struct {
	DryRun          bool                  `json:"dry_run"`
	NodeCount       int                   `json:"node_count"`
	EdgeCount       int                   `json:"edge_count"`
	ParentDeps      int                   `json:"parent_deps"`
	ValidationNotes []string              `json:"validation_notes,omitempty"`
	Nodes           []GraphApplyDryRunRow `json:"nodes"`
}

// GraphApplyDryRunRow describes a single planned node in the dry-run preview.
type GraphApplyDryRunRow struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	Priority  int    `json:"priority"`
	ParentKey string `json:"parent_key,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
}

const graphApplyDryRunTransactionValidationNote = "dry-run validates the graph structure only; live create may still reject parent-child blocking paths after resolving stored dependencies"

// knownGraphPlanFields lists the JSON keys recognized at the top level of a
// GraphApplyPlan. Any other top-level keys produce a warning so users can spot
// schema typos (e.g. when a plan uses a sibling tool's format) instead of
// having fields silently dropped by encoding/json. (GH#3367)
var knownGraphPlanFields = map[string]struct{}{
	"commit_message": {},
	"nodes":          {},
	"edges":          {},
}

// knownGraphNodeFields lists the JSON keys recognized on a GraphApplyNode.
// Kept in sync with the json tags on GraphApplyNode. (GH#3367)
var knownGraphNodeFields = map[string]struct{}{
	"key":                 {},
	"title":               {},
	"type":                {},
	"description":         {},
	"assignee":            {},
	"assign_after_create": {},
	"priority":            {},
	"labels":              {},
	"metadata":            {},
	"metadata_refs":       {},
	"parent_key":          {},
	"parent_id":           {},
}

// knownGraphEdgeFields lists the JSON keys recognized on a GraphApplyEdge.
// Kept in sync with the json tags on GraphApplyEdge. (GH#3367)
var knownGraphEdgeFields = map[string]struct{}{
	"from_key": {},
	"from_id":  {},
	"to_key":   {},
	"to_id":    {},
	"type":     {},
}

// graphFieldHints maps unknown-field names to a corrective hint pointing at
// the recognized schema field. Used by warnUnknownGraphFields to suggest the
// intended schema when a plan uses a common-but-wrong name (e.g. nodes carry
// a "parent" string instead of "parent_key", or "blocks" arrays instead of
// the top-level edges array). (GH#3367)
var graphFieldHints = map[string]string{
	"parent":   "use 'parent_key' (referencing another node's 'key') or 'parent_id' (an existing issue ID)",
	"blocks":   "use the top-level 'edges' array, e.g. {\"from_key\": \"a\", \"to_key\": \"b\", \"type\": \"blocks\"}",
	"depends":  "use the top-level 'edges' array with type 'blocks'",
	"children": "set 'parent_key' on each child instead of listing children on the parent",
}

// detectUnknownGraphFields scans the raw plan JSON and returns unknown field
// names grouped by their location in the plan. The returned map keys describe
// the location ("plan", "node[<key-or-index>]", "edge[<index>]") and values
// are sorted lists of unknown field names at that location. Returns an empty
// map when the plan is structurally invalid (callers should still attempt the
// strict parse so the operator gets a normal parse error rather than only the
// schema warning). (GH#3367)
func detectUnknownGraphFields(rawData []byte) map[string][]string {
	out := make(map[string][]string)

	var top map[string]json.RawMessage
	if err := json.Unmarshal(rawData, &top); err != nil {
		return out
	}

	if planUnknown := unknownKeys(top, knownGraphPlanFields); len(planUnknown) > 0 {
		out["plan"] = planUnknown
	}

	if nodesRaw, ok := top["nodes"]; ok {
		var rawNodes []json.RawMessage
		if err := json.Unmarshal(nodesRaw, &rawNodes); err == nil {
			for i, nodeRaw := range rawNodes {
				var nodeMap map[string]json.RawMessage
				if err := json.Unmarshal(nodeRaw, &nodeMap); err != nil {
					continue
				}
				if unknown := unknownKeys(nodeMap, knownGraphNodeFields); len(unknown) > 0 {
					label := fmt.Sprintf("node[%d]", i)
					if keyRaw, ok := nodeMap["key"]; ok {
						var keyStr string
						if err := json.Unmarshal(keyRaw, &keyStr); err == nil && keyStr != "" {
							label = fmt.Sprintf("node[%q]", keyStr)
						}
					}
					out[label] = unknown
				}
			}
		}
	}

	if edgesRaw, ok := top["edges"]; ok {
		var rawEdges []json.RawMessage
		if err := json.Unmarshal(edgesRaw, &rawEdges); err == nil {
			for i, edgeRaw := range rawEdges {
				var edgeMap map[string]json.RawMessage
				if err := json.Unmarshal(edgeRaw, &edgeMap); err != nil {
					continue
				}
				if unknown := unknownKeys(edgeMap, knownGraphEdgeFields); len(unknown) > 0 {
					out[fmt.Sprintf("edge[%d]", i)] = unknown
				}
			}
		}
	}

	return out
}

// unknownKeys returns the keys present in have that are not in known, sorted
// alphabetically for deterministic output.
func unknownKeys(have map[string]json.RawMessage, known map[string]struct{}) []string {
	var unknown []string
	for k := range have {
		if _, ok := known[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// warnUnknownGraphFields prints a single warning line per location in the
// plan with one or more unknown fields, plus a per-field hint when one is
// available. Output goes to w (typically os.Stderr). (GH#3367)
func warnUnknownGraphFields(w io.Writer, unknown map[string][]string) {
	if len(unknown) == 0 {
		return
	}

	locations := make([]string, 0, len(unknown))
	for loc := range unknown {
		locations = append(locations, loc)
	}
	sort.Strings(locations)

	distinct := make(map[string]struct{})
	for _, loc := range locations {
		fields := append([]string(nil), unknown[loc]...)
		sort.Strings(fields)
		fmt.Fprintf(w, "warning: graph plan %s has unknown field(s): %v (silently dropped — see 'bd create --graph' schema)\n", loc, fields)
		for _, f := range fields {
			distinct[f] = struct{}{}
		}
	}

	hintFields := make([]string, 0, len(distinct))
	for f := range distinct {
		hintFields = append(hintFields, f)
	}
	sort.Strings(hintFields)
	for _, f := range hintFields {
		if hint, ok := graphFieldHints[f]; ok {
			fmt.Fprintf(w, "  hint: %q is not part of the schema; %s\n", f, hint)
		}
	}
}

func loadEmbeddedCustomTypes() []string {
	if store != nil {
		if ct, err := store.GetCustomTypes(rootCtx); err == nil && len(ct) > 0 {
			return ct
		}
	}
	return config.GetCustomTypesFromYAML()
}

func createIssuesFromGraph(planFile string, dryRun bool, opts GraphApplyOptions) error {
	data, err := os.ReadFile(planFile) // #nosec G304 -- user-provided path is intentional
	if err != nil {
		return HandleErrorRespectJSON("reading graph plan: %v", err)
	}

	if unknown := detectUnknownGraphFields(data); len(unknown) > 0 {
		warnUnknownGraphFields(os.Stderr, unknown)
	}

	var plan GraphApplyPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return HandleErrorRespectJSON("parsing graph plan: %v", err)
	}

	if err := validateGraphApplyPlan(&plan, loadEmbeddedCustomTypes()); err != nil {
		return HandleErrorRespectJSON("invalid graph plan: %v", err)
	}

	if dryRun {
		return emitGraphApplyDryRun(&plan)
	}

	result, err := executeGraphApply(rootCtx, &plan, opts)
	if err != nil {
		return HandleErrorRespectJSON("graph create: %v", err)
	}

	if jsonOutput {
		return outputJSON(result)
	}
	fmt.Printf("Created %d issues\n", len(result.IDs))
	keys := make([]string, 0, len(result.IDs))
	for key := range result.IDs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Printf("  %s -> %s\n", key, result.IDs[key])
	}
	return nil
}

func emitGraphApplyDryRun(plan *GraphApplyPlan) error {
	parentDeps := 0
	rows := make([]GraphApplyDryRunRow, 0, len(plan.Nodes))
	for _, node := range plan.Nodes {
		// Normalize so the dry-run preview shows the SAME canonical type the live
		// create will store (beads-h3k5); otherwise --dry-run previews "feat"
		// while the create stores "feature". Empty stays empty -> defaults task.
		issueType := string(types.IssueType(node.Type).Normalize())
		if issueType == "" {
			issueType = string(types.TypeTask)
		}
		priority := 2
		if node.Priority != nil {
			priority = *node.Priority
		}
		if node.ParentKey != "" || node.ParentID != "" {
			parentDeps++
		}
		rows = append(rows, GraphApplyDryRunRow{
			Key:       node.Key,
			Title:     node.Title,
			Type:      issueType,
			Priority:  priority,
			ParentKey: node.ParentKey,
			ParentID:  node.ParentID,
		})
	}

	preview := GraphApplyDryRun{
		DryRun:          true,
		NodeCount:       len(plan.Nodes),
		EdgeCount:       len(plan.Edges),
		ParentDeps:      parentDeps,
		ValidationNotes: []string{graphApplyDryRunTransactionValidationNote},
		Nodes:           rows,
	}

	if jsonOutput {
		return outputJSON(preview)
	}

	fmt.Printf("Dry run: would create %d issue(s) and %d edge(s) (%d parent-child link(s))\n",
		preview.NodeCount, preview.EdgeCount, preview.ParentDeps)
	fmt.Printf("Note: %s.\n", graphApplyDryRunTransactionValidationNote)
	for _, row := range rows {
		parent := ""
		switch {
		case row.ParentKey != "":
			parent = fmt.Sprintf(" parent_key=%s", row.ParentKey)
		case row.ParentID != "":
			parent = fmt.Sprintf(" parent_id=%s", row.ParentID)
		}
		fmt.Printf("  %s [%s] P%d %q%s\n", row.Key, row.Type, row.Priority, row.Title, parent)
	}
	return nil
}

func validateGraphApplyPlan(plan *GraphApplyPlan, customTypes []string) error {
	if len(plan.Nodes) == 0 {
		return fmt.Errorf("plan has no nodes")
	}

	seenKeys := make(map[string]bool, len(plan.Nodes))
	for i, node := range plan.Nodes {
		if node.Key == "" {
			return fmt.Errorf("node %d has empty key", i)
		}
		if seenKeys[node.Key] {
			return fmt.Errorf("duplicate node key %q", node.Key)
		}
		seenKeys[node.Key] = true
		// beads-pwtvp: trim then empty-check and write back, mirroring single
		// `bd create` (create.go: "title = strings.TrimSpace(title); if
		// title==\"\"") — so a whitespace-only title is rejected and a padded
		// title is stored trimmed (not unmatchable). Both graph mint seams
		// (direct graph_apply.go, proxied materializeGraphNodeIssue) read
		// plan.Nodes after this validate, so the single write-back covers both.
		// Sibling of the adjacent assignee normalize (beads-7i4m/llzt) and the
		// label/closed-parent input-parity legs (l8qsn/t39ph).
		trimmedTitle := strings.TrimSpace(node.Title)
		plan.Nodes[i].Title = trimmedTitle
		if trimmedTitle == "" {
			return fmt.Errorf("node %q has empty title", node.Key)
		}
		if node.Type != "" {
			// Normalize expands documented aliases (feat->feature, mol->molecule,
			// ...) and folds canonical-name case, matching `bd create -t <type>`.
			// Without this a documented alias in a graph node failed validation,
			// an asymmetry with plain bd create (beads-h3k5, sibling of dr70).
			it := types.IssueType(node.Type).Normalize()
			if !it.IsValidWithCustom(customTypes) {
				return fmt.Errorf("node %q: invalid type %q", node.Key, node.Type)
			}
		}
		// beads-s13vq: reject reserved gt identity labels (gt:agent/gt:role/gt:rig)
		// on graph-apply nodes, matching single `bd create --label`, `--file`
		// markdown mint (kvq0v), `bd label add`, `bd tag`, and set-state — the
		// 3c4g write-reserve family. GraphApplyNode.Labels flows straight into the
		// minted Issue.Labels (direct executeGraphApply and proxied
		// buildDomainGraphPlan), so without this a node carrying "labels":
		// ["gt:agent"] silently mints a bead HIDDEN from `bd ready` (the wqs
		// discriminator excludes the identity family). validateGraphApplyPlan is
		// the shared chokepoint for both backends, so one guard covers both.
		// Gated on GT_INTERNAL like the rest of the family so gt's own graph-based
		// registration keeps working.
		for _, label := range node.Labels {
			if msg := reservedIdentityLabelError(label); msg != "" {
				return fmt.Errorf("node %q: %s", node.Key, msg)
			}
			// beads-o70m1: reject the reserved 'provides:' capability family on
			// graph-apply nodes, matching single `bd create --labels` and `bd label
			// add`. A node carrying "labels": ["provides:cap"] otherwise mints an
			// OPEN bead with a cross-project capability label at RC=0, bypassing the
			// closed-requirement + single-provider invariants that `bd ship`
			// enforces. validateGraphApplyPlan is the shared chokepoint for both
			// backends, so one guard covers direct executeGraphApply and the proxied
			// path. Not gated on GT_INTERNAL (ship stamps provides: via storage, not
			// this seam).
			if msg := providesLabelError(label); msg != "" {
				return fmt.Errorf("node %q: %s", node.Key, msg)
			}
		}
		// beads-h79ym: reject an out-of-range priority (must be 0-4) for parity
		// with single `bd create`, which rejects it via validation.ValidatePriority.
		// node.Priority flows straight into the minted Issue.Priority
		// (executeGraphApply + proxied materializeGraphNodeIssue) and storage does
		// not range-check it, so without this a node with "priority":9 (or -1)
		// silently minted a bead with an invalid priority invisible to `bd list -p`
		// filters. Only an explicitly-supplied value is checked; a nil Priority
		// keeps the documented P2 default. Mirrors the node-type (beads-h3k5) and
		// edge-type (beads-0kno) parity guards already in this validator.
		if node.Priority != nil && (*node.Priority < 0 || *node.Priority > 4) {
			return fmt.Errorf("node %q: invalid priority %d (expected 0-4)", node.Key, *node.Priority)
		}
		// Validate MetadataRefs point to known keys.
		for metaKey, refKey := range node.MetadataRefs {
			if !seenKeys[refKey] {
				found := false
				for _, other := range plan.Nodes {
					if other.Key == refKey {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("node %q: metadata ref %q references unknown key %q", node.Key, metaKey, refKey)
				}
			}
		}
		if node.ParentKey != "" && !seenKeys[node.ParentKey] {
			found := false
			for _, other := range plan.Nodes {
				if other.Key == node.ParentKey {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("node %q: parent key %q not found in plan", node.Key, node.ParentKey)
			}
		}
	}

	for i, edge := range plan.Edges {
		if edge.FromKey != "" && !seenKeys[edge.FromKey] {
			return fmt.Errorf("edge %d: from key %q not found in plan", i, edge.FromKey)
		}
		if edge.ToKey != "" && !seenKeys[edge.ToKey] {
			return fmt.Errorf("edge %d: to key %q not found in plan", i, edge.ToKey)
		}
		if edge.FromKey == "" && edge.FromID == "" {
			return fmt.Errorf("edge %d: must specify from_key or from_id", i)
		}
		if edge.ToKey == "" && edge.ToID == "" {
			return fmt.Errorf("edge %d: must specify to_key or to_id", i)
		}
		if edge.Type != "" {
			dt := types.DependencyType(edge.Type)
			if !dt.IsValid() {
				return fmt.Errorf("edge %d: invalid dependency type %q", i, edge.Type)
			}
			// beads-0kno: reject unknown types for parity with `bd dep add`
			// (beads-qfka) and `bd create --deps`. Without this a typo'd blocking
			// type (e.g. "blockd") was accepted and stored as a non-gating custom
			// edge, silently defeating the intended block. Mirrors the node-type
			// membership check at validateGraphApplyPlan (IsValidWithCustom).
			if !dt.IsWellKnown() {
				return fmt.Errorf("edge %d: unknown dependency type %q; valid types: %s", i, edge.Type, createDepsAcceptedTypeList())
			}
		}
	}

	if err := validateGraphApplyLocalCycles(plan, seenKeys); err != nil {
		return err
	}

	return nil
}

func validateGraphApplyLocalCycles(plan *GraphApplyPlan, knownKeys map[string]bool) error {
	adj := make(map[string][]string)
	for _, node := range plan.Nodes {
		if node.ParentKey != "" && knownKeys[node.Key] && knownKeys[node.ParentKey] {
			// ParentKey is guaranteed local by validateGraphApplyPlan, so it is
			// safe to model the implicit parent-child dependency by key here.
			adj[node.Key] = append(adj[node.Key], node.ParentKey)
		}
	}
	for _, edge := range plan.Edges {
		depType := graphApplyDependencyType(edge.Type)
		if !graphApplyEdgeIsLocalCycleRelevant(edge, depType) {
			continue
		}
		if !knownKeys[edge.FromKey] || !knownKeys[edge.ToKey] {
			continue
		}
		adj[edge.FromKey] = append(adj[edge.FromKey], edge.ToKey)
	}

	visiting := make(map[string]bool, len(knownKeys))
	visited := make(map[string]bool, len(knownKeys))
	var visit func(string) (string, bool)
	visit = func(key string) (string, bool) {
		if visiting[key] {
			return key, true
		}
		if visited[key] {
			return "", false
		}
		visiting[key] = true
		for _, next := range adj[key] {
			if cycleKey, ok := visit(next); ok {
				return cycleKey, true
			}
		}
		visiting[key] = false
		visited[key] = true
		return "", false
	}

	for _, key := range graphApplySortedKeys(knownKeys) {
		if cycleKey, ok := visit(key); ok {
			return fmt.Errorf("graph contains a blocking dependency cycle involving node %q", cycleKey)
		}
	}
	return nil
}

func executeGraphApply(ctx context.Context, plan *GraphApplyPlan, opts GraphApplyOptions) (*GraphApplyResult, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	keyToID := make(map[string]string, len(plan.Nodes))

	commitMsg := plan.CommitMessage
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("bd: graph-apply %d nodes", len(plan.Nodes))
	}

	if err := store.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		issues := make([]*types.Issue, 0, len(plan.Nodes))
		pendingAssignees := make(map[int]string)

		for i, node := range plan.Nodes {
			// Normalize aliases/case to canonical, matching validation above and
			// bd create -t (beads-h3k5). Empty stays empty -> defaults to task.
			issueType := types.IssueType(node.Type).Normalize()
			if issueType == "" {
				issueType = types.TypeTask
			}

			var metadataJSON json.RawMessage
			if len(node.Metadata) > 0 {
				raw, err := json.Marshal(node.Metadata)
				if err != nil {
					return fmt.Errorf("node %q: marshaling metadata: %w", node.Key, err)
				}
				metadataJSON = raw
			}

			priority := 2 // Default P2
			if node.Priority != nil {
				priority = *node.Priority
			}

			issue := &types.Issue{
				Title:     node.Title,
				IssueType: issueType,
				Status:    types.StatusOpen,
				Priority:  priority,
				Labels:    node.Labels,
				Metadata:  metadataJSON,
				Ephemeral: opts.Ephemeral,
				NoHistory: opts.NoHistory,
			}
			if node.Description != "" {
				issue.Description = node.Description
			}
			// Trim/fold-"none" like assign/create/update (normalizeAssignee) so a
			// padded "  alice  " isn't stored unmatchable and "none" unassigns
			// (beads-7i4m, llzt graph sibling). Normalize BEFORE the empty check
			// so a padded "  none  "/"  " correctly resolves to no assignment.
			if assignee := normalizeAssignee(node.Assignee); assignee != "" {
				if node.AssignAfterCreate {
					pendingAssignees[i] = assignee
				} else {
					issue.Assignee = assignee
				}
			}

			issues = append(issues, issue)
		}

		if err := tx.CreateIssues(ctx, issues, actor); err != nil {
			return fmt.Errorf("batch create: %w", err)
		}

		for i, node := range plan.Nodes {
			keyToID[node.Key] = issues[i].ID
		}

		// Resolve MetadataRefs now that all IDs are known.
		for i, node := range plan.Nodes {
			if len(node.MetadataRefs) == 0 {
				continue
			}
			mergedMeta := make(map[string]string)
			if issues[i].Metadata != nil {
				if err := json.Unmarshal(issues[i].Metadata, &mergedMeta); err != nil {
					return fmt.Errorf("node %q: re-parsing metadata: %w", node.Key, err)
				}
			}
			for metaKey, refKey := range node.MetadataRefs {
				mergedMeta[metaKey] = keyToID[refKey]
			}
			metaJSON, err := json.Marshal(mergedMeta)
			if err != nil {
				return fmt.Errorf("node %q: marshaling updated metadata: %w", node.Key, err)
			}
			updates := map[string]interface{}{
				"metadata": json.RawMessage(metaJSON),
			}
			if err := tx.UpdateIssue(ctx, issues[i].ID, updates, actor); err != nil {
				return fmt.Errorf("node %q: updating metadata refs: %w", node.Key, err)
			}
		}

		parentDepPairs := graphApplyParentDepPairs(plan.Nodes, keyToID)
		if err := validateGraphApplyPlannedParentBlockingPaths(ctx, tx, plan, keyToID, parentDepPairs); err != nil {
			return err
		}
		if err := validateGraphApplyPlannedBlockingCycles(ctx, tx, plan, keyToID); err != nil {
			return err
		}

		// Add dependencies from edges.
		for i, edge := range plan.Edges {
			fromID := resolveEdgeRef(edge.FromKey, edge.FromID, keyToID)
			toID := resolveEdgeRef(edge.ToKey, edge.ToID, keyToID)
			depType := graphApplyDependencyType(edge.Type)
			if parentDepPairs[graphApplyDepPairKey(fromID, toID)] {
				if depType == types.DepParentChild {
					continue
				}
				return fmt.Errorf("edge %d %s->%s duplicates a parent-child relationship with dependency type %q", i, fromID, toID, depType)
			}
			if parentDepPairs[graphApplyDepPairKey(toID, fromID)] && graphApplyCycleRelevantDependencyType(depType) {
				return fmt.Errorf("edge %d %s->%s creates a blocking reverse of a parent-child relationship", i, fromID, toID)
			}
			// beads-t39ph (edge-loop seam, dogfooder cascade-coverage): an explicit
			// parent-child EDGE ({from:child, to:closed-parent, type:parent-child})
			// links a child under its parent via THIS loop, separate from the
			// node.ParentID post-pass below. The post-pass guard (added below) never
			// runs for an edge-declared parent-child that isn't also a node.ParentID
			// pair (those are continue'd above), so a closed auto-closing parent
			// (epic/molecule/wisp) leaked here. Mirror the same guard: refuse an OPEN
			// child (fromID) linked under a CLOSED auto-closing parent (toID) unless
			// --force. A closed child, or a non-auto-closing/open parent, is fine.
			if !opts.Force && depType == types.DepParentChild {
				parent, perr := tx.GetIssue(ctx, toID)
				if perr == nil && parent != nil &&
					parent.IsAutoClosingParentType() && parent.Status == types.StatusClosed {
					child, cerr := tx.GetIssue(ctx, fromID)
					if cerr == nil && child != nil && child.Status != types.StatusClosed {
						return fmt.Errorf("cannot create a child under closed parent %s (its status is closed; reopen the parent first or use --force to override)", toID)
					}
				}
			}
			dep := &types.Dependency{
				IssueID:     fromID,
				DependsOnID: toID,
				Type:        depType,
			}
			addOpts := storage.DependencyAddOptions{}
			if graphApplyCycleRelevantDependencyType(depType) {
				addOpts.SkipCycleCheck = true
			}
			if err := tx.AddDependencyWithOptions(ctx, dep, actor, addOpts); err != nil {
				return fmt.Errorf("adding edge %s->%s: %w", fromID, toID, err)
			}
		}

		// Add parent-child dependencies.
		for i, node := range plan.Nodes {
			parentID := node.ParentID
			if node.ParentKey != "" {
				parentID = keyToID[node.ParentKey]
			}
			if parentID != "" {
				// beads-t39ph: graph-create mints every node top-level then links
				// parent-child here, so the single-create closed-parent guard
				// (create.go, gated on parentID != "") never runs for graph
				// children — a child under a CLOSED auto-closing parent (epic OR
				// molecule OR wisp, beads-aw9x8) landed silently. Mirror the guard
				// at this link seam (the same top-level-then-link seam l8qsn added
				// label inheritance to). Graph children are minted open, so any
				// closed auto-closing parent is a violation. Honors --force.
				if !opts.Force {
					parent, perr := tx.GetIssue(ctx, parentID)
					if perr == nil && parent != nil && parent.IsAutoClosingParentType() && parent.Status == types.StatusClosed {
						return fmt.Errorf("cannot create a child under closed parent %s (its status is closed; reopen the parent first or use --force to override)", parentID)
					}
				}
				dep := &types.Dependency{
					IssueID:     issues[i].ID,
					DependsOnID: parentID,
					Type:        types.DepParentChild,
				}
				if err := tx.AddDependency(ctx, dep, actor); err != nil {
					return fmt.Errorf("node %q: adding parent-child dep: %w", node.Key, err)
				}
			}
		}

		// Inherit parent labels onto graph children, matching single create
		// --parent (beads-l8qsn). The nodes are created top-level in the batch
		// above (no parent passed to CreateIssues), so the single-create
		// inheritance path never runs for them; this post-pass mirrors it via the
		// tx after parent-child deps are linked, so a parent declared later in
		// plan order (parent_key) is already minted. Union semantics (add only
		// labels the child lacks). Kept consistent with the domain applyGraph
		// Pass 4.5 twin. Skipped on the plan-level opt-out (--no-inherit-labels
		// parity).
		if !opts.NoInheritLabels {
			for i, node := range plan.Nodes {
				parentID := node.ParentID
				if node.ParentKey != "" {
					parentID = keyToID[node.ParentKey]
				}
				if parentID == "" {
					continue
				}
				childID := issues[i].ID
				parentLabels, err := tx.GetLabels(ctx, parentID)
				if err != nil {
					return fmt.Errorf("node %q: read parent labels for inheritance from %s: %w", node.Key, parentID, err)
				}
				if len(parentLabels) == 0 {
					continue
				}
				existing, err := tx.GetLabels(ctx, childID)
				if err != nil {
					return fmt.Errorf("node %q: read child labels %s: %w", node.Key, childID, err)
				}
				have := make(map[string]bool, len(existing))
				for _, l := range existing {
					have[l] = true
				}
				for _, label := range parentLabels {
					if have[label] {
						continue
					}
					if err := tx.AddLabel(ctx, childID, label, actor); err != nil {
						return fmt.Errorf("node %q: add inherited label %s: %w", node.Key, label, err)
					}
					have[label] = true
				}
			}
		}

		// Inherit source_repo from a discovered-from edge, matching single create
		// (beads-comt0). Single `bd create --deps discovered-from:<parent>` copies
		// the parent's source_repo onto the child (create.go, mirroring the domain
		// u.create DiscoveredFromParent block). Graph nodes are minted top-level
		// with an empty source_repo (the Issue struct above sets none, and
		// GraphApplyNode carries no source_repo field), then edges are linked in the
		// loop above — so the single-create inheritance never runs for graph
		// children. This post-pass mirrors it: for each discovered-from edge
		// (from=child, to=source-parent, same direction as single create's
		// depends-on), copy a non-empty parent source_repo onto the child. Runs
		// AFTER the edge loop so every ID is resolved. Same create-input-parity
		// class as the l8qsn label pass and the t39ph closed-parent guard; kept
		// consistent with the domain applyGraph Pass 4.6 twin.
		for i, edge := range plan.Edges {
			if graphApplyDependencyType(edge.Type) != types.DepDiscoveredFrom {
				continue
			}
			childID := resolveEdgeRef(edge.FromKey, edge.FromID, keyToID)
			parentID := resolveEdgeRef(edge.ToKey, edge.ToID, keyToID)
			if childID == "" || parentID == "" {
				continue
			}
			parent, perr := tx.GetIssue(ctx, parentID)
			if perr != nil || parent == nil || parent.SourceRepo == "" {
				// A miss or a parent with no source_repo leaves the child's default
				// (empty) source_repo, matching single create's "continue with
				// default" fallback.
				continue
			}
			if err := tx.UpdateIssue(ctx, childID, map[string]interface{}{"source_repo": parent.SourceRepo}, actor); err != nil {
				return fmt.Errorf("edge %d: inherit source_repo onto %s from %s: %w", i, childID, parentID, err)
			}
		}

		// Apply deferred assignees.
		for i, assignee := range pendingAssignees {
			updates := map[string]interface{}{
				"assignee": assignee,
			}
			if err := tx.UpdateIssue(ctx, issues[i].ID, updates, actor); err != nil {
				return fmt.Errorf("node %q: setting assignee: %w", plan.Nodes[i].Key, err)
			}
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return &GraphApplyResult{IDs: keyToID}, nil
}

// validateGraphApplyPlannedBlockingCycles rejects planned blocking edges that
// would close a blocking-dependency cycle, evaluated whole-graph before any
// insert. Both the planned adjacency and the existing-dep walk are restricted
// to cycle-relevant BLOCKING types via graphApplyCycleRelevantDependencyType.
// This is deliberately narrower than the ready-work traversal used by
// validateGraphApplyPlannedParentBlockingPaths — graph-apply must not reject a
// blocking edge whose only return path runs through an existing parent-child or
// waits-for dep when plain `bd dep add` would allow it. The storage per-edge
// check (issueops.CheckDependencyCycleInTx) is a backstop at insert time and
// checks each edge against its OWN type family (blocks-vs-blocks,
// parent-child-vs-parent-child, beads-8qij); this whole-graph pass covers the
// blocking family before any write for a clear plan-level error.
func validateGraphApplyPlannedBlockingCycles(ctx context.Context, tx storage.Transaction, plan *GraphApplyPlan, keyToID map[string]string) error {
	type plannedEdge struct {
		index  int
		fromID string
		toID   string
	}

	adj := make(map[string][]string)
	checks := make([]plannedEdge, 0, len(plan.Edges))
	for i, edge := range plan.Edges {
		depType := graphApplyDependencyType(edge.Type)
		if !graphApplyCycleRelevantDependencyType(depType) {
			continue
		}
		fromID := resolveEdgeRef(edge.FromKey, edge.FromID, keyToID)
		toID := resolveEdgeRef(edge.ToKey, edge.ToID, keyToID)
		if fromID == "" || toID == "" {
			continue
		}
		if fromID == toID {
			return fmt.Errorf("edge %d %s->%s creates a blocking dependency cycle", i, fromID, toID)
		}
		adj[fromID] = append(adj[fromID], toID)
		checks = append(checks, plannedEdge{index: i, fromID: fromID, toID: toID})
	}

	depCache := make(map[string][]*types.Dependency)
	for _, edge := range checks {
		hasPath, err := graphApplyHasPath(ctx, tx, adj, depCache, edge.toID, edge.fromID, graphApplyCycleRelevantDependencyType)
		if err != nil {
			return fmt.Errorf("edge %d %s->%s: checking planned blocking cycle: %w", edge.index, edge.fromID, edge.toID, err)
		}
		if hasPath {
			return fmt.Errorf("edge %d %s->%s creates a blocking dependency cycle", edge.index, edge.fromID, edge.toID)
		}
	}
	return nil
}

// validateGraphApplyPlannedParentBlockingPaths rejects plans where a planned
// blocking edge would create a path from a parent to its child. Unlike
// validateGraphApplyPlannedBlockingCycles, its existing-dep walk follows the
// full AffectsReadyWork set (blocks, conditional-blocks, parent-child,
// waits-for) because a parent→child path closed through any ready-affecting
// dependency is a real ready-work deadlock. The two predicates must stay
// distinct: narrowing this one would miss real deadlocks; broadening the
// blocking-cycle walk would reject edges plain `bd dep add` accepts.
func validateGraphApplyPlannedParentBlockingPaths(ctx context.Context, tx storage.Transaction, plan *GraphApplyPlan, keyToID map[string]string, parentDepPairs map[string]bool) error {
	adj := make(map[string][]string)
	for pair := range parentDepPairs {
		fromID, toID, ok := graphApplyDepPairIDs(pair)
		if ok {
			adj[fromID] = append(adj[fromID], toID)
		}
	}
	for _, edge := range plan.Edges {
		depType := graphApplyDependencyType(edge.Type)
		if !graphApplyReadyPathDependencyType(depType) {
			continue
		}
		fromID := resolveEdgeRef(edge.FromKey, edge.FromID, keyToID)
		toID := resolveEdgeRef(edge.ToKey, edge.ToID, keyToID)
		if fromID == "" || toID == "" {
			continue
		}
		// Direct parent -> child blocking edges have a dedicated error below.
		// This prewrite pass covers transitive parent -> ... -> child paths.
		if graphApplyCycleRelevantDependencyType(depType) && parentDepPairs[graphApplyDepPairKey(toID, fromID)] {
			continue
		}
		adj[fromID] = append(adj[fromID], toID)
	}

	depCache := make(map[string][]*types.Dependency)
	for _, node := range plan.Nodes {
		childID := keyToID[node.Key]
		parentID := node.ParentID
		if node.ParentKey != "" {
			parentID = keyToID[node.ParentKey]
		}
		if childID == "" || parentID == "" {
			continue
		}
		hasPath, err := graphApplyHasPath(ctx, tx, adj, depCache, parentID, childID, graphApplyReadyPathDependencyType)
		if err != nil {
			return err
		}
		if hasPath {
			return fmt.Errorf("node %q: planned blocking dependencies create a path from parent %q to child %q", node.Key, parentID, childID)
		}
	}
	return nil
}

// graphApplyHasPath reports whether fromID can reach toID by following the
// in-memory planned adjacency plus existing store dependencies. followExistingDep
// selects which existing dep types the walk traverses, letting callers mirror
// either the blocking-only SQL cycle check or the broader ready-work graph.
func graphApplyHasPath(ctx context.Context, tx storage.Transaction, adj map[string][]string, depCache map[string][]*types.Dependency, fromID, toID string, followExistingDep func(types.DependencyType) bool) (bool, error) {
	seen := make(map[string]bool)
	var visit func(string) (bool, error)
	visit = func(id string) (bool, error) {
		if id == toID {
			return true, nil
		}
		if seen[id] {
			return false, nil
		}
		seen[id] = true
		for _, next := range adj[id] {
			found, err := visit(next)
			if err != nil || found {
				return found, err
			}
		}
		deps, ok := depCache[id]
		if !ok {
			var err error
			deps, err = tx.GetDependencyRecords(ctx, id)
			if err != nil {
				return false, fmt.Errorf("reading existing dependencies for %s: %w", id, err)
			}
			depCache[id] = deps
		}
		for _, dep := range deps {
			if !followExistingDep(dep.Type) {
				continue
			}
			found, err := visit(dep.DependsOnID)
			if err != nil || found {
				return found, err
			}
		}
		return false, nil
	}
	return visit(fromID)
}

// graphApplyEdgeIsLocalCycleRelevant reports whether an edge participates in the
// in-memory local cycle check run by validateGraphApplyLocalCycles: it must be a
// fully-local edge (both endpoints addressed by key, neither by an existing ID)
// of a cycle-relevant dependency type. The whole-graph preflight now always skips
// the storage SQL cycle probe, so this no longer gates that skip.
func graphApplyEdgeIsLocalCycleRelevant(edge GraphApplyEdge, depType types.DependencyType) bool {
	if edge.FromKey == "" || edge.ToKey == "" || edge.FromID != "" || edge.ToID != "" {
		return false
	}
	return graphApplyCycleRelevantDependencyType(depType)
}

func graphApplyDependencyType(depType string) types.DependencyType {
	if depType == "" {
		return types.DepBlocks
	}
	return types.DependencyType(depType)
}

func graphApplyCycleRelevantDependencyType(depType types.DependencyType) bool {
	return depType == types.DepBlocks || depType == types.DepConditionalBlocks
}

func graphApplyReadyPathDependencyType(depType types.DependencyType) bool {
	return depType.AffectsReadyWork()
}

func graphApplySortedKeys(keys map[string]bool) []string {
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func graphApplyParentDepPairs(nodes []GraphApplyNode, keyToID map[string]string) map[string]bool {
	pairs := make(map[string]bool)
	for _, node := range nodes {
		parentID := node.ParentID
		if node.ParentKey != "" {
			parentID = keyToID[node.ParentKey]
		}
		childID := keyToID[node.Key]
		if childID != "" && parentID != "" {
			pairs[graphApplyDepPairKey(childID, parentID)] = true
		}
	}
	return pairs
}

func graphApplyDepPairKey(issueID, dependsOnID string) string {
	return issueID + "\x00" + dependsOnID
}

func graphApplyDepPairIDs(pair string) (string, string, bool) {
	for i := 0; i < len(pair); i++ {
		if pair[i] == 0 {
			return pair[:i], pair[i+1:], true
		}
	}
	return "", "", false
}

func resolveEdgeRef(key, id string, keyToID map[string]string) string {
	if id != "" {
		return id
	}
	if key != "" {
		return keyToID[key]
	}
	return ""
}
