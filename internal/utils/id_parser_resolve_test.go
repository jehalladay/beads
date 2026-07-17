package utils

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// fakeResolveStore is a minimal in-memory storage.Storage used to exercise
// ResolvePartialID/ResolvePartialIDs without a Dolt server. It embeds the
// storage.Storage interface so it satisfies the full method set; only the two
// methods the resolver touches (SearchIssues + GetConfig) are implemented.
// Every other method is nil and panics if called — which is the point: these
// tests must not reach the rest of the store.
//
// SearchIssues models a transaction-scoped store: a nil Ephemeral filter
// returns persistent issues only (it does NOT merge wisps). This is exactly
// the store shape ResolvePartialID's wisp-fallback branch defends against, so
// modeling it this way makes that branch reachable.
type fakeResolveStore struct {
	storage.Storage

	issues []*types.Issue

	// config backs GetConfig lookups (e.g. issue_prefix, allowed_prefixes).
	config map[string]string
	// configErr, when set for a key, is returned by GetConfig for that key.
	configErr map[string]error

	// searchErr, when non-nil, is returned by every SearchIssues call.
	searchErr error
}

func newResolveStore(issues ...*types.Issue) *fakeResolveStore {
	return &fakeResolveStore{
		issues:    issues,
		config:    map[string]string{"issue_prefix": "bd"},
		configErr: make(map[string]error),
	}
}

func (f *fakeResolveStore) GetConfig(_ context.Context, key string) (string, error) {
	if err, ok := f.configErr[key]; ok {
		return "", err
	}
	return f.config[key], nil
}

