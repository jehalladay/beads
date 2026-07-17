package validation

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestNotHooked exercises the NotHooked validator across the nil-issue,
// hooked-without-force (rejected), hooked-with-force (allowed), and
// non-hooked passthrough cases.
func TestNotHooked(t *testing.T) {
	tests := []struct {
		name    string
		issue   *types.Issue
		force   bool
		wantErr bool
	}{
		{
			name:    "nil issue passes",
			issue:   nil,
			force:   false,
			wantErr: false,
		},
		{
			name:    "hooked without force fails",
			issue:   &types.Issue{ID: "bd-test", Status: types.StatusHooked},
			force:   false,
			wantErr: true,
		},
		{
			name:    "hooked with force passes",
			issue:   &types.Issue{ID: "bd-test", Status: types.StatusHooked},
			force:   true,
			wantErr: false,
		},
		{
			name:    "open issue passes regardless of force",
			issue:   &types.Issue{ID: "bd-test", Status: types.StatusOpen},
			force:   false,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NotHooked(tt.force)("bd-test", tt.issue)
			if (err != nil) != tt.wantErr {
				t.Errorf("NotHooked() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestForDelete exercises the forDelete validator chain (Exists + NotTemplate).
func TestForDelete(t *testing.T) {
	tests := []struct {
		name    string
		issue   *types.Issue
		wantErr bool
	}{
		{
			name:    "nil issue fails",
			issue:   nil,
			wantErr: true,
		},
		{
			name:    "template fails",
			issue:   &types.Issue{ID: "bd-test", IsTemplate: true},
			wantErr: true,
		},
		{
			name:    "regular issue passes",
			issue:   &types.Issue{ID: "bd-test", Status: types.StatusOpen},
			wantErr: false,
		},
		{
			name:    "closed non-template issue passes",
			issue:   &types.Issue{ID: "bd-test", Status: types.StatusClosed},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := forDelete()("bd-test", tt.issue)
			if (err != nil) != tt.wantErr {
				t.Errorf("forDelete() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestForReopen exercises the forReopen validator chain
// (Exists + NotTemplate + HasStatus(closed)).
func TestForReopen(t *testing.T) {
	tests := []struct {
		name    string
		issue   *types.Issue
		wantErr bool
	}{
		{
			name:    "nil issue fails",
			issue:   nil,
			wantErr: true,
		},
		{
			name:    "template fails",
			issue:   &types.Issue{ID: "bd-test", IsTemplate: true, Status: types.StatusClosed},
			wantErr: true,
		},
		{
			name:    "open issue fails (not closed)",
			issue:   &types.Issue{ID: "bd-test", Status: types.StatusOpen},
			wantErr: true,
		},
		{
			name:    "closed issue passes",
			issue:   &types.Issue{ID: "bd-test", Status: types.StatusClosed},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := forReopen()("bd-test", tt.issue)
			if (err != nil) != tt.wantErr {
				t.Errorf("forReopen() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
