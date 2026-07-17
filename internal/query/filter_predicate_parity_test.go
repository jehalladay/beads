package query

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestStatusValidationParity pins beads-bi4g: an invalid status must ERROR in
// BOTH filter and predicate mode. applyStatusFilter validates via Status.IsValid
// (filter/AND mode errors), but buildStatusPredicate did ToLower only and built
// `i.Status == status`, so `status=bogus OR ...` silently matched nothing (rc=0)
// — the validation half of the 123i/shux class, applied to status.
func TestStatusValidationParity(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	const bogus = "bogusstatus"

	t.Run("filter", func(t *testing.T) {
		if _, err := EvaluateAt("status="+bogus, now); err == nil || !strings.Contains(err.Error(), "invalid status") {
			t.Fatalf("EvaluateAt(status=%s) err = %v, want 'invalid status'", bogus, err)
		}
	})
	t.Run("predicate", func(t *testing.T) {
		_, err := EvaluateAt("status="+bogus+" OR id=zzz", now)
		if err == nil || !strings.Contains(err.Error(), "invalid status") {
			t.Fatalf("EvaluateAt(status=%s OR id=zzz) err = %v, want 'invalid status' (not silent false-empty)", bogus, err)
		}
	})

	// A valid status must still work in predicate mode (no over-rejection).
	t.Run("valid-predicate-ok", func(t *testing.T) {
		res, err := EvaluateAt("status=open OR id=zzz", now)
		if err != nil {
			t.Fatalf("EvaluateAt(status=open OR id=zzz) unexpected err: %v", err)
		}
		if res.Predicate == nil {
			t.Fatal("expected a predicate for status=open OR ...")
		}
		if !res.Predicate(&types.Issue{Status: types.StatusOpen}) {
			t.Error("predicate did not match an open issue")
		}
		if res.Predicate(&types.Issue{Status: types.StatusClosed}) {
			t.Error("predicate matched a closed issue for status=open")
		}
	})
}

// TestSpecMatchParity pins beads-dcww: `spec=X` must mean the SAME thing in
// filter and predicate mode. applySpecFilter sets SpecIDPrefix=X (a `spec_id
// LIKE 'X%'` prefix match) while buildSpecPredicate's no-wildcard case did an
// EXACT `SpecID==X` — so `spec=abc` prefix-matched in AND mode but exact-matched
// in an OR query (silent context-dependent result). The `*` wildcard is not
// lexable in the query language (id=abc* fails to lex too — the trailing-*
// branches in build{Spec,ID}Predicate/applyIDFilter are dead for the query-
// string path), and IssueFilter/SQL express spec only as a prefix (SpecIDPrefix,
// no exact field), so the filter's prefix semantics are authoritative: the
// predicate must be aligned to prefix, not the reverse.
func TestSpecMatchParity(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	// The filter is a prefix match (pre-existing, authoritative — SpecIDPrefix).
	t.Run("filter/prefix", func(t *testing.T) {
		res, err := EvaluateAt("spec=abc", now)
		if err != nil {
			t.Fatalf("EvaluateAt(spec=abc): %v", err)
		}
		if res.Filter.SpecIDPrefix != "abc" {
			t.Errorf("spec=abc SpecIDPrefix = %q, want abc (prefix)", res.Filter.SpecIDPrefix)
		}
	})

	// The predicate MUST match the filter: `spec=abc` prefix-matches abcdef.
	t.Run("predicate/prefix-matches-filter", func(t *testing.T) {
		res, err := EvaluateAt("spec=abc OR id=zzz", now)
		if err != nil {
			t.Fatalf("EvaluateAt(spec=abc OR id=zzz): %v", err)
		}
		if res.Predicate == nil {
			t.Fatal("expected predicate")
		}
		if !res.Predicate(&types.Issue{SpecID: "abc"}) {
			t.Error("spec=abc did not match exact SpecID abc")
		}
		if !res.Predicate(&types.Issue{SpecID: "abcdef"}) {
			t.Error("spec=abc did NOT prefix-match abcdef — predicate diverges from the prefix filter")
		}
		if res.Predicate(&types.Issue{SpecID: "xyz"}) {
			t.Error("spec=abc matched non-prefix xyz")
		}
	})
}

// TestMolTypePredicateParity pins beads-z6iy (mol_type half): mol_type is a
// plain column compare, so it must work in predicate mode too (option A). Before
// the fix buildComparisonPredicate had no case → `mol_type=work OR ...` errored
// 'unknown field: mol_type' though the bare form succeeded.
func TestMolTypePredicateParity(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	res, err := EvaluateAt("mol_type=swarm OR id=zzz", now)
	if err != nil {
		t.Fatalf("EvaluateAt(mol_type=swarm OR id=zzz): %v (want a working predicate)", err)
	}
	if res.Predicate == nil {
		t.Fatal("expected a predicate for mol_type=swarm OR ...")
	}
	if !res.Predicate(&types.Issue{MolType: types.MolTypeSwarm}) {
		t.Error("mol_type=swarm predicate did not match a swarm issue")
	}
	if res.Predicate(&types.Issue{MolType: types.MolTypePatrol}) {
		t.Error("mol_type=swarm predicate matched a patrol issue")
	}

	// Invalid mol_type must error in predicate mode too (parity with the filter).
	if _, err := EvaluateAt("mol_type=bogus OR id=zzz", now); err == nil || !strings.Contains(err.Error(), "invalid mol_type") {
		t.Fatalf("EvaluateAt(mol_type=bogus OR id=zzz) err = %v, want 'invalid mol_type'", err)
	}
}

// TestParentPredicateRejectsOR pins beads-z6iy (parent half): parent= filter
// semantics are transitive-descendants resolved against the store (a
// dependency-table lookup), which a pure in-memory predicate cannot replicate
// faithfully. So rather than a predicate that silently mismatches the filter
// (the exact silent-divergence class this sweep targets), `parent=X` in an OR
// context must fail with a SPECIFIC, actionable error — not the generic
// 'unknown field' (option B).
func TestParentPredicateRejectsOR(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	_, err := EvaluateAt("parent=bd-1 OR id=zzz", now)
	if err == nil {
		t.Fatal("EvaluateAt(parent=bd-1 OR id=zzz): want a rejection, got nil")
	}
	if strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("parent= in OR errored with the generic 'unknown field' (%v); want a specific 'not supported in OR' message", err)
	}
	if !strings.Contains(err.Error(), "OR") {
		t.Fatalf("parent= in OR error = %v, want a message mentioning OR-context limitation", err)
	}

	// The bare filter form must still work.
	if _, err := EvaluateAt("parent=bd-1", now); err != nil {
		t.Fatalf("EvaluateAt(parent=bd-1) bare filter should still work: %v", err)
	}
}
