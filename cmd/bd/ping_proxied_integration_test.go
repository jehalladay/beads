//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerPing proves bd ping is proxied-server-aware (beads-jegd5):
// ping.go called getStore() unconditionally, which is nil in proxiedServerMode
// (main.go wires uowProvider then returns before store init) → ping ALWAYS
// false-failed "store not initialized" for the whole hub-connected fleet, even
// with a healthy proxied connection, and contradicting ping's own help ("Open
// the store (embedded OR server)"). The fix routes the read-only SearchIssues
// probe through a proxied read UOW (runPingProxiedServer), mirroring bd
// count/list/show — the aocj/fszd/eh0z proxied umbrella that missed this sibling
// (same class as beads-9dsym bd todo).
//
// MUTATION-VERIFY: delete the usesProxiedServer() gate in ping.go's RunE and
// both sub-tests go RED ("store not initialized", exit 1).
func TestProxiedServerPing(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("ping_ok", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "png")
		bdProxiedCreate(t, bd, p.dir, "one", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "ping")
		if err != nil {
			t.Fatalf("bd ping failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "store not initialized") {
			t.Fatalf("bd ping hit 'store not initialized' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "ok") {
			t.Errorf("expected bd ping to report ok:\n%s", stdout)
		}
	})

	t.Run("ping_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pnj")
		bdProxiedCreate(t, bd, p.dir, "a", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "ping", "--json")
		if err != nil {
			t.Fatalf("bd ping --json failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "store not initialized") {
			t.Fatalf("bd ping --json hit 'store not initialized':\n%s\n%s", stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("no JSON object in ping output:\n%s", stdout)
		}
		var out struct {
			Status  string `json:"status"`
			TotalMs int64  `json:"total_ms"`
		}
		if err := json.Unmarshal([]byte(stdout[start:]), &out); err != nil {
			t.Fatalf("parse ping JSON: %v\nraw: %s", err, stdout[start:])
		}
		if out.Status != "ok" {
			t.Errorf("expected status ok, got %q\nraw: %s", out.Status, stdout[start:])
		}
	})
}
