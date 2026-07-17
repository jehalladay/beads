package main

import (
	"strings"
	"testing"
)

// TestValidateCompactMode is the teeth for beads-9fww. The command body routes
// this pure validator through FatalErrorRespectJSON, so under --json a bad
// invocation now surfaces as structured JSON on stdout instead of the historical
// empty-stdout + os.Exit(1). Testing the pure function pins the mode/tier logic
// (exactly-one-mode, tier-1-only) that the contract depends on without invoking
// the destructive command body or os.Exit.
func TestValidateCompactMode(t *testing.T) {
	tests := []struct {
		name             string
		analyze          bool
		apply            bool
		auto             bool
		tier             int
		wantErr          bool
		wantErrSubstring string
	}{
		{name: "analyze only tier1", analyze: true, tier: 1, wantErr: false},
		{name: "apply only tier1", apply: true, tier: 1, wantErr: false},
		{name: "auto only tier1", auto: true, tier: 1, wantErr: false},
		{
			name: "no mode", tier: 1, wantErr: true,
			wantErrSubstring: "must specify one mode",
		},
		{
			name: "analyze+apply", analyze: true, apply: true, tier: 1, wantErr: true,
			wantErrSubstring: "cannot use multiple modes",
		},
		{
			name: "all three modes", analyze: true, apply: true, auto: true, tier: 1, wantErr: true,
			wantErrSubstring: "cannot use multiple modes",
		},
		{
			name: "analyze tier2 unimplemented", analyze: true, tier: 2, wantErr: true,
			wantErrSubstring: "not yet implemented",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCompactMode(tt.analyze, tt.apply, tt.auto, tt.tier)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateCompactMode(%v,%v,%v,tier=%d) = nil; want error",
						tt.analyze, tt.apply, tt.auto, tt.tier)
				}
				if tt.wantErrSubstring != "" && !strings.Contains(err.Error(), tt.wantErrSubstring) {
					t.Errorf("error = %q; want it to contain %q", err.Error(), tt.wantErrSubstring)
				}
			} else if err != nil {
				t.Errorf("validateCompactMode(%v,%v,%v,tier=%d) = %v; want nil",
					tt.analyze, tt.apply, tt.auto, tt.tier, err)
			}
		})
	}
}
