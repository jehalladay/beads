package issueops

import (
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

func TestValidateIssueIDPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		id              string
		prefix          string
		allowedPrefixes string
		wantErr         bool
	}{
		{name: "matches primary prefix", id: "bd-abc", prefix: "bd", wantErr: false},
		{name: "mismatch with no allowed list", id: "hq-abc", prefix: "bd", wantErr: true},
		{name: "matches an allowed prefix", id: "hq-abc", prefix: "bd", allowedPrefixes: "hq,rc", wantErr: false},
		{name: "allowed prefixes are trimmed", id: "rc-1", prefix: "bd", allowedPrefixes: " hq , rc ", wantErr: false},
		{name: "empty entries in allowed list are ignored", id: "bd-x", prefix: "zz", allowedPrefixes: ",,", wantErr: true},
		{name: "prefix requires the hyphen separator", id: "bdabc", prefix: "bd", wantErr: true},
		{name: "no match against allowed list", id: "zz-1", prefix: "bd", allowedPrefixes: "hq,rc", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateIssueIDPrefix(tt.id, tt.prefix, tt.allowedPrefixes)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateIssueIDPrefix(%q,%q,%q) = nil, want error", tt.id, tt.prefix, tt.allowedPrefixes)
				}
				if !errors.Is(err, storage.ErrPrefixMismatch) {
					t.Fatalf("error = %v, want wraps ErrPrefixMismatch", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateIssueIDPrefix(%q,%q,%q) = %v, want nil", tt.id, tt.prefix, tt.allowedPrefixes, err)
			}
		})
	}
}

func TestParseHierarchicalID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		id         string
		wantParent string
		wantChild  int
		wantOK     bool
	}{
		{name: "simple hierarchical", id: "bd-abc.1", wantParent: "bd-abc", wantChild: 1, wantOK: true},
		{name: "multi-digit child", id: "bd-abc.42", wantParent: "bd-abc", wantChild: 42, wantOK: true},
		{name: "nested parent keeps everything before last dot", id: "bd-abc.1.2", wantParent: "bd-abc.1", wantChild: 2, wantOK: true},
		{name: "no dot is not hierarchical", id: "bd-abc", wantOK: false},
		{name: "non-numeric child rejected", id: "bd-abc.x", wantOK: false},
		{name: "empty child rejected", id: "bd-abc.", wantOK: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parent, child, ok := ParseHierarchicalID(tt.id)
			if ok != tt.wantOK {
				t.Fatalf("ParseHierarchicalID(%q) ok = %v, want %v", tt.id, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if parent != tt.wantParent || child != tt.wantChild {
				t.Fatalf("ParseHierarchicalID(%q) = (%q,%d), want (%q,%d)",
					tt.id, parent, child, tt.wantParent, tt.wantChild)
			}
		})
	}
}

func TestAllWisps(t *testing.T) {
	t.Parallel()

	ephemeral := &types.Issue{ID: "bd-1", Ephemeral: true}
	noHistory := &types.Issue{ID: "bd-2", NoHistory: true}
	regular := &types.Issue{ID: "bd-3"}

	tests := []struct {
		name   string
		issues []*types.Issue
		want   bool
	}{
		{name: "empty slice is vacuously all-wisps", issues: nil, want: true},
		{name: "all ephemeral", issues: []*types.Issue{ephemeral, ephemeral}, want: true},
		{name: "all no-history", issues: []*types.Issue{noHistory}, want: true},
		{name: "mixed ephemeral and no-history", issues: []*types.Issue{ephemeral, noHistory}, want: true},
		{name: "one regular issue breaks it", issues: []*types.Issue{ephemeral, regular}, want: false},
		{name: "all regular", issues: []*types.Issue{regular, regular}, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := AllWisps(tt.issues); got != tt.want {
				t.Fatalf("AllWisps(%v) = %v, want %v", tt.issues, got, tt.want)
			}
		})
	}
}

func TestValidatePeerName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		peer    string
		wantErr bool
	}{
		{name: "valid simple", peer: "alpha", wantErr: false},
		{name: "valid with hyphen and underscore", peer: "a-b_c1", wantErr: false},
		{name: "empty", peer: "", wantErr: true},
		{name: "at max length (64) is valid", peer: "a" + strings.Repeat("b", 63), wantErr: false},
		{name: "too long (>64)", peer: "a" + strings.Repeat("b", 64), wantErr: true},
		{name: "starts with digit", peer: "1abc", wantErr: true},
		{name: "starts with hyphen", peer: "-abc", wantErr: true},
		{name: "invalid character", peer: "a.b", wantErr: true},
		{name: "space", peer: "a b", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePeerName(tt.peer)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidatePeerName(%q) = nil, want error", tt.peer)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidatePeerName(%q) = %v, want nil", tt.peer, err)
			}
		})
	}
}
