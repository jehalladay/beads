package domain

import "testing"

// beads-36d6n: the domain rewriteTextReferences twin of cmd/bd/delete.go used a
// single-pass re.ReplaceAllString, so a run of adjacent references sharing one
// delimiter ("bd-abc bd-abc") had only the FIRST rewritten — the second stayed a
// dangling live reference to a deleted issue. deletedReferenceRewriter loops to a
// fixed point. This mirrors the cmd/bd twin test to keep the two paths aligned.
func TestDeletedReferenceRewriter_Domain_AdjacentRun_36d6n(t *testing.T) {
	rewrite := deletedReferenceRewriter("bd-abc")

	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"bd-abc bd-abc", "[deleted:bd-abc] [deleted:bd-abc]", true},
		{"bd-abc,bd-abc", "[deleted:bd-abc],[deleted:bd-abc]", true},
		{"bd-abc bd-abc bd-abc", "[deleted:bd-abc] [deleted:bd-abc] [deleted:bd-abc]", true},
		{"see bd-abc now", "see [deleted:bd-abc] now", true},
		// hyphen-extended sibling untouched (1nvr5 boundary invariant)
		{"bd-abc-2 and bd-abc", "bd-abc-2 and [deleted:bd-abc]", true},
		{"no ref here", "no ref here", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := rewrite(tc.in)
		if ok != tc.ok {
			t.Errorf("rewrite(%q) changed=%v, want %v (got %q)", tc.in, ok, tc.ok, got)
			continue
		}
		if got != tc.want {
			t.Errorf("rewrite(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
