//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedGateCreateRejectsUnresolvableGates is the PROXIED twin of the
// beads-ds9tr guard: runGateCreateProxied must also reject a timer-without-timeout
// and an invalid gate type before create, matching the direct path.
func TestProxiedGateCreateRejectsUnresolvableGates(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("timer_without_timeout_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gpv")
		target := bdProxiedCreate(t, bd, p.dir, "Gate target timer", "--type", "task")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "create", "--type", "timer", "--blocks", target.ID)
		if err == nil {
			t.Fatalf("timer without --timeout must be rejected, got success:\n%s", stdout)
		}
		if !strings.Contains(stdout+stderr, "requires --timeout") {
			t.Errorf("expected timer-requires-timeout rejection, got stdout=%q stderr=%q", stdout, stderr)
		}
	})

	t.Run("invalid_type_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gpi")
		target := bdProxiedCreate(t, bd, p.dir, "Gate target bad", "--type", "task")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "create", "--type", "banana", "--blocks", target.ID)
		if err == nil {
			t.Fatalf("invalid gate type must be rejected, got success:\n%s", stdout)
		}
		if !strings.Contains(stdout+stderr, "invalid gate type") {
			t.Errorf("expected invalid-gate-type rejection, got stdout=%q stderr=%q", stdout, stderr)
		}
	})

	t.Run("timer_with_timeout_accepted", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gpt")
		target := bdProxiedCreate(t, bd, p.dir, "Gate target ok", "--type", "task")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "gate", "create", "--type", "timer", "--timeout", "2h", "--blocks", target.ID)
		if err != nil {
			t.Fatalf("timer+timeout must be accepted: %v\n%s\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, "Created gate") {
			t.Errorf("expected gate-created, got: %s", stdout)
		}
	})
}
