package ado

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// TestBuildBootstrapIndex verifies the three lookup maps are populated from the
// issue slice: external_ref (only non-empty refs), source_system (only ado:
// prefixed), and title (lowercased, multi-valued).
func TestBuildBootstrapIndex(t *testing.T) {
	issues := []*types.Issue{
		{ID: "bd-1", Title: "Fix Login", ExternalRef: strPtr("https://dev.azure.com/o/p/_workitems/edit/1"), SourceSystem: "ado:1"},
		{ID: "bd-2", Title: "fix login", ExternalRef: strPtr("")}, // empty ref must be skipped
		{ID: "bd-3", Title: "Other", SourceSystem: "github:99"},   // non-ado source skipped
	}

	idx := BuildBootstrapIndex(issues)

	if got, ok := idx.ExternalRefMap["https://dev.azure.com/o/p/_workitems/edit/1"]; !ok || got.ID != "bd-1" {
		t.Errorf("ExternalRefMap lookup = %v, ok=%v, want bd-1", got, ok)
	}
	if len(idx.ExternalRefMap) != 1 {
		t.Errorf("ExternalRefMap size = %d, want 1 (empty ref skipped)", len(idx.ExternalRefMap))
	}
	if got, ok := idx.SourceSystemMap["1"]; !ok || got.ID != "bd-1" {
		t.Errorf("SourceSystemMap[1] = %v, ok=%v, want bd-1", got, ok)
	}
	if _, ok := idx.SourceSystemMap["99"]; ok {
		t.Error("SourceSystemMap should not contain the github source id")
	}
	// Both bd-1 and bd-2 share the lowercased title "fix login".
	if titled := idx.TitleMap["fix login"]; len(titled) != 2 {
		t.Errorf("TitleMap[\"fix login\"] size = %d, want 2 (case-insensitive)", len(titled))
	}
}

// TestFindMatchIndexed_NilIndex covers the nil-index guard.
func TestFindMatchIndexed_NilIndex(t *testing.T) {
	m := NewBootstrapMatcher(NewFieldMapper(nil, nil), true)
	if res := m.FindMatchIndexed(&tracker.TrackerIssue{ID: "1"}, nil); res.Matched {
		t.Errorf("FindMatchIndexed(nil idx) = %+v, want no match", res)
	}
}

// TestFindMatchIndexed_ExternalRef covers the O(1) external-ref hit.
func TestFindMatchIndexed_ExternalRef(t *testing.T) {
	m := NewBootstrapMatcher(NewFieldMapper(nil, nil), false)
	url := "https://dev.azure.com/o/p/_workitems/edit/42"
	idx := BuildBootstrapIndex([]*types.Issue{{ID: "bd-42", Title: "T", ExternalRef: strPtr(url)}})

	res := m.FindMatchIndexed(&tracker.TrackerIssue{ID: "42", URL: url}, idx)
	if !res.Matched || res.BeadsID != "bd-42" || res.MatchType != "external_ref" {
		t.Errorf("FindMatchIndexed = %+v, want match bd-42 via external_ref", res)
	}
}

// TestFindMatchIndexed_SourceSystem covers the O(1) source-system hit.
func TestFindMatchIndexed_SourceSystem(t *testing.T) {
	m := NewBootstrapMatcher(NewFieldMapper(nil, nil), false)
	idx := BuildBootstrapIndex([]*types.Issue{{ID: "bd-7", Title: "T", SourceSystem: "ado:7"}})

	res := m.FindMatchIndexed(&tracker.TrackerIssue{ID: "7", URL: "no-match"}, idx)
	if !res.Matched || res.BeadsID != "bd-7" || res.MatchType != "source_system" {
		t.Errorf("FindMatchIndexed = %+v, want match bd-7 via source_system", res)
	}
}

// TestFindMatchIndexed_Heuristic covers the opt-in heuristic single-candidate,
// multi-candidate (ambiguous → no match with count), time-window, type-mismatch,
// and no-match arms.
func TestFindMatchIndexed_Heuristic(t *testing.T) {
	now := time.Now()
	mkAdo := func() *tracker.TrackerIssue {
		return &tracker.TrackerIssue{ID: "1", Title: "Fix Login Bug", Type: "Bug", URL: "u", CreatedAt: now}
	}

	t.Run("single candidate matches", func(t *testing.T) {
		m := NewBootstrapMatcher(NewFieldMapper(nil, nil), true)
		idx := BuildBootstrapIndex([]*types.Issue{
			{ID: "bd-10", Title: "Fix Login Bug", IssueType: types.TypeBug, CreatedAt: now.Add(time.Hour)},
		})
		res := m.FindMatchIndexed(mkAdo(), idx)
		if !res.Matched || res.BeadsID != "bd-10" || res.MatchType != "heuristic" || res.Candidates != 1 {
			t.Errorf("FindMatchIndexed = %+v, want heuristic bd-10 cand=1", res)
		}
	})

	t.Run("multiple candidates are ambiguous", func(t *testing.T) {
		m := NewBootstrapMatcher(NewFieldMapper(nil, nil), true)
		idx := BuildBootstrapIndex([]*types.Issue{
			{ID: "bd-10", Title: "Fix Login Bug", IssueType: types.TypeBug, CreatedAt: now},
			{ID: "bd-11", Title: "Fix Login Bug", IssueType: types.TypeBug, CreatedAt: now},
		})
		res := m.FindMatchIndexed(mkAdo(), idx)
		if res.Matched || res.Candidates != 2 {
			t.Errorf("FindMatchIndexed = %+v, want no match with 2 candidates", res)
		}
	})

	t.Run("outside time window is not a candidate", func(t *testing.T) {
		m := NewBootstrapMatcher(NewFieldMapper(nil, nil), true)
		idx := BuildBootstrapIndex([]*types.Issue{
			{ID: "bd-10", Title: "Fix Login Bug", IssueType: types.TypeBug, CreatedAt: now.Add(48 * time.Hour)},
		})
		if res := m.FindMatchIndexed(mkAdo(), idx); res.Matched {
			t.Errorf("FindMatchIndexed = %+v, want no match outside 24h window", res)
		}
	})

	t.Run("type mismatch is not a candidate", func(t *testing.T) {
		m := NewBootstrapMatcher(NewFieldMapper(nil, nil), true)
		idx := BuildBootstrapIndex([]*types.Issue{
			{ID: "bd-10", Title: "Fix Login Bug", IssueType: types.TypeFeature, CreatedAt: now},
		})
		if res := m.FindMatchIndexed(mkAdo(), idx); res.Matched {
			t.Errorf("FindMatchIndexed = %+v, want no match on type mismatch", res)
		}
	})

	t.Run("no title match returns empty", func(t *testing.T) {
		m := NewBootstrapMatcher(NewFieldMapper(nil, nil), true)
		idx := BuildBootstrapIndex([]*types.Issue{
			{ID: "bd-10", Title: "Something Else", IssueType: types.TypeBug, CreatedAt: now},
		})
		if res := m.FindMatchIndexed(mkAdo(), idx); res.Matched {
			t.Errorf("FindMatchIndexed = %+v, want no match", res)
		}
	})
}
