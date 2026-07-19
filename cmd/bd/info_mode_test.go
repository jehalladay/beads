package main

import "testing"

// TestResolveInfoMode is the teeth for beads-28ai: `bd info` used to print a
// hardcoded "direct" mode regardless of the real backend, misreporting a
// server- or proxied-server-backed workspace as a local embedded DB. The mode
// string must now be derived from the runtime backend detection (the same
// signals usesProxiedServer()/usesSQLServer() feed), so a server-backed
// workspace no longer lies about being direct.
func TestResolveInfoMode(t *testing.T) {
	cases := []struct {
		name     string
		proxied  bool
		server   bool
		expected string
	}{
		{"embedded_direct", false, false, "direct"},
		{"server_mode", false, true, "server"},
		{"proxied_server", true, true, "proxied-server"},
		// proxied implies a SQL server; even if the server flag were not
		// separately set, proxied must win over "direct".
		{"proxied_wins_over_direct", true, false, "proxied-server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveInfoMode(tc.proxied, tc.server)
			if got != tc.expected {
				t.Errorf("resolveInfoMode(proxied=%v, server=%v) = %q, want %q",
					tc.proxied, tc.server, got, tc.expected)
			}
		})
	}
}