func (f *fakeResolveStore) SearchIssues(_ context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}

	var out []*types.Issue
	for _, iss := range f.issues {
		// Ephemeral filter: nil = persistent-only (transaction-scoped model),
		// non-nil = must match exactly.
		if filter.Ephemeral == nil {
			if iss.Ephemeral {
				continue
			}
		} else if iss.Ephemeral != *filter.Ephemeral {
			continue
		}

		// Exact ID filter (used by the fast path and the normalized-ID path).
		if len(filter.IDs) > 0 {
			matched := false
			for _, id := range filter.IDs {
				if iss.ID == id {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Substring query mirrors the real store's SQL `id LIKE %query%`.
		if query != "" && !strings.Contains(iss.ID, query) {
			continue
		}

		out = append(out, iss)
	}
	return out, nil
}

func iss(id string) *types.Issue  { return &types.Issue{ID: id} }
func wisp(id string) *types.Issue { return &types.Issue{ID: id, Ephemeral: true} }

func TestResolvePartialID_NilStore(t *testing.T) {
	if _, err := ResolvePartialID(context.Background(), nil, "abc"); err == nil {
		t.Fatal("expected error for nil store, got nil")
	}
}

func TestResolvePartialID_Pure(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		issues  []*types.Issue
		config  map[string]string
		want    string
		wantErr string // substring of the expected error; "" means expect success
	}{
		{
			name:   "fast path exact input match",
			input:  "bd-a3f8e9",
			issues: []*types.Issue{iss("bd-a3f8e9")},
			want:   "bd-a3f8e9",
		},
		{
			name:   "bare hash gets prefix and matches on normalized id",
			input:  "a3f8e9",
			issues: []*types.Issue{iss("bd-a3f8e9")},
			want:   "bd-a3f8e9",
		},
		{
			name:   "different known prefix used as-is via allowed_prefixes",
			input:  "hacker-news-ko4",
			issues: []*types.Issue{iss("hacker-news-ko4")},
			config: map[string]string{"issue_prefix": "bd", "allowed_prefixes": "hacker-news, bd"},
			want:   "hacker-news-ko4",
		},
		{
			name:   "different heuristic prefix used as-is (cross-prefix)",
			input:  "aap-4ar",
			issues: []*types.Issue{iss("aap-4ar")},
			want:   "aap-4ar",
		},
		{
			name:   "partial hash unique substring match",
			input:  "a3f8",
			issues: []*types.Issue{iss("bd-a3f8e9"), iss("bd-zzzzzz")},
			want:   "bd-a3f8e9",
		},
		{
			name:   "exact hash match preferred over substring",
			input:  "a3f8",
			issues: []*types.Issue{iss("bd-a3f8"), iss("bd-a3f8e9")},
			want:   "bd-a3f8",
		},
		{
			name:    "ambiguous partial matches multiple",
			input:   "a3",
			issues:  []*types.Issue{iss("bd-a3f8e9"), iss("bd-a3c1d2")},
			wantErr: "ambiguous",
		},
		{
			name:    "no match at all",
			input:   "zzzzzz",
			issues:  []*types.Issue{iss("bd-a3f8e9")},
			wantErr: "no issue found",
		},
		{
			name:    "unsearchable input (space) yields no-issue error",
			input:   "bd nope",
			issues:  []*types.Issue{iss("bd-a3f8e9")},
			wantErr: "no issue found",
		},
		{
			name:   "wisp resolved by partial id via ephemeral fallback",
			input:  "w1a2b3",
			issues: []*types.Issue{wisp("bd-w1a2b3")},
			want:   "bd-w1a2b3",
		},
		{
			name:   "wisp exact-hash match via ephemeral fallback",
			input:  "w1a2",
			issues: []*types.Issue{wisp("bd-w1a2"), wisp("bd-w1a2b3")},
			want:   "bd-w1a2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newResolveStore(tt.issues...)
			if tt.config != nil {
				store.config = tt.config
			}
			got, err := ResolvePartialID(context.Background(), store, tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (result %q)", tt.wantErr, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolvePartialID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolvePartialID_SearchError(t *testing.T) {
	store := newResolveStore(iss("bd-a3f8e9"))
	store.searchErr = errors.New("boom")
	// A bare-hash input skips the fast path result (search errors -> len==0),
	// falls through to the substring search which surfaces the error.
	if _, err := ResolvePartialID(context.Background(), store, "a3f8"); err == nil {
		t.Fatal("expected error to propagate from SearchIssues, got nil")
	}
}

func TestResolvePartialID_PrefixConfigFallback(t *testing.T) {
	// GetConfig("issue_prefix") errors -> resolver falls back to "bd".
	store := newResolveStore(iss("bd-a3f8e9"))
	store.config = map[string]string{}
	store.configErr = map[string]error{"issue_prefix": errors.New("no config")}
	got, err := ResolvePartialID(context.Background(), store, "a3f8e9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "bd-a3f8e9" {
		t.Fatalf("got %q, want bd-a3f8e9", got)
	}
}

func TestResolvePartialIDs_Pure(t *testing.T) {
	store := newResolveStore(iss("bd-a3f8e9"), iss("bd-b1c2d3"))

	got, err := ResolvePartialIDs(context.Background(), store, []string{"a3f8e9", "b1c2d3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"bd-a3f8e9", "bd-b1c2d3"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolvePartialIDs_ErrorStopsAndReturnsNil(t *testing.T) {
	store := newResolveStore(iss("bd-a3f8e9"))
	got, err := ResolvePartialIDs(context.Background(), store, []string{"a3f8e9", "zzzzzz"})
	if err == nil {
		t.Fatal("expected error for unresolvable second id, got nil")
	}
	if got != nil {
		t.Fatalf("expected nil result on error, got %v", got)
	}
}

func TestHasKnownPrefix(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		knownPrefixes []string
		want          bool
	}{
		{"multi-hyphen prefix matches", "hacker-news-ko4", []string{"hacker-news"}, true},
		{"simple prefix matches", "bd-abc", []string{"bd"}, true},
		{"no matching prefix", "hq-1", []string{"bd"}, false},
		{"bare hash no prefix", "abc", []string{"bd"}, false},
		{"empty prefix in list ignored", "abc", []string{""}, false},
		{"prefix without following hyphen", "bdabc", []string{"bd"}, false},
		{"first of several prefixes", "hq-9", []string{"bd", "hq"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasKnownPrefix(tt.input, tt.knownPrefixes); got != tt.want {
				t.Fatalf("hasKnownPrefix(%q, %v) = %v, want %v", tt.input, tt.knownPrefixes, got, tt.want)
			}
		})
	}
}
