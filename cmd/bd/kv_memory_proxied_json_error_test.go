//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// beads-21vns: `bd kv` (set/get/clear/list) and the memory subsystem
// (remember/memories/forget/recall) are thin wrappers over the config store,
// but — unlike `bd config` — only had the direct-store path. In proxied-server
// mode their ensureDirectMode() guard failed hard (newDoltStoreFromConfig
// returns "proxy server store should be uow provider"), so the ENTIRE
// hub-connected fleet could not use `bd kv` or `bd remember` (the
// persistent-knowledge subsystem CLAUDE.md mandates). This routes each through
// the proxied UOW like `bd config`.
//
// Before beads-21vns the sibling test beads-5fu1 asserted these commands
// *failed* in proxied mode (it only made the failure emit clean --json). That
// premise is now inverted: they must WORK. This test proves the round-trip
// persists and the --json shape is preserved on both success and legitimate
// error paths.
func TestProxiedServerKVMemory(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// The core bug: bd kv set → get round-trips over the proxied server.
	// Mutation-verify: with the usesProxiedServer() branch removed, `kv set`
	// fails ensureDirectMode and this Fatalf's.
	t.Run("kv_set_get_roundtrip", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "kvrt")
		out, err := bdProxiedRun(t, bd, p.dir, "kv", "set", "feature_flag", "on")
		if err != nil {
			t.Fatalf("proxied bd kv set failed (beads-21vns nil-store): %v\n%s", err, out)
		}
		got, err := bdProxiedRun(t, bd, p.dir, "kv", "get", "feature_flag")
		if err != nil {
			t.Fatalf("proxied bd kv get failed: %v\n%s", err, got)
		}
		if !strings.Contains(string(got), "on") {
			t.Errorf("kv get did not return the set value; got: %s", got)
		}
	})

	// list must surface the set key (round-trips through GetAllConfig + the
	// kvPrefix filter on the proxied path).
	t.Run("kv_list_shows_key", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "kvls")
		if _, err := bdProxiedRun(t, bd, p.dir, "kv", "set", "api_endpoint", "https://x"); err != nil {
			t.Fatalf("kv set failed: %v", err)
		}
		out := bdProxiedRunOrFail(t, bd, p.dir, "kv", "list")
		if !strings.Contains(out, "api_endpoint") {
			t.Errorf("kv list missing the set key: %s", out)
		}
	})

	// clear removes the key AND fails-loud on a missing key (existence
	// pre-check must run against the proxied config map, not the nil store).
	t.Run("kv_clear_removes_and_missing_errors", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "kvcl")
		if _, err := bdProxiedRun(t, bd, p.dir, "kv", "set", "temp", "1"); err != nil {
			t.Fatalf("kv set failed: %v", err)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "kv", "clear", "temp"); err != nil {
			t.Fatalf("kv clear of an existing key must succeed: %v", err)
		}
		list := bdProxiedRunOrFail(t, bd, p.dir, "kv", "list")
		if strings.Contains(list, "temp") {
			t.Errorf("cleared key still present: %s", list)
		}
		// Missing-key clear is a fail-loud error even on the proxied path.
		out, err := bdProxiedRun(t, bd, p.dir, "kv", "clear", "nope")
		if err == nil {
			t.Errorf("kv clear of a missing key must error; got:\n%s", out)
		}
	})

	// The memory subsystem round-trips (remember → recall → forget). This is
	// the CLAUDE.md-mandated persistent-knowledge path; without 21vns it is
	// wholly unusable for the fleet.
	t.Run("remember_recall_forget_roundtrip", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "memrt")
		if _, err := bdProxiedRun(t, bd, p.dir, "remember", "always run tests with -race", "--key", "race-flag"); err != nil {
			t.Fatalf("proxied bd remember failed (beads-21vns nil-store): %v", err)
		}
		got := bdProxiedRunOrFail(t, bd, p.dir, "recall", "race-flag")
		if !strings.Contains(got, "-race") {
			t.Errorf("recall did not return the stored memory; got: %s", got)
		}
		mem := bdProxiedRunOrFail(t, bd, p.dir, "memories")
		if !strings.Contains(mem, "race-flag") {
			t.Errorf("memories list missing the stored key: %s", mem)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "forget", "race-flag"); err != nil {
			t.Fatalf("proxied bd forget failed: %v", err)
		}
		after := bdProxiedRunOrFail(t, bd, p.dir, "memories")
		if strings.Contains(after, "race-flag") {
			t.Errorf("forgotten memory still listed: %s", after)
		}
	})

	// beads-5fu1 retained: --json success envelopes are well-formed on the
	// proxied path (regression guard for the JSON-contract class that 5fu1
	// originally covered — now on the working path, not the failure path).
	t.Run("json_envelopes_wellformed", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "kvjson")
		// kv set --json
		out := bdProxiedRunOrFail(t, bd, p.dir, "kv", "set", "k", "v", "--json")
		assertJSONField(t, out, "key", "k")
		// kv get of a MISSING key: found:false at rc0 (beads-7qkq parity), not
		// an error — must still be a parseable envelope on the proxied path.
		got, gerr := bdProxiedRun(t, bd, p.dir, "kv", "get", "absent", "--json")
		if gerr != nil {
			t.Fatalf("kv get --json of a missing key must be rc0 (found:false): %v\n%s", gerr, got)
		}
		var m map[string]any
		s := strings.TrimSpace(string(got))
		if i := strings.IndexByte(s, '{'); i >= 0 {
			s = s[i:]
		}
		if jerr := json.Unmarshal([]byte(s), &m); jerr != nil {
			t.Fatalf("kv get --json not a JSON object: %v\n%s", jerr, got)
		}
		if m["found"] != false {
			t.Errorf("expected found:false for a missing key, got: %v", m)
		}
		// remember --json
		rem := bdProxiedRunOrFail(t, bd, p.dir, "remember", "note here", "--key", "nk", "--json")
		assertJSONField(t, rem, "action", "remembered")
	})
}

// assertJSONField parses the first JSON object in out and asserts field==want.
func assertJSONField(t *testing.T, out, field, want string) {
	t.Helper()
	s := strings.TrimSpace(out)
	if i := strings.IndexByte(s, '{'); i >= 0 {
		s = s[i:]
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, out)
	}
	if got, _ := m[field].(string); got != want {
		t.Errorf("expected %s=%q, got %q (full: %v)", field, want, got, m)
	}
}
