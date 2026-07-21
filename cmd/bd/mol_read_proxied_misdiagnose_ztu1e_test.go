//go:build cgo

package main

import (
	"strings"
	"testing"
)

// beads-ztu1e (ojyjj/mgjco/aocj fail-loud class, READ-side): the molecule
// inspection commands (bd mol show/current/progress/stale/distill/last-activity
// and bd mol ready --gated) guarded only with a bare `if store == nil { "no
// database connection" }`. In proxied-server mode main.go's PersistentPreRunE
// returns before newDoltStore (main.go:1147-1155) leaving the global store nil,
// so every one of these, run by a hub-connected crew (the whole cluster runs
// proxied-server mode), reported "no database connection" — the SAME string a
// genuinely-down Dolt server emits — sending operators chasing a phantom infra
// outage (the exact false-signal the ojyjj/mgjco write-side fixes eliminated).
//
// The fix replicates the ojyjj precedent (cmd/bd/mol_burn.go:90-99): an
// `if usesProxiedServer() { return HandleErrorRespectJSON("<cmd> is not
// supported in proxied-server mode (connect directly ...)") }` guard placed
// BEFORE the bare nil check, at all 7 read sites.
//
// This runs end-to-end through the real proxied-server subprocess
// (BEADS_TEST_PROXIED_SERVER=1). MUTATION-VERIFIED: with the guards removed each
// command exits with "no database connection" (RED); with the guards each emits
// the accurate "not supported in proxied-server mode" message (GREEN). The
// discriminator is that the command must NOT emit the literal "no database
// connection" — a false infra-outage signal — under a healthy proxied server.
func TestMolReadCommandsProxiedNotMisdiagnosed_ztu1e(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "mrp")

	// A real molecule root so the commands that take an id have a valid target;
	// the guard fires before any store use regardless, but this rules out an
	// "id not found" confound and proves the proxied server itself is healthy.
	root := bdProxiedCreate(t, bd, p.dir, "molecule root", "--type", "molecule")

	cases := []struct {
		name string
		args []string
	}{
		{"mol_show", []string{"mol", "show", root.ID}},
		{"mol_current", []string{"mol", "current"}},
		{"mol_progress", []string{"mol", "progress", root.ID}},
		{"mol_stale", []string{"mol", "stale"}},
		{"mol_distill", []string{"mol", "distill", root.ID}},
		{"mol_last_activity", []string{"mol", "last-activity", root.ID}},
		// The standalone `bd mol ready` subcommand's RunE (runMolReadyGated)
		// unconditionally runs runMolReadyGatedCore (the guarded site); the
		// --gated flag is only registered on the root `bd ready` command, so
		// `bd mol ready` alone reaches the guard.
		{"mol_ready_gated", []string{"mol", "ready"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, tc.args...)
			combined := stdout + stderr
			// The whole point: proxied mode must NOT be misdiagnosed as a local
			// "no database connection" (an infra-outage false signal).
			if strings.Contains(combined, "no database connection") {
				t.Fatalf("`bd %s` under proxied-server emitted the misdiagnosis \"no database connection\" (beads-ztu1e); want the accurate proxied-mode message.\nexit-err=%v\nstdout:\n%s\nstderr:\n%s",
					strings.Join(tc.args, " "), err, stdout, stderr)
			}
			// And it must be the accurate fail-loud message.
			if !strings.Contains(combined, "not supported in proxied-server mode") {
				t.Fatalf("`bd %s` under proxied-server did not emit the accurate \"not supported in proxied-server mode\" message (beads-ztu1e).\nexit-err=%v\nstdout:\n%s\nstderr:\n%s",
					strings.Join(tc.args, " "), err, stdout, stderr)
			}
			// It must fail loud (non-zero exit), not silently succeed.
			if err == nil {
				t.Fatalf("`bd %s` under proxied-server should exit non-zero (fail loud); got success.\nstdout:\n%s\nstderr:\n%s",
					strings.Join(tc.args, " "), stdout, stderr)
			}
		})
	}
}

// Regression guard: under --json the accurate message must be a parseable
// {error} object on STDOUT, not on STDERR — the json-error contract
// (HandleErrorRespectJSON) that the guard uses. Covers the one command whose
// bare misdiagnosis would otherwise reach a JSON consumer (mol show --json is a
// routine hub-crew automation call).
func TestMolShowProxiedJSONError_ztu1e(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "mrj")
	root := bdProxiedCreate(t, bd, p.dir, "molecule root", "--type", "molecule")

	stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "mol", "show", root.ID, "--json")
	if err == nil {
		t.Fatalf("`bd mol show --json` under proxied-server should fail; got success.\nstdout:\n%s", stdout)
	}
	if strings.Contains(stdout+stderr, "no database connection") {
		t.Fatalf("`bd mol show --json` under proxied-server emitted the misdiagnosis (beads-ztu1e).\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	s := strings.TrimSpace(stdout)
	if !strings.HasPrefix(s, "{") || !strings.Contains(s, "\"error\"") {
		t.Fatalf("`bd mol show --json` under proxied-server: want a JSON {error} object on STDOUT, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(s, "not supported in proxied-server mode") {
		t.Fatalf("`bd mol show --json` JSON error did not carry the accurate message (beads-ztu1e).\nstdout:\n%s", stdout)
	}
}
