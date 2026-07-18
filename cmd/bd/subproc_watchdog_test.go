//go:build cgo

package main

import (
	"os/exec"
	"testing"
	"time"
)

// TestProxiedTestSubprocessTimeout covers the env-configurable bound (beads-cine).
func TestProxiedTestSubprocessTimeout(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"default_when_unset", "", 120 * time.Second},
		{"explicit_valid", "45s", 45 * time.Second},
		{"zero_disables", "0", 0},
		{"bad_value_falls_back_to_default", "not-a-duration", 120 * time.Second},
		{"negative_falls_back_to_default", "-5s", 120 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == "" {
				t.Setenv("BEADS_TEST_SUBPROC_TIMEOUT", "")
			} else {
				t.Setenv("BEADS_TEST_SUBPROC_TIMEOUT", tc.env)
			}
			if got := proxiedTestSubprocessTimeout(); got != tc.want {
				t.Errorf("timeout for %q: got %s, want %s", tc.env, got, tc.want)
			}
		})
	}
}

// TestRunCmdWithWatchdog is the teeth for beads-cine: a hung subprocess must be
// killed within the bound (fast) instead of hanging the suite until the 10m
// default, and a fast subprocess must NOT be falsely killed.
func TestRunCmdWithWatchdog(t *testing.T) {
	t.Run("hung_process_killed_within_bound", func(t *testing.T) {
		// `sleep 60` stands in for a hung bd; the 200ms bound must fire long
		// before that (and vastly before the 10m suite default).
		cmd := exec.Command("sleep", "60")
		start := time.Now()
		_, timedOut := runCmdWithWatchdog(cmd, 200*time.Millisecond)
		elapsed := time.Since(start)
		if !timedOut {
			t.Fatalf("expected the watchdog to time out on `sleep 60`, got timedOut=false")
		}
		if elapsed > 5*time.Second {
			t.Errorf("watchdog took %s to fire on a 200ms bound — should kill promptly", elapsed)
		}
	})

	t.Run("fast_process_not_killed", func(t *testing.T) {
		cmd := exec.Command("true")
		err, timedOut := runCmdWithWatchdog(cmd, 30*time.Second)
		if timedOut {
			t.Errorf("fast `true` should NOT time out under a 30s bound")
		}
		if err != nil {
			t.Errorf("fast `true` should succeed, got err=%v", err)
		}
	})

	t.Run("zero_timeout_disables_watchdog", func(t *testing.T) {
		// With the bound disabled, a fast process still runs via cmd.Run and
		// never reports timedOut.
		cmd := exec.Command("true")
		err, timedOut := runCmdWithWatchdog(cmd, 0)
		if timedOut {
			t.Errorf("timeout=0 must disable the watchdog (timedOut should be false)")
		}
		if err != nil {
			t.Errorf("fast `true` with disabled watchdog should succeed, got err=%v", err)
		}
	})
}
