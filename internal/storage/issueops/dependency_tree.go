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
func GetDependencyTreeInTx(ctx context.Context, tx DBTX, issueID string, maxDepth int, showAllPaths bool, reverse bool) ([]*types.TreeNode, error) {
	visited := make(map[string]bool)
	return buildDependencyTreeInTx(ctx, tx, issueID, 0, maxDepth, reverse, visited, "", "")
}

func buildDependencyTreeInTx(ctx context.Context, tx DBTX, issueID string, depth, maxDepth int, reverse bool, visited map[string]bool, parentID string, edgeFromParent types.DependencyType) ([]*types.TreeNode, error) {
	if depth >= maxDepth || visited[issueID] {
		return nil, nil
	}
	visited[issueID] = true

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
		children, err := buildDependencyTreeInTx(ctx, tx, rel.ID, depth+1, maxDepth, reverse, visited, issueID, rel.DependencyType)
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
