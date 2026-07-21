package main

import "testing"

// beads-36d6n: bd delete's text-reference cleanup used a single-pass
// re.ReplaceAllString with a charclass-boundary regex. That boundary consumes
// the delimiter a run of adjacent references shares ("bd-abc bd-abc" shares one
// space), so single-pass rewrote only the FIRST of the run and left the second
// as a dangling live reference to a now-deleted issue. deletedReferenceRewriter
// loops to a fixed point (the delete-side analogue of the rename fix, 1nvr5).
func TestDeletedReferenceRewriter_AdjacentRun_36d6n(t *testing.T) {
	rewrite := deletedReferenceRewriter("bd-abc")

	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			// The core bug: two references sharing one space delimiter. Single-pass
			// left the second untouched.
			name: "two space-separated refs",
			in:   "bd-abc bd-abc",
			want: "[deleted:bd-abc] [deleted:bd-abc]",
			ok:   true,
		},
		{
			name: "two comma-separated refs",
			in:   "bd-abc,bd-abc",
			want: "[deleted:bd-abc],[deleted:bd-abc]",
			ok:   true,
		},
		{
			name: "three adjacent refs",
			in:   "bd-abc bd-abc bd-abc",
			want: "[deleted:bd-abc] [deleted:bd-abc] [deleted:bd-abc]",
			ok:   true,
		},
		{
			name: "refs embedded in prose",
			in:   "see bd-abc bd-abc now",
			want: "see [deleted:bd-abc] [deleted:bd-abc] now",
			ok:   true,
		},
		{
			name: "single ref",
			in:   "closes bd-abc.",
			want: "closes [deleted:bd-abc].",
			ok:   true,
		},
		{
			// A hyphen-extended sibling id must NOT be rewritten (the 1nvr5
			// charclass-boundary invariant we inherit).
			name: "hyphen-extended sibling untouched",
			in:   "bd-abc-2 references bd-abc",
			want: "bd-abc-2 references [deleted:bd-abc]",
			ok:   true,
		},
		{
			name: "no reference",
			in:   "nothing to see here",
			want: "nothing to see here",
			ok:   false,
		},
		{
			name: "empty",
			in:   "",
			want: "",
			ok:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rewrite(tc.in)
			if ok != tc.ok {
				t.Fatalf("changed=%v, want %v (in=%q got=%q)", ok, tc.ok, tc.in, got)
			}
			if got != tc.want {
				t.Errorf("rewrite(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The tombstone contains the id bounded by non-id chars, so a naive re-scan loop
// would re-match forever. Guard against a regression that reintroduces an
// infinite loop (or that leaves the sentinel un-swapped).
func TestDeletedReferenceRewriter_Terminates_36d6n(t *testing.T) {
	rewrite := deletedReferenceRewriter("bd-x")
	got, ok := rewrite("bd-x bd-x bd-x bd-x")
	if !ok {
		t.Fatal("expected a change")
	}
	want := "[deleted:bd-x] [deleted:bd-x] [deleted:bd-x] [deleted:bd-x]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// The internal NUL sentinel must never leak into the output.
	for _, r := range got {
		if r == 0 {
			t.Fatalf("NUL sentinel leaked into output: %q", got)
		}
	}
}
