package main

import (
	"strings"
	"testing"
)

// TestValidateGateCreateTimerPositive is the teeth for beads-cx0eu: a timer
// --timeout must be POSITIVE, not merely non-empty and well-formed.
// time.ParseDuration accepts "0s" and negative durations, and the previous guard
// only checked the string was non-empty — so two degenerate timers slipped
// through create:
//   - --timeout=0s persists Timeout==0, which checkTimer treats exactly like a
//     missing timeout ("no timeout set" error on every check) → the blocked
//     issue is stranded out of bd ready forever (the exact ds9tr strand).
//   - a negative --timeout puts expiresAt in the past, so the "timer" resolves on
//     the first check without ever waiting (silently degenerate).
//
// Pure unit test on validateGateCreate (no cgo/embedded dolt needed).
func TestValidateGateCreateTimerPositive(t *testing.T) {
	t.Run("zero_timeout_rejected", func(t *testing.T) {
		err := validateGateCreate("timer", "", "0s")
		if err == nil {
			t.Fatal("timer --timeout=0s must be rejected (Timeout==0 => 'no timeout set' forever)")
		}
		if !strings.Contains(err.Error(), "positive --timeout") {
			t.Errorf("expected positive-timeout rejection, got: %v", err)
		}
	})

	t.Run("negative_timeout_rejected", func(t *testing.T) {
		err := validateGateCreate("timer", "", "-5m")
		if err == nil {
			t.Fatal("timer --timeout=-5m must be rejected (expires before it starts)")
		}
		if !strings.Contains(err.Error(), "positive --timeout") {
			t.Errorf("expected positive-timeout rejection, got: %v", err)
		}
	})

	t.Run("zero_alt_spelling_rejected", func(t *testing.T) {
		// "0h0m0s" also parses to a zero duration.
		if err := validateGateCreate("timer", "", "0h0m0s"); err == nil {
			t.Error("timer --timeout=0h0m0s must be rejected (parses to zero)")
		}
	})

	t.Run("positive_timeout_accepted", func(t *testing.T) {
		if err := validateGateCreate("timer", "", "2h"); err != nil {
			t.Errorf("timer --timeout=2h must be accepted, got: %v", err)
		}
		if err := validateGateCreate("timer", "", "1ns"); err != nil {
			t.Errorf("timer --timeout=1ns (smallest positive) must be accepted, got: %v", err)
		}
	})

	t.Run("empty_timeout_still_missing_timeout_error", func(t *testing.T) {
		// The empty-string leg keeps its original "requires --timeout" message
		// (not the new positive-timeout message) — the positive check only runs
		// on a parseable non-empty value.
		err := validateGateCreate("timer", "", "")
		if err == nil || !strings.Contains(err.Error(), "requires --timeout") {
			t.Errorf("empty timer timeout must keep the requires--timeout error, got: %v", err)
		}
	})

	t.Run("malformed_timeout_not_masked", func(t *testing.T) {
		// A malformed --timeout is owned by the upstream ParseDuration format
		// check at the call sites; validateGateCreate must NOT reject it (parse
		// error => skip the positive guard), so the caller emits the precise
		// "invalid timeout" message rather than this generic one.
		if err := validateGateCreate("timer", "", "notaduration"); err != nil {
			t.Errorf("malformed timeout must pass validateGateCreate (format is the call site's job), got: %v", err)
		}
	})

	// Non-timer types are unaffected by the timeout-positivity check.
	t.Run("human_ignores_timeout", func(t *testing.T) {
		if err := validateGateCreate("human", "", "0s"); err != nil {
			t.Errorf("human gate must ignore --timeout entirely, got: %v", err)
		}
	})
}
