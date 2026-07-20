package issueops

import (
	"context"
	"errors"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// GetDependencyTreeInTx returns a flattened dependency tree for visualization.
// It performs a recursive BFS traversal up to maxDepth, using GetIssueInTx and
// GetDependenciesInTx/GetDependentsInTx which handle wisp routing.
//
// showAllPaths controls diamond/DAG handling (beads-lhq7s): when false (the
// default), a node reachable by more than one path is fully expanded on its
// FIRST visit and emitted as a shallow re-visit node (rendered "(shown above)")
// on later visits, so its edges are never silently dropped. When true, the
// cross-path dedup is skipped so every path is fully expanded, gated only by a
// per-path recursion-stack guard against true cycles.
func GetDependencyTreeInTx(ctx context.Context, tx DBTX, issueID string, maxDepth int, showAllPaths bool, reverse bool) ([]*types.TreeNode, error) {
	visited := make(map[string]bool)
	onPath := make(map[string]bool)
	return buildDependencyTreeInTx(ctx, tx, issueID, 0, maxDepth, reverse, showAllPaths, visited, onPath, "", "")
}

func buildDependencyTreeInTx(ctx context.Context, tx DBTX, issueID string, depth, maxDepth int, reverse, showAllPaths bool, visited, onPath map[string]bool, parentID string, edgeFromParent types.DependencyType) ([]*types.TreeNode, error) {
	if depth >= maxDepth {
		return nil, nil
	}

	// Cross-path dedup (default). A node reachable by >1 path is emitted as a
	// shallow re-visit node so the renderer prints its "(shown above)" diamond
	// marker (cmd/bd/dep.go renderNode r.seen) — previously the builder returned
	// nil BEFORE emitting, so the node rendered as a bare childless leaf with its
	// real edges silently dropped (beads-lhq7s defect 1). --show-all-paths skips
	// this so every path is fully expanded (defect 2).
	if !showAllPaths && visited[issueID] {
		issue, err := GetIssueInTx(ctx, tx, issueID)
		if err != nil {
			if depth > 0 && errors.Is(err, storage.ErrNotFound) {
				return []*types.TreeNode{{
					Issue:          unresolvedDepTargetIssue(issueID),
					Depth:          depth,
					ParentID:       parentID,
					EdgeFromParent: edgeFromParent,
				}}, nil
			}
			return nil, err
		}
		return []*types.TreeNode{{
			Issue:          *issue,
			Depth:          depth,
			ParentID:       parentID,
			EdgeFromParent: edgeFromParent,
		}}, nil
	}

	// Per-path recursion-stack guard: prevents infinite recursion on a true
	// dependency cycle regardless of showAllPaths (in --show-all-paths mode the
	// cross-path `visited` dedup is off, so this is the ONLY cycle protection).
	if onPath[issueID] {
		return nil, nil
	}

	visited[issueID] = true
	onPath[issueID] = true
	defer func() { onPath[issueID] = false }()

	issue, err := GetIssueInTx(ctx, tx, issueID)
	if err != nil {
		// beads-s34r: a CHILD (depth > 0) can be an unresolved external /
		// cross-prefix / not-yet-synced target. `dep add` intentionally allows
		// such edges (no existence validation) and GetDependenciesWithMetadataInTx
		// already surfaces them as placeholders (beads-n49j), so recursing into one
		// and re-fetching it must NOT abort the whole render — otherwise a single
		// unresolved child turns `bd dep tree` into rc1 "not found" for the entire
		// tree. Emit a placeholder leaf node (same rendering the metadata query
		// uses) and stop descending; it has no locally-resolvable dependencies.
		// A not-found ROOT (depth 0) is a genuine "no such issue" error and still
		// propagates.
		if depth > 0 && errors.Is(err, storage.ErrNotFound) {
			return []*types.TreeNode{{
				Issue:          unresolvedDepTargetIssue(issueID),
				Depth:          depth,
				ParentID:       parentID,
				EdgeFromParent: edgeFromParent,
			}}, nil
		}
		return nil, err
	}

	// Use metadata-aware queries to get dependency type for tree annotation (GH#3565).
	var related []*types.IssueWithDependencyMetadata
	if reverse {
		related, err = GetDependentsWithMetadataInTx(ctx, tx, issueID)
	} else {
		related, err = GetDependenciesWithMetadataInTx(ctx, tx, issueID)
	}
	if err != nil {
		return nil, err
	}

	node := &types.TreeNode{
		Issue:          *issue,
		Depth:          depth,
		ParentID:       parentID,
		EdgeFromParent: edgeFromParent,
	}

	// TreeNode doesn't have Children field - return flat list
	nodes := []*types.TreeNode{node}
	for _, rel := range related {
		if !isDependencyTreeEdge(rel.DependencyType) {
			continue
		}
		children, err := buildDependencyTreeInTx(ctx, tx, rel.ID, depth+1, maxDepth, reverse, showAllPaths, visited, onPath, issueID, rel.DependencyType)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, children...)
	}

	return nodes, nil
}

func isDependencyTreeEdge(depType types.DependencyType) bool {
	return depType != types.DepRelatesTo
}
