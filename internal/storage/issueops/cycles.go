package issueops

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// DetectCyclesInTx finds dependency cycles across both the dependencies and
// wisp_dependencies tables. Returns slices of issues forming each cycle.
//
// It audits EVERY edge-type family that the create-time cycle invariant
// (cycleCheckTypesFor / cycleAuditedFamilies) protects — blocks+conditional,
// parent-child (beads-8qij), and supersedes (beads-8ix02) — not just the
// blocking family. Earlier this detector graphed only blocks/conditional, so a
// parent-child or supersedes cycle (reachable when a Dolt branch/clone MERGE
// combines two individually-acyclic halves, since the per-edge write guard runs
// only per-transaction, never on merge) was invisible: `bd dep cycles` reported
// a false "no cycles" all-clear on exactly the families it exists to verify
// (beads-cjvxq). Each family is detected against its OWN graph — a
// blocks→parent-child→blocks path is not a real cycle in either family — and
// the family set is derived from the SAME source as the write guard so the two
// can never drift again.
func DetectCyclesInTx(ctx context.Context, tx DBTX) ([][]*types.Issue, error) {
	depTables := []string{"dependencies", "wisp_dependencies"}

	// Detect cycles within each acyclic family separately, deduplicating by the
	// set of node IDs so a family that is graphed under more than one alias does
	// not double-report (families are disjoint here, but this keeps the contract
	// robust if that changes).
	var cycles [][]*types.Issue
	seen := make(map[string]bool)
	for _, family := range cycleAuditedFamilies() {
		graph := make(map[string][]string)
		if err := appendGraphForTypesInTx(ctx, tx, depTables, family, graph); err != nil {
			return nil, err
		}
		for _, cyclePath := range findCyclesInGraph(graph) {
			key := cycleKey(cyclePath)
			if seen[key] {
				continue
			}
			seen[key] = true
			var cycleIssues []*types.Issue
			for _, id := range cyclePath {
				issue, _ := GetIssueInTx(ctx, tx, id)
				if issue != nil {
					cycleIssues = append(cycleIssues, issue)
				}
			}
			if len(cycleIssues) > 0 {
				cycles = append(cycles, cycleIssues)
			}
		}
	}

	return cycles, nil
}

// cycleKey returns an order-independent key for a cycle's node set so the same
// cycle discovered from different start nodes (or via different family aliases)
// is reported once.
func cycleKey(cyclePath []string) string {
	sorted := make([]string, len(cyclePath))
	copy(sorted, cyclePath)
	sort.Strings(sorted)
	return strings.Join(sorted, "\x00")
}

// findCyclesInGraph returns one node path per cycle found in the adjacency
// list via DFS. Each path starts and continues to (but does not repeat) the
// back-edge target. Pure function — no I/O — so cycle-detection logic is unit
// testable independent of the database (beads-cjvxq).
func findCyclesInGraph(graph map[string][]string) [][]string {
	var cycles [][]string
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	path := make([]string, 0)

	var dfs func(node string) bool
	dfs = func(node string) bool {
		visited[node] = true
		recStack[node] = true
		path = append(path, node)

		for _, neighbor := range graph[node] {
			if !visited[neighbor] {
				if dfs(neighbor) {
					return true
				}
			} else if recStack[neighbor] {
				// Found cycle — extract it.
				cycleStart := -1
				for i, n := range path {
					if n == neighbor {
						cycleStart = i
						break
					}
				}
				if cycleStart >= 0 {
					cyclePath := make([]string, len(path)-cycleStart)
					copy(cyclePath, path[cycleStart:])
					cycles = append(cycles, cyclePath)
				}
			}
		}

		path = path[:len(path)-1]
		recStack[node] = false
		return false
	}

	// Deterministic iteration order so the reported cycle is stable across runs.
	nodes := make([]string, 0, len(graph))
	for node := range graph {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		if !visited[node] {
			dfs(node)
		}
	}

	return cycles
}

// AppendBlockingGraphInTx adds the blocking-type ("blocks",
// "conditional-blocks") dependency edges from the given tables on tx into
// graph as adjacency lists. The caller may merge tables read from different
// transactions into one graph (dolt server mode keeps wisp writes on a
// separate ignored tx). Used by the per-edge write guard's CycleThroughEdges,
// which is intentionally blocks-only; the read-side audit uses
// appendGraphForTypesInTx per acyclic family instead.
func AppendBlockingGraphInTx(ctx context.Context, tx DBTX, depTables []string, graph map[string][]string) error {
	return appendGraphForTypesInTx(ctx, tx, depTables,
		[]string{string(types.DepBlocks), string(types.DepConditionalBlocks)}, graph)
}

// appendGraphForTypesInTx adds the edges whose type is in wantTypes from the
// given tables on tx into graph as adjacency lists. wantTypes comes from the
// cycle-audited family set (fixed constants), never user input.
//
//nolint:gosec // G201: depTable is hardcoded to "dependencies" or "wisp_dependencies"
func appendGraphForTypesInTx(ctx context.Context, tx DBTX, depTables []string, wantTypes []string, graph map[string][]string) error {
	want := make(map[string]bool, len(wantTypes))
	for _, t := range wantTypes {
		want[t] = true
	}
	for _, depTable := range depTables {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
			SELECT issue_id, %s AS depends_on_id, type
			FROM %s
		`, DepTargetExpr, depTable))
		if err != nil {
			return fmt.Errorf("cycle graph: query %s: %w", depTable, err)
		}
		for rows.Next() {
			var issueID, dependsOnID, depType string
			if err := rows.Scan(&issueID, &dependsOnID, &depType); err != nil {
				_ = rows.Close()
				return fmt.Errorf("cycle graph: scan %s: %w", depTable, err)
			}
			if want[depType] {
				graph[issueID] = append(graph[issueID], dependsOnID)
			}
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("cycle graph: rows %s: %w", depTable, err)
		}
	}
	return nil
}

// CycleThroughEdgesInGraph reports a rendered blocking cycle that traverses
// one of the new edges (issueID -> dependsOnID pairs), or "" when no new edge
// lies on a cycle. An edge u -> v is on a cycle exactly when u is reachable
// from v, so this is precise where cycle enumeration is not: a DFS-based
// detector records one cycle per back edge and can report a pre-existing
// cycle through the same nodes instead of the one the new edge created
// (bd-578h9.9). The graph must already contain the new edges.
func CycleThroughEdgesInGraph(graph map[string][]string, edges [][2]string) string {
	for _, edge := range edges {
		source, target := edge[0], edge[1]
		if source == "" || target == "" {
			continue
		}
		if source == target {
			return source + " → " + source
		}
		path := reachPath(graph, target, source)
		if path == nil {
			continue
		}
		// path runs target ⇝ source inclusive; the new edge closes the cycle.
		ids := append([]string{source}, path...)
		return strings.Join(ids, " → ")
	}
	return ""
}

// reachPath returns a BFS path from start to goal in graph (inclusive of
// both), or nil when goal is unreachable. start == goal returns [start].
func reachPath(graph map[string][]string, start, goal string) []string {
	if start == goal {
		return []string{start}
	}
	parent := map[string]string{start: ""}
	queue := []string{start}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, next := range graph[node] {
			if _, seen := parent[next]; seen {
				continue
			}
			parent[next] = node
			if next == goal {
				path := []string{goal}
				for at := node; at != ""; at = parent[at] {
					path = append(path, at)
				}
				// Reverse: built goal-back-to-start.
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				return path
			}
			queue = append(queue, next)
		}
	}
	return nil
}
