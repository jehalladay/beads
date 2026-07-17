package query

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestTypeAliasNormalizeParity pins beads-123i tooth (1): a `type=<alias>` value
// must resolve identically in AND/bare filter mode and in OR predicate mode.
// Before the fix, applyTypeFilter (EQ) normalized the alias (feat->feature) but
// buildTypePredicate and the NOT-type filter branch did ToLower only, so
// `type=feat` matched a feature issue in AND mode but silently dropped it in an
// OR query — same value, context-dependent result.
func TestTypeAliasNormalizeParity(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	featureIssue := &types.Issue{ID: "bd-1", IssueType: types.IssueType("feature")}

	// alias -> canonical, using the documented Normalize() aliases.
	aliases := []string{"feat", "enhancement"}

	for _, alias := range aliases {
		// Filter mode (bare): applyTypeFilter must normalize to the canonical type.
		t.Run("filter/"+alias, func(t *testing.T) {
			res, err := EvaluateAt("type="+alias, now)
			if err != nil {
				t.Fatalf("EvaluateAt(type=%s) error: %v", alias, err)
			}
			if res.Filter.IssueType == nil {
				t.Fatalf("type=%s: Filter.IssueType is nil", alias)
			}
			if *res.Filter.IssueType != types.IssueType("feature") {
				t.Fatalf("type=%s: filter IssueType = %q, want feature", alias, *res.Filter.IssueType)
			}
		})

		// Predicate mode (OR): buildTypePredicate must ALSO normalize, so the
		// feature issue matches the alias.
		t.Run("predicate/"+alias, func(t *testing.T) {
			res, err := EvaluateAt("type="+alias+" OR id=zzz", now)
			if err != nil {
				t.Fatalf("EvaluateAt(type=%s OR id=zzz) error: %v", alias, err)
			}
			if res.Predicate == nil {
				t.Fatalf("type=%s OR: Predicate is nil (expected predicate mode)", alias)
			}
			if !res.Predicate(featureIssue) {
				t.Fatalf("type=%s OR: predicate did NOT match a feature issue (alias not normalized)", alias)
			}
		})

		// NOT-node filter mode (`NOT type=feat` -> applyNot, evaluator.go:652):
		// must exclude via the canonical type, i.e. a feature issue is in
		// ExcludeTypes as "feature", not the raw alias "feat".
		t.Run("notnode/"+alias, func(t *testing.T) {
			res, err := EvaluateAt("NOT type="+alias, now)
			if err != nil {
				t.Fatalf("EvaluateAt(NOT type=%s) error: %v", alias, err)
			}
			found := false
			for _, ex := range res.Filter.ExcludeTypes {
				if ex == types.IssueType("feature") {
					found = true
				}
				if ex == types.IssueType(alias) {
					t.Fatalf("NOT type=%s: ExcludeTypes carries raw alias %q (not normalized)", alias, alias)
				}
			}
			if !found {
				t.Fatalf("NOT type=%s: ExcludeTypes missing canonical feature; got %v", alias, res.Filter.ExcludeTypes)
			}
		})
	}
}

// TestTypeValidationParity pins beads-123i tooth (2): an unknown/typo'd type must
// ERROR in BOTH filter and predicate mode (matching the beads-shux fix for the
// filter path), never a silent false-empty rc=0 in the OR/predicate context.
func TestTypeValidationParity(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	const bogus = "bogustype"

	t.Run("filter", func(t *testing.T) {
		_, err := EvaluateAt("type="+bogus, now)
		if err == nil {
			t.Fatalf("EvaluateAt(type=%s): want invalid-type error, got nil", bogus)
		}
		if !strings.Contains(err.Error(), "invalid type") {
			t.Fatalf("EvaluateAt(type=%s): err = %q, want 'invalid type'", bogus, err)
		}
	})

	t.Run("predicate", func(t *testing.T) {
		_, err := EvaluateAt("type="+bogus+" OR id=zzz", now)
		if err == nil {
			t.Fatalf("EvaluateAt(type=%s OR id=zzz): want invalid-type error, got nil (silent false-empty)", bogus)
		}
		if !strings.Contains(err.Error(), "invalid type") {
			t.Fatalf("EvaluateAt(type=%s OR id=zzz): err = %q, want 'invalid type'", bogus, err)
		}
	})

	t.Run("notnode", func(t *testing.T) {
		_, err := EvaluateAt("NOT type="+bogus, now)
		if err == nil {
			t.Fatalf("EvaluateAt(NOT type=%s): want invalid-type error, got nil", bogus)
		}
		if !strings.Contains(err.Error(), "invalid type") {
			t.Fatalf("EvaluateAt(NOT type=%s): err = %q, want 'invalid type'", bogus, err)
		}
	})
}
