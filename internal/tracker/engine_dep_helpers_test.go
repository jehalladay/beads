// Table-driven unit tests for the engine dependency helpers that carried 0%
// coverage (beads-pej, C1 agentic-tdd under beads-r06). These exercise
// dependencyExists, pendingDependencyPreviewKey, buildDescendantSet, and
// ResolveState with an in-memory storage mock — no CGO, network, or Dolt.

package tracker

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// depGraphStore is a minimal storage.Storage that answers the dependency
// metadata queries used by dependencyExists and buildDescendantSet. Embedding
// storage.Storage (nil) satisfies the interface; only the two methods under
// test are implemented. deps maps an issue ID to its dependencies; dependents
// maps an issue ID to the issues that depend on it.
type depGraphStore struct {
	storage.Storage
	deps       map[string][]*types.IssueWithDependencyMetadata
	dependents map[string][]*types.IssueWithDependencyMetadata
	depErr     error
	dependErr  error
}

func (s *depGraphStore) GetDependenciesWithMetadata(_ context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	if s.depErr != nil {
		return nil, s.depErr
	}
	return s.deps[issueID], nil
}

func (s *depGraphStore) GetDependentsWithMetadata(_ context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	if s.dependErr != nil {
		return nil, s.dependErr
	}
	return s.dependents[issueID], nil
}

func depMeta(id string, t types.DependencyType) *types.IssueWithDependencyMetadata {
	m := &types.IssueWithDependencyMetadata{DependencyType: t}
	m.Issue.ID = id
	return m
}

func TestPendingDependencyPreviewKey(t *testing.T) {
	tests := []struct {
		name             string
		from, to, dtype  string
		other            [3]string // a second key to compare against
		wantEqualToOther bool
	}{
		{
			name: "identical inputs collide",
			from: "a", to: "b", dtype: "blocks",
			other:            [3]string{"a", "b", "blocks"},
			wantEqualToOther: true,
		},
		{
			name: "whitespace is trimmed so padded == bare",
			from: " a ", to: "\tb", dtype: "blocks ",
			other:            [3]string{"a", "b", "blocks"},
			wantEqualToOther: true,
		},
		{
			name: "different type does not collide",
			from: "a", to: "b", dtype: "blocks",
			other:            [3]string{"a", "b", "parent-child"},
			wantEqualToOther: false,
		},
		{
			name: "swapped endpoints do not collide",
			from: "a", to: "b", dtype: "blocks",
			other:            [3]string{"b", "a", "blocks"},
			wantEqualToOther: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pendingDependencyPreviewKey(tt.from, tt.to, tt.dtype)
			other := pendingDependencyPreviewKey(tt.other[0], tt.other[1], tt.other[2])
			if (got == other) != tt.wantEqualToOther {
				t.Errorf("key(%q,%q,%q)=%q vs key(%v)=%q; equal=%v, want %v",
					tt.from, tt.to, tt.dtype, got, tt.other, other, got == other, tt.wantEqualToOther)
			}
		})
	}
}

func TestDependencyExists(t *testing.T) {
	ctx := context.Background()

	baseStore := &depGraphStore{
		deps: map[string][]*types.IssueWithDependencyMetadata{
			"a": {
				depMeta("b", types.DepBlocks),
				depMeta("c", types.DepParentChild),
			},
		},
	}

	tests := []struct {
		name                 string
		store                *depGraphStore
		issueID, dependsOnID string
		depType              types.DependencyType
		want                 bool
	}{
		{"blank issueID", baseStore, "  ", "b", types.DepBlocks, false},
		{"blank dependsOnID", baseStore, "a", "", types.DepBlocks, false},
		{"exists (blocks)", baseStore, "a", "b", types.DepBlocks, true},
		{"exists (parent-child)", baseStore, "a", "c", types.DepParentChild, true},
		{"target present but wrong type", baseStore, "a", "b", types.DepParentChild, false},
		{"target absent", baseStore, "a", "z", types.DepBlocks, false},
		{"no records for issue", baseStore, "unknown", "b", types.DepBlocks, false},
		{"store error is treated as not-exists",
			&depGraphStore{depErr: errors.New("boom")}, "a", "b", types.DepBlocks, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dependencyExists(ctx, tt.store, tt.issueID, tt.dependsOnID, tt.depType)
			if got != tt.want {
				t.Errorf("dependencyExists(%q,%q,%q) = %v, want %v",
					tt.issueID, tt.dependsOnID, tt.depType, got, tt.want)
			}
		})
	}
}

