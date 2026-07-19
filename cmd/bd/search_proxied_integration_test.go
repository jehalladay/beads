//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerSearch proves bd search is proxied-server-aware (beads-iq3i).
// Before the fix, search.go's RunE used the nil global `store` unconditionally
// (loadDirectListFilterConfig(store), store.SearchIssues, store.GetLabelsForIssues,
// store.GetDependencyCounts, store.GetCommentCounts) with no usesProxiedServer()
// gate, so `bd search` failed "storage is nil" for a hub-connected crew.
// This is a clean-mirror leg: every store method maps to an existing UOW usecase
// (IssueUseCase().SearchIssues, LabelUseCase().GetLabelsForIssues,
// DependencyUseCase().CountsByIssueIDs, CommentUseCase().GetCommentCounts) and
// loadProxiedListFilterConfig(uw) already exists, mirroring list_proxied_server.go.
func TestProxiedServerSearch(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "srch")

	hit := bdProxiedCreate(t, bd, p.dir, "authentication login bug", "--type", "bug", "--label", "backend")
	_ = bdProxiedCreate(t, bd, p.dir, "unrelated cleanup task", "--type", "task")

	t.Run("text_match", func(t *testing.T) {
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "search", "authentication")
		if err != nil {
			t.Fatalf("bd search failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd search hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, hit.ID) {
			t.Errorf("expected search to return %s for 'authentication':\n%s", hit.ID, stdout)
		}
	})

	t.Run("json_with_counts_and_labels", func(t *testing.T) {
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "search", "authentication", "--json")
		if err != nil {
			t.Fatalf("bd search --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd search --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		var results []struct {
			ID     string   `json:"id"`
			Labels []string `json:"labels"`
		}
		if uerr := json.Unmarshal([]byte(stdout), &results); uerr != nil {
			t.Fatalf("bd search --json produced invalid JSON: %v\n%s", uerr, stdout)
		}
		var found bool
		for _, r := range results {
			if r.ID == hit.ID {
				found = true
				if !containsString(r.Labels, "backend") {
					t.Errorf("expected label 'backend' on %s in JSON output, got %v", hit.ID, r.Labels)
				}
			}
		}
		if !found {
			t.Errorf("expected %s in --json search results:\n%s", hit.ID, stdout)
		}
	})

	t.Run("no_match_clean", func(t *testing.T) {
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "search", "zzzznomatchzzzz")
		if err != nil {
			t.Fatalf("bd search (no match) should exit 0: %v\nstderr:\n%s", err, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd search (no match) hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
	})
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
