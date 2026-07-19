//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerGraphCheck proves `bd graph check` is proxied-server-aware
// (beads-jc8k). Before the fix, graphCheckCmd's RunE called the nil global
// `store.DetectCycles` unconditionally (graph.go L200) with no `store == nil`
// guard and no usesProxiedServer() gate, so `bd graph check` panicked
// "storage is nil" for a hub-connected crew. (The main `bd graph` RunE has the
// nil guard; the check subcommand did not.)
//
// This is a clean-mirror leg: DetectCycles exists on DependencyUseCase
// (internal/storage/domain/dependency.go) and runDepCyclesProxiedServer already
// calls uw.DependencyUseCase().DetectCycles, mirroring dep_proxied_server.go.
func TestProxiedServerGraphCheck(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "gchk")

	// A couple of unrelated issues; no cycles -> the check should pass clean.
	_ = bdProxiedCreate(t, bd, p.dir, "first issue", "--type", "task")
	_ = bdProxiedCreate(t, bd, p.dir, "second issue", "--type", "task")

	t.Run("clean_graph_exits_zero", func(t *testing.T) {
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "graph", "check")
		if err != nil {
			t.Fatalf("bd graph check failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd graph check hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
	})

	t.Run("json_clean", func(t *testing.T) {
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "graph", "check", "--json")
		if err != nil {
			t.Fatalf("bd graph check --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd graph check --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "\"clean\"") {
			t.Errorf("expected JSON with a \"clean\" field, got:\n%s", stdout)
		}
	})
}
