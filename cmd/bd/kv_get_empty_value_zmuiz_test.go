//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedKVGetEmptyValue_zmuiz proves `bd kv get` distinguishes a key that
// was explicitly set to the empty string ("" — allowed, `bd kv set k ""`) from a
// never-set key. The pre-fix code derived found-ness from `value != ""`, which is
// wrong: kvGetConfig -> GetConfigInTx maps sql.ErrNoRows -> ("", nil), collapsing
// "row absent" and "present-but-empty" to the same empty string. So an existing
// empty-valued key was reported "(not set)" / found:false (a false negative). The
// fix derives existence from GetAllConfig membership (same source `bd kv list`
// reads), mirroring beads-aj9r for the sibling `bd config get`.
func TestEmbeddedKVGetEmptyValue_zmuiz(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ze")

	// An empty-valued key EXISTS. Text-mode get must succeed (rc0), print the
	// empty value, and NOT report "(not set)".
	t.Run("empty_valued_get_text_reports_found", func(t *testing.T) {
		bdKV(t, bd, dir, "set", "empty_z", "")
		cmd := exec.Command(bd, "kv", "get", "empty_z")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("bd kv get on an empty-valued key must exit 0 (the key exists), got err=%v\nstdout:%q\nstderr:%q", err, stdout.String(), stderr.String())
		}
		if strings.Contains(stderr.String(), "(not set)") {
			t.Errorf("false negative: an existing empty-valued key reported '(not set)': stderr=%q", stderr.String())
		}
	})

	// --json for an empty-valued key must report found:true, value:"".
	t.Run("empty_valued_get_json_found_true", func(t *testing.T) {
		bdKV(t, bd, dir, "set", "empty_zj", "")
		cmd := exec.Command(bd, "kv", "get", "empty_zj", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("bd kv get --json on an empty-valued key must exit 0, got err=%v\nstderr:%q", err, stderr.String())
		}
		obj := parseKVGetJSON(t, stdout.String())
		if found, ok := obj["found"].(bool); !ok || !found {
			t.Errorf("expected found:true for an existing empty-valued key, got: %v", obj["found"])
		}
		if v, ok := obj["value"].(string); !ok || v != "" {
			t.Errorf("expected value:\"\" for an empty-valued key, got: %v", obj["value"])
		}
	})

	// Negative control: a genuinely never-set key must still report found:false.
	t.Run("missing_key_json_found_false", func(t *testing.T) {
		cmd := exec.Command(bd, "kv", "get", "never_set_zmuiz", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("bd kv get <missing> --json must exit 0 (beads-7qkq), got err=%v\nstderr:%q", err, stderr.String())
		}
		obj := parseKVGetJSON(t, stdout.String())
		if found, ok := obj["found"].(bool); !ok || found {
			t.Errorf("expected found:false for a never-set key, got: %v", obj["found"])
		}
	})

	// Positive control: a non-empty value still reads back found:true with value.
	t.Run("nonempty_value_get_found_true", func(t *testing.T) {
		bdKV(t, bd, dir, "set", "nonempty_z", "hello")
		cmd := exec.Command(bd, "kv", "get", "nonempty_z", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("bd kv get --json on a set key must exit 0, got err=%v\nstderr:%q", err, stderr.String())
		}
		obj := parseKVGetJSON(t, stdout.String())
		if found, ok := obj["found"].(bool); !ok || !found {
			t.Errorf("expected found:true for a set key, got: %v", obj["found"])
		}
		if v, ok := obj["value"].(string); !ok || v != "hello" {
			t.Errorf("expected value:\"hello\", got: %v", obj["value"])
		}
	})
}

// parseKVGetJSON parses a `bd kv get --json` payload, tolerating leading
// non-JSON noise (advisory lines) the way the sibling 7qkq test does.
func parseKVGetJSON(t *testing.T, out string) map[string]interface{} {
	t.Helper()
	s := strings.TrimSpace(out)
	if start := strings.Index(s, "{"); start >= 0 {
		s = s[start:]
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		t.Fatalf("kv get --json output is not valid JSON: %v\n%s", err, out)
	}
	return obj
}
