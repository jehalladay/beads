package main

import "testing"

// TestDoltCommitMessageProvided is the teeth for beads-by9ph: `bd dolt commit
// -m/--message` with a whitespace-only value must fall through to the
// auto-generated "bd: dolt commit (auto-commit) by <actor>" default, NOT
// overwrite it with blank whitespace. The pre-fix `msg == ""` check let a
// whitespace-only message through (it is != ""), so the Dolt commit message
// became "   " and the actor-provenance default was discarded.
//
// Mirrors the sibling auto-commit path (dolt_autocommit.go: strings.TrimSpace)
// and the override-a-default whitespace class (mol squash --summary beads-au0rt
// squashSummaryProvided, todo done --reason beads-07sko). Pure unit test —
// no cgo / embedded dolt.
func TestDoltCommitMessageProvided(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"empty", "", false},
		{"spaces only", "   ", false},
		{"tab only", "\t", false},
		{"newline only", "\n", false},
		{"mixed whitespace", " \t\n ", false},
		{"genuine message", "fix: land the thing", true},
		{"message with leading space", "  keep me", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := doltCommitMessageProvided(tc.msg); got != tc.want {
				t.Errorf("doltCommitMessageProvided(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}
