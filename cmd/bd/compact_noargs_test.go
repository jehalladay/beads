package main

import (
	"strings"
	"testing"
)

// TestCompactRejectsPositionalArgs guards beads-jg5e: `bd compact bd-42 --force`
// (natural muscle memory, since compact targets a single issue via --id) used to
// silently discard the "bd-42" positional and compact per the flags — i.e. the
// whole database — with rc=0. compact is destructive ("permanent graceful decay
// - original content is discarded"), so a stray positional must be a loud error
// that points the user at --id, not a silent whole-DB compaction.
func TestCompactRejectsPositionalArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "no args ok", args: []string{}, wantErr: false},
		{name: "single issue-id positional rejected", args: []string{"bd-42"}, wantErr: true},
		{name: "multiple positionals rejected", args: []string{"bd-42", "bd-43"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := compactNoArgs(nil, tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for args %v, got nil", tt.args)
				}
				if !strings.Contains(err.Error(), "--id") {
					t.Errorf("error %q should hint at --id (compact's single-issue targeting flag)", err.Error())
				}
				if !strings.Contains(err.Error(), tt.args[0]) {
					t.Errorf("error %q should echo the stray arg %q", err.Error(), tt.args[0])
				}
			} else if err != nil {
				t.Errorf("unexpected error for args %v: %v", tt.args, err)
			}
		})
	}
}

// TestCompactWiresArgsValidator ensures the guard stays wired so a refactor
// can't silently reintroduce the whole-DB-compaction footgun (beads-jg5e).
func TestCompactWiresArgsValidator(t *testing.T) {
	if compactCmd.Args == nil {
		t.Fatal("compactCmd.Args is nil; guard (beads-jg5e) not wired")
	}
	if err := compactCmd.Args(compactCmd, []string{"bd-42"}); err == nil {
		t.Fatal("compact accepted a positional arg; guard (beads-jg5e) not wired")
	}
}

// TestCompactDoltRejectsPositionalArgs guards beads-jg5e for the top-level
// `bd compact` (Dolt history squash) — the MORE destructive of the two compact
// commands. `bd compact somebead --force` used to silently discard "somebead"
// and irreversibly squash ALL old Dolt history with rc=0.
func TestCompactDoltRejectsPositionalArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "no args ok", args: []string{}, wantErr: false},
		{name: "single positional rejected", args: []string{"somebead"}, wantErr: true},
		{name: "multiple positionals rejected", args: []string{"a", "b"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := compactDoltNoArgs(nil, tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for args %v, got nil", tt.args)
				}
				if !strings.Contains(err.Error(), tt.args[0]) {
					t.Errorf("error %q should echo the stray arg %q", err.Error(), tt.args[0])
				}
			} else if err != nil {
				t.Errorf("unexpected error for args %v: %v", tt.args, err)
			}
		})
	}
}

// TestCompactDoltWiresArgsValidator ensures the top-level compact guard stays
// wired (beads-jg5e).
func TestCompactDoltWiresArgsValidator(t *testing.T) {
	if compactDoltCmd.Args == nil {
		t.Fatal("compactDoltCmd.Args is nil; guard (beads-jg5e) not wired")
	}
	if err := compactDoltCmd.Args(compactDoltCmd, []string{"somebead"}); err == nil {
		t.Fatal("bd compact accepted a positional arg; guard (beads-jg5e) not wired")
	}
}
