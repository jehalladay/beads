package utils

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// caseFoldStore models the REAL Dolt store's case behavior for ResolvePartialID,
// which is what makes beads-ry0m reachable:
//
//   - The exact-ID filter (filter.IDs -> `id IN (?)`) is under the binary
//     collation utf8mb4_0900_bin, i.e. CASE-SENSITIVE (verified live:
//     SELECT 'MgSx'='mgsx' -> 0).
//   - The substring text query lowercases both sides (transaction.go:292-297
//     `LOWER(...) LIKE ?` + strings.ToLower(query)), i.e. CASE-INSENSITIVE, so
//     the SQL layer LENIENTLY surfaces a lowercase-stored row for an
//     uppercase-typed query.
//
// The shared fakeResolveStore in id_parser_resolve_test.go does a
// case-SENSITIVE substring match, which would hide the bug at the SQL layer for
// the wrong reason. This store reproduces the real asymmetry so the Go
// post-filter (id_parser.go issue.ID==input / hash ==/Contains) is the sole
// reject point — exactly where ry0m lives.
type caseFoldStore struct {
	storage.Storage
	issues []*types.Issue
	config map[string]string
}

func newCaseFoldStore(issues ...*types.Issue) *caseFoldStore {
	return &caseFoldStore{issues: issues, config: map[string]string{"issue_prefix": "bd"}}
}

func (f *caseFoldStore) GetConfig(_ context.Context, key string) (string, error) {
	return f.config[key], nil
}

func (f *caseFoldStore) SearchIssues(_ context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	var out []*types.Issue
	for _, iss := range f.issues {
		if filter.Ephemeral == nil {
			if iss.Ephemeral {
				continue
			}
		} else if iss.Ephemeral != *filter.Ephemeral {
			continue
		}

		// Exact ID filter: CASE-SENSITIVE (binary collation, `id IN (?)`).
		if len(filter.IDs) > 0 {
			matched := false
			for _, id := range filter.IDs {
				if iss.ID == id { // binary == , not EqualFold
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Substring query: CASE-INSENSITIVE (SQL LOWER()/LIKE lowercases).
		if query != "" && !strings.Contains(strings.ToLower(iss.ID), strings.ToLower(query)) {
			continue
		}

		out = append(out, iss)
	}
	return out, nil
}

// TestResolvePartialID_CaseFold pins beads-ry0m: an uppercase- or mixed-case
// typed ID must resolve a lowercase-stored issue. Before the EqualFold fix the
// SQL substring path surfaced the candidate but the byte-exact Go post-filter
// (issue.ID==input, issueHash==hashPart, strings.Contains(issueHash,hashPart))
// discarded it on case, so show/update/close/dep returned "no issue found".
func TestResolvePartialID_CaseFold(t *testing.T) {
	ctx := context.Background()

	t.Run("uppercase-full-id-resolves-lowercase-stored", func(t *testing.T) {
		store := newCaseFoldStore(iss("beads-bi4g"))
		got, err := ResolvePartialID(ctx, store, "BEADS-BI4G")
		if err != nil {
			t.Fatalf(`ResolvePartialID("BEADS-BI4G") error: %v (want it to resolve lowercase-stored beads-bi4g)`, err)
		}
		if got != "beads-bi4g" {
			t.Errorf(`ResolvePartialID("BEADS-BI4G") = %q, want "beads-bi4g"`, got)
		}
	})

	t.Run("uppercase-hash-resolves-lowercase-stored", func(t *testing.T) {
		store := newCaseFoldStore(iss("bd-a3f8e9"))
		got, err := ResolvePartialID(ctx, store, "A3F8E9")
		if err != nil {
			t.Fatalf(`ResolvePartialID("A3F8E9") error: %v (want it to resolve bd-a3f8e9)`, err)
		}
		if got != "bd-a3f8e9" {
			t.Errorf(`ResolvePartialID("A3F8E9") = %q, want "bd-a3f8e9"`, got)
		}
	})

	t.Run("mixed-case-hash-resolves-lowercase-stored", func(t *testing.T) {
		store := newCaseFoldStore(iss("bd-a3f8e9"))
		got, err := ResolvePartialID(ctx, store, "A3f8E9")
		if err != nil {
			t.Fatalf(`ResolvePartialID("A3f8E9") error: %v`, err)
		}
		if got != "bd-a3f8e9" {
			t.Errorf(`ResolvePartialID("A3f8E9") = %q, want "bd-a3f8e9"`, got)
		}
	})

	t.Run("uppercase-wisp-id-resolves-lowercase-stored", func(t *testing.T) {
		store := newCaseFoldStore(wisp("bd-wspxyz"))
		got, err := ResolvePartialID(ctx, store, "BD-WSPXYZ")
		if err != nil {
			t.Fatalf(`ResolvePartialID("BD-WSPXYZ") wisp error: %v`, err)
		}
		if got != "bd-wspxyz" {
			t.Errorf(`ResolvePartialID("BD-WSPXYZ") = %q, want "bd-wspxyz"`, got)
		}
	})

	// TEETH / symmetry lock: IDs are NOT canonically lowercase — the live DB
	// stores gt-rig-polecat-TestAgent (uppercase suffix). A naive
	// ToLower(input) fix would break resolving that ID by its exact typed form;
	// EqualFold is symmetric, so a lowercase-typed input must still resolve an
	// uppercase-STORED id. This case fails under ToLower(input) and passes under
	// EqualFold — it's what pins the fix to the correct direction.
	t.Run("lowercase-input-resolves-uppercase-stored", func(t *testing.T) {
		store := newCaseFoldStore(iss("gt-rig-polecat-testagent-A3F8E9"))
		got, err := ResolvePartialID(ctx, store, "gt-rig-polecat-testagent-a3f8e9")
		if err != nil {
			t.Fatalf(`ResolvePartialID(lowercase for uppercase-stored) error: %v`, err)
		}
		if got != "gt-rig-polecat-testagent-A3F8E9" {
			t.Errorf(`ResolvePartialID(lowercase) = %q, want the uppercase-stored id (EqualFold symmetry)`, got)
		}
	})

	// A genuinely non-matching id must still fail — the case-fold must not
	// over-match unrelated ids.
	t.Run("non-matching-still-fails", func(t *testing.T) {
		store := newCaseFoldStore(iss("bd-a3f8e9"))
		if _, err := ResolvePartialID(ctx, store, "ZZZZZZ"); err == nil {
			t.Error(`ResolvePartialID("ZZZZZZ") = nil error, want not-found (case-fold must not over-match)`)
		}
	})
}
