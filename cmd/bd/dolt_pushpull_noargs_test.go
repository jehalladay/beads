package main

import (
	"strings"
	"testing"
)

// TestDoltPushPullRejectPositionalRemote guards beads-cdx9: `bd dolt push origin`
// (git muscle memory) used to silently ignore the positional and push to the
// DEFAULT remote with rc=0. Named remotes are selected via --remote only, so a
// positional must now be a clear error.
func TestDoltPushPullRejectPositionalRemote(t *testing.T) {
	tests := []struct {
		name    string
		verb    string
		args    []string
		wantErr bool
	}{
		{name: "push no args ok", verb: "push", args: []string{}, wantErr: false},
		{name: "pull no args ok", verb: "pull", args: []string{}, wantErr: false},
		{name: "push with remote positional rejected", verb: "push", args: []string{"origin"}, wantErr: true},
		{name: "pull with remote positional rejected", verb: "pull", args: []string{"origin"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := doltRemoteNoPositional(tt.verb)(nil, tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %s args %v, got nil", tt.verb, tt.args)
				}
				if !strings.Contains(err.Error(), "--remote") {
					t.Errorf("error %q should hint at --remote", err.Error())
				}
				if !strings.Contains(err.Error(), "origin") {
					t.Errorf("error %q should echo the stray arg", err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected error for %s args %v: %v", tt.verb, tt.args, err)
			}
		})
	}
}

// TestDoltPushPullWireArgsValidator ensures the guard stays wired on both
// commands so a refactor can't silently reintroduce the wrong-remote bug.
func TestDoltPushPullWireArgsValidator(t *testing.T) {
	for _, c := range []struct {
		name string
		args func([]string) error
	}{
		{"push", func(a []string) error {
			if doltPushCmd.Args == nil {
				t.Fatal("doltPushCmd.Args is nil; guard (beads-cdx9) not wired")
			}
			return doltPushCmd.Args(doltPushCmd, a)
		}},
		{"pull", func(a []string) error {
			if doltPullCmd.Args == nil {
				t.Fatal("doltPullCmd.Args is nil; guard (beads-cdx9) not wired")
			}
			return doltPullCmd.Args(doltPullCmd, a)
		}},
	} {
		if err := c.args([]string{"origin"}); err == nil {
			t.Fatalf("dolt %s accepted a positional remote; guard (beads-cdx9) not wired", c.name)
		}
	}
}
