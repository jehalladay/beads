package main

import (
	"testing"
)

// TestResolveTitle_Trim covers the create-path title normalization (beads-n5xz):
// resolveTitle must trim leading/trailing whitespace and reject an
// empty-after-trim title, mirroring the update path (cmd/bd/update.go ~:101).
// Before the fix, a padded title was stored verbatim and a whitespace-only
// title was accepted (types.Validate only checks len==0), while `bd update
// --title` trimmed+rejected the same values — a create/update asymmetry and a
// sibling of the label-trim gap (beads-4g2h).
func TestResolveTitle_Trim(t *testing.T) {
	// resolveTitle reports errors via HandleError (message to stderr, returns an
	// opaque exit-code error), so error cases assert only that an error occurred,
	// not its text.
	tests := []struct {
		name      string
		args      []string
		titleFlag string
		md        string
		graph     string
		want      string
		wantErr   bool
	}{
		{name: "positional trimmed", args: []string{"  padded title  "}, want: "padded title"},
		{name: "flag trimmed", titleFlag: "  padded flag  ", want: "padded flag"},
		{name: "positional whitespace-only rejected", args: []string{"   "}, wantErr: true},
		{name: "flag whitespace-only rejected", titleFlag: "\t \n", wantErr: true},
		{name: "positional tabs+newlines trimmed", args: []string{"\t keep me \n"}, want: "keep me"},
		{name: "clean positional unchanged", args: []string{"already clean"}, want: "already clean"},
		{name: "internal whitespace preserved", args: []string{"  a  b  "}, want: "a  b"},
		// Existing behavior that must NOT regress:
		{name: "no title errors", wantErr: true},
		{name: "flag-like positional errors", args: []string{"--foo"}, wantErr: true},
		{name: "markdown route returns empty (title parsed from file)", md: "x.md", want: ""},
		{name: "graph route returns empty", graph: "x.json", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTitle(tt.args, tt.titleFlag, tt.md, tt.graph)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveTitle(%q, %q) = %q, want error", tt.args, tt.titleFlag, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveTitle(%q, %q) unexpected error: %v", tt.args, tt.titleFlag, err)
			}
			if got != tt.want {
				t.Fatalf("resolveTitle(%q, %q) = %q, want %q", tt.args, tt.titleFlag, got, tt.want)
			}
		})
	}
}
