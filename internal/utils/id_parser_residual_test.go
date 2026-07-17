package utils

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestParseIssueIDEmptyPrefixDefaults(t *testing.T) {
	// Empty prefix → defaults to "bd-" (id_parser.go:18-20).
	if got := parseIssueID("a3f8e9", ""); got != "bd-a3f8e9" {
		t.Fatalf("parseIssueID(\"a3f8e9\", \"\") = %q, want bd-a3f8e9", got)
	}
	// Empty prefix, input already bd-prefixed → returned as-is.
	if got := parseIssueID("bd-a3f8e9", ""); got != "bd-a3f8e9" {
		t.Fatalf("parseIssueID(\"bd-a3f8e9\", \"\") = %q, want bd-a3f8e9", got)
	}
}

func TestResolvePartialIDHasKnownPrefixBranch(t *testing.T) {
	// allowed_prefixes makes "myproj" a known prefix; a known-prefixed input
	// that matches no issue still exercises the hasKnownPrefix normalization
	// branch (id_parser.go:90-92) before failing to resolve.
	store := newResolveStore()
	store.config["allowed_prefixes"] = "myproj"

	_, err := ResolvePartialID(context.Background(), store, "myproj-zz")
	if err == nil {
		t.Fatal("ResolvePartialID(known-prefix, no match) = nil error, want not-found error")
	}
}

func TestResolvePartialIDSubstringNoPrefixExtract(t *testing.T) {
	// An issue whose ID has no known prefix ("xyz123abc") drives the
	// substring loop's prefix-extract else branch (issueHash = issue.ID,
	// id_parser.go:141-143) and resolves via substring containment.
	store := newResolveStore(&types.Issue{ID: "xyz123abc"})

	got, err := ResolvePartialID(context.Background(), store, "abc")
	if err != nil {
		t.Fatalf("ResolvePartialID(\"abc\") error: %v", err)
	}
	if got != "xyz123abc" {
		t.Fatalf("ResolvePartialID(\"abc\") = %q, want xyz123abc", got)
	}
}

func TestResolvePartialIDWispFallbackExactID(t *testing.T) {
	// A full wisp ID is skipped by the persistent-only fast path and the
	// persistent substring search, then resolved by the wisp-fallback exact
	// match (w.ID == input, id_parser.go:171).
	wisp := &types.Issue{ID: "bd-wspxyz", Ephemeral: true}
	store := newResolveStore(wisp)

	got, err := ResolvePartialID(context.Background(), store, "bd-wspxyz")
	if err != nil {
		t.Fatalf("ResolvePartialID(full wisp ID) error: %v", err)
	}
	if got != "bd-wspxyz" {
		t.Fatalf("ResolvePartialID(full wisp ID) = %q, want bd-wspxyz", got)
	}
}

func TestResolvePartialIDWispFallbackNoPrefixExtract(t *testing.T) {
	// A wisp with no known prefix ("plainwisp") drives the wisp-fallback
	// prefix-extract else branch (wHash = w.ID, id_parser.go:177-179) and
	// resolves by substring containment.
	wisp := &types.Issue{ID: "plainwisp", Ephemeral: true}
	store := newResolveStore(wisp)

	got, err := ResolvePartialID(context.Background(), store, "plain")
	if err != nil {
		t.Fatalf("ResolvePartialID(\"plain\") error: %v", err)
	}
	if got != "plainwisp" {
		t.Fatalf("ResolvePartialID(\"plain\") = %q, want plainwisp", got)
	}
}

func TestNaturalCompareIDsNumericSegmentsDiffer(t *testing.T) {
	// Numeric segments that differ take the `return na - nb` branch
	// (issue_id.go:154).
	if got := NaturalCompareIDs("bd-1", "bd-2"); got >= 0 {
		t.Fatalf("NaturalCompareIDs(bd-1, bd-2) = %d, want < 0", got)
	}
	if got := NaturalCompareIDs("bd-10", "bd-3"); got <= 0 {
		t.Fatalf("NaturalCompareIDs(bd-10, bd-3) = %d, want > 0 (natural, not lexical)", got)
	}
	// Hierarchical numeric segments.
	if got := NaturalCompareIDs("bd-a3f.2", "bd-a3f.11"); got >= 0 {
		t.Fatalf("NaturalCompareIDs(a3f.2, a3f.11) = %d, want < 0", got)
	}
	// Segments that differ as strings but are numerically equal ("01" vs "1")
	// take the na==nb `continue` branch (issue_id.go:154), then the next
	// segment "2" < "3" decides.
	if got := NaturalCompareIDs("bd-01.2", "bd-1.3"); got >= 0 {
		t.Fatalf("NaturalCompareIDs(01.2, 1.3) = %d, want < 0 (equal-numeric continue)", got)
	}
}
