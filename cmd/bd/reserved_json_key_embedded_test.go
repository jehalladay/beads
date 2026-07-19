//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestReservedJSONKeyRejectedAtWrite is the teeth for beads-z0fe (ruling c1):
// bd kv set / bd remember --key must REJECT a key whose name collides with a
// key wrapWithSchemaVersion injects into --json output (schema_version always;
// data under BD_JSON_ENVELOPE=1). Before the guard, such a key was accepted and
// then SILENTLY CLOBBERED when the flat map (`bd kv list --json` /
// `bd memories --json`) was wrapped — data-loss. The c1 fix turns silent loss
// into a loud, non-zero, JSON-error-contract-respecting rejection at write time,
// while preserving the established flat-map read contract (bdKVListJSON parses a
// flat map[string]string and skips schema_version; 4+ call sites depend on it).
func TestReservedJSONKeyRejectedAtWrite(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rk")

	reserved := []string{"schema_version", "data"}

	// (a) bd kv set <reserved> → error + rc!=0.
	for _, key := range reserved {
		key := key
		t.Run("kv_set_"+key+"_rejected", func(t *testing.T) {
			cmd := exec.Command(bd, "kv", "set", key, "somevalue")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("bd kv set %s must be rejected (reserved JSON key, beads-z0fe), got rc=0:\n%s", key, out)
			}
			if !strings.Contains(string(out), "reserved") {
				t.Errorf("expected a 'reserved' error for kv set %s, got:\n%s", key, out)
			}
			// Must NOT have persisted: it must be absent from the flat list.
			listed := bdKVListJSON(t, bd, dir)
			if _, ok := listed[key]; ok {
				t.Errorf("kv set %s persisted despite being reserved (z0fe data-loss guard failed): %v", key, listed)
			}
		})
	}

	// (a') bd kv set <reserved> --json → JSON error object on STDOUT, rc!=0.
	// bd's --json error contract routes through HandleErrorRespectJSON →
	// jsonStdoutError: the structured {"error":...} object is written to stdout
	// (not stderr) with a non-zero exit. Assert that shape.
	t.Run("kv_set_reserved_json_error_contract", func(t *testing.T) {
		cmd := exec.Command(bd, "kv", "set", "schema_version", "x", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Fatalf("bd kv set schema_version --json must fail, got rc=0\nstdout:%s\nstderr:%s", stdout.String(), stderr.String())
		}
		// stdout carries a JSON object with an "error" field (jsonStdoutError).
		s := strings.TrimSpace(stdout.String())
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("expected a JSON error object on stdout, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		}
		var obj map[string]interface{}
		if e := json.Unmarshal([]byte(s[start:]), &obj); e != nil {
			t.Fatalf("stdout is not valid JSON (--json error contract): %v\n%s", e, s)
		}
		ev, ok := obj["error"]
		if !ok {
			t.Errorf("expected an \"error\" key in the JSON error object, got: %v", obj)
		}
		if es, _ := ev.(string); !strings.Contains(es, "reserved") {
			t.Errorf("expected the error message to mention 'reserved', got: %v", ev)
		}
	})

	// (b) bd remember --key <reserved> → error + rc!=0.
	for _, key := range reserved {
		key := key
		t.Run("remember_key_"+key+"_rejected", func(t *testing.T) {
			out := bdRememberFail(t, bd, dir, "some memory content", "--key", key)
			if !strings.Contains(out, "reserved") {
				t.Errorf("expected a 'reserved' error for remember --key %s, got:\n%s", key, out)
			}
		})
	}

	// (c) a normal key still works on both write paths.
	t.Run("normal_kv_key_still_works", func(t *testing.T) {
		bdKV(t, bd, dir, "set", "feature_flag", "true")
		out := bdKV(t, bd, dir, "get", "feature_flag")
		if !strings.Contains(out, "true") {
			t.Errorf("expected normal kv key to still work: %s", out)
		}
	})
	t.Run("normal_remember_key_still_works", func(t *testing.T) {
		bdRemember(t, bd, dir, "auth uses JWT not sessions", "--key", "auth-note")
	})

	// (d) the flat-map --json read contract is preserved (kv list --json parses
	// as a flat map[string]string; the normal key set in (c) is present).
	t.Run("flat_map_json_contract_preserved", func(t *testing.T) {
		listed := bdKVListJSON(t, bd, dir)
		if listed["feature_flag"] != "true" {
			t.Errorf("expected feature_flag=true in flat kv list --json map, got: %v", listed)
		}
	})

	// (e) config leg (z0fe 3rd member, ruling: warn-not-reject): `schema_version`
	// is a BUILT-IN config key (config.go recognized-keys), so it CANNOT be
	// rejected at write like a user kv/memory key — and `config list --json`'s
	// flat-map shape is a tested contract, so we cannot nest. Instead
	// `config list --json` must WARN on stderr that the colliding key is
	// overwritten (so the loss is not silent), while still exiting 0.
	t.Run("config_reserved_key_warns_on_list_json", func(t *testing.T) {
		// schema_version is a recognized built-in key → set succeeds.
		setCmd := exec.Command(bd, "config", "set", "schema_version", "42")
		setCmd.Dir = dir
		setCmd.Env = bdEnv(dir)
		if out, err := setCmd.CombinedOutput(); err != nil {
			t.Fatalf("config set schema_version (a built-in key) should succeed: %v\n%s", err, out)
		}

		listCmd := exec.Command(bd, "config", "list", "--json")
		listCmd.Dir = dir
		listCmd.Env = bdEnv(dir)
		var stdout, stderr strings.Builder
		listCmd.Stdout = &stdout
		listCmd.Stderr = &stderr
		if err := listCmd.Run(); err != nil {
			t.Fatalf("config list --json should exit 0 (warn, not fail): %v\nstderr:%s", err, stderr.String())
		}
		// The collision must be announced on stderr (not silent data-loss).
		if !strings.Contains(stderr.String(), "schema_version") || !strings.Contains(strings.ToLower(stderr.String()), "collides") {
			t.Errorf("expected a stderr collision warning naming schema_version, got stderr:\n%s", stderr.String())
		}
	})
}