func TestBuildDescendantSet(t *testing.T) {
	ctx := context.Background()

	// Graph (parent-child edges, plus one non-parent-child edge that must be ignored):
	//   root -> childA -> grandchild
	//   root -> childB
	//   root -(blocks)-> blockedNode   (NOT a descendant)
	store := &depGraphStore{
		dependents: map[string][]*types.IssueWithDependencyMetadata{
			"root": {
				depMeta("childA", types.DepParentChild),
				depMeta("childB", types.DepParentChild),
				depMeta("blockedNode", types.DepBlocks),
			},
			"childA": {depMeta("grandchild", types.DepParentChild)},
		},
	}

	t.Run("collects transitive parent-child descendants only", func(t *testing.T) {
		e := &Engine{Store: store}
		got, err := e.buildDescendantSet(ctx, "root")
		if err != nil {
			t.Fatalf("buildDescendantSet: %v", err)
		}
		want := map[string]bool{"root": true, "childA": true, "childB": true, "grandchild": true}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for id := range want {
			if !got[id] {
				t.Errorf("missing descendant %q in %v", id, got)
			}
		}
		if got["blockedNode"] {
			t.Errorf("blockedNode reached via a non-parent-child edge; should be excluded: %v", got)
		}
	})

	t.Run("leaf parent yields only itself", func(t *testing.T) {
		e := &Engine{Store: store}
		got, err := e.buildDescendantSet(ctx, "grandchild")
		if err != nil {
			t.Fatalf("buildDescendantSet: %v", err)
		}
		if len(got) != 1 || !got["grandchild"] {
			t.Errorf("got %v, want only {grandchild}", got)
		}
	})

	t.Run("store error propagates", func(t *testing.T) {
		e := &Engine{Store: &depGraphStore{dependErr: errors.New("boom")}}
		if _, err := e.buildDescendantSet(ctx, "root"); err == nil {
			t.Error("expected error when GetDependentsWithMetadata fails")
		}
	})
}

func TestEngineResolveState(t *testing.T) {
	t.Run("nil PushHooks returns not-ok", func(t *testing.T) {
		e := &Engine{}
		if id, ok := e.ResolveState(types.StatusOpen); ok || id != "" {
			t.Errorf("ResolveState() = (%q, %v), want (\"\", false) with nil hooks", id, ok)
		}
	})

	t.Run("nil ResolveState hook returns not-ok", func(t *testing.T) {
		e := &Engine{PushHooks: &PushHooks{}, stateCache: struct{}{}}
		if id, ok := e.ResolveState(types.StatusOpen); ok || id != "" {
			t.Errorf("ResolveState() = (%q, %v), want (\"\", false) with nil hook", id, ok)
		}
	})

	t.Run("nil stateCache returns not-ok", func(t *testing.T) {
		e := &Engine{PushHooks: &PushHooks{
			ResolveState: func(interface{}, types.Status) (string, bool) { return "state-1", true },
		}}
		if id, ok := e.ResolveState(types.StatusOpen); ok || id != "" {
			t.Errorf("ResolveState() = (%q, %v), want (\"\", false) with nil stateCache", id, ok)
		}
	})

	t.Run("delegates to hook when fully wired", func(t *testing.T) {
		var gotStatus types.Status
		e := &Engine{
			stateCache: "cache",
			PushHooks: &PushHooks{
				ResolveState: func(cache interface{}, status types.Status) (string, bool) {
					gotStatus = status
					if cache != "cache" {
						t.Errorf("ResolveState got cache %v, want \"cache\"", cache)
					}
					return "state-42", true
				},
			},
		}
		id, ok := e.ResolveState(types.StatusClosed)
		if !ok || id != "state-42" {
			t.Errorf("ResolveState() = (%q, %v), want (\"state-42\", true)", id, ok)
		}
		if gotStatus != types.StatusClosed {
			t.Errorf("hook received status %v, want %v", gotStatus, types.StatusClosed)
		}
	})
}
