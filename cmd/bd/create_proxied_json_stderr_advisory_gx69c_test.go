//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedCreateJSONStderrAdvisory_gx69c is the PROXIED-path twin of the
// beads-gx69c defer-in-past teeth (8lqh json-error contract, Direction-2
// interleaved-stderr class; sibling of beads-8zed6).
//
// The DIRECT create path warns about a past --defer at create.go:284; the
// PROXIED path (gatherCreateInput, create_input.go, the live path for
// hub-connected crew) has the SAME advisory. runCreateProxiedSingle emits the
// created-issue JSON envelope on stdout under --json (create_proxied_server.go),
// but the defer-in-past advisory wrote to os.Stderr guarded only by
// `!in.silent && !debug.IsQuiet()` (NOT !jsonOutput). --json does not set
// silent/quiet, so under `bd create --defer <past> --json 2>&1 | jq` the leading
// `!` advisory interleaves with the JSON object -> parse failure. The defer date
// is already carried in the payload (defer_until), so the hint is pure-human and
// safe to suppress under --json. The fix gates it under && !jsonOutput.
//
// The existing proxied due/defer test (beads-82pv3) parses stdout ONLY, so it
// cannot catch a stderr interleave — this teeth combines 2>&1 and asserts a
// single JSON object.
//
// Mutation-verify: drop `&& !jsonOutput` from the create_input.go guard and the
// json subtest goes RED (the leading `!` advisory trips json.Unmarshal on the
// combined 2>&1 stream); the plain positive-control stays GREEN.
func TestProxiedCreateJSONStderrAdvisory_gx69c(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pgx")

	// (1) --defer with a PAST date + --json: the 2>&1 stream must parse as a
	//     single JSON object. The positive control below proves the branch is
	//     reachable, so this cannot false-green.
	t.Run("defer_past_json_is_pure", func(t *testing.T) {
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "create",
			"proxied defer-in-past json probe", "--type", "task",
			"--defer", "2020-01-01", "--json")
		if err != nil {
			t.Fatalf("proxied `bd create --defer <past> --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		combined := strings.TrimSpace(stdout + stderr)
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(combined), &obj); jerr != nil {
			t.Fatalf("beads-gx69c: proxied `bd create --json` 2>&1 is NOT a single JSON object "+
				"(defer-in-past advisory leaked plaintext to stderr): %v\ncombined:\n%s", jerr, combined)
		}
		// Proxied output may be enveloped ({data:{...}} or {schema_version,...}).
		if _, ok := obj["id"]; !ok {
			if data, ok := obj["data"].(map[string]interface{}); ok {
				if _, ok := data["id"]; ok {
					return
				}
			}
			t.Errorf("beads-gx69c: parsed proxied JSON lacks the created-issue 'id' field; got: %s", combined)
		}
	})

	// (2) Positive control: WITHOUT --json a past --defer still prints the
	//     advisory to stderr. Proves the defer-past branch is genuinely reachable
	//     on the proxied path — otherwise subtest (1) would pass vacuously.
	t.Run("defer_past_plain_keeps_advisory", func(t *testing.T) {
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "create",
			"proxied defer-in-past plain probe", "--type", "task",
			"--defer", "2020-01-01")
		if err != nil {
			t.Fatalf("proxied `bd create --defer <past>` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "is in the past") {
			t.Errorf("beads-gx69c: plain (non-json) proxied create with a past --defer must still "+
				"print the past-defer advisory to stderr (fix suppresses only under --json); "+
				"stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})
}
