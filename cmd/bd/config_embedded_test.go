//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// bdConfig runs "bd config" with the given args and returns stdout.
func bdConfig(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"config"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd config %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdConfigFail runs "bd config" expecting failure.
func bdConfigFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"config"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd config %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdConfigListJSON runs "bd config list --json" and returns parsed map.
func bdConfigListJSON(t *testing.T, bd, dir string) map[string]string {
	t.Helper()
	cmd := exec.Command(bd, "config", "list", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd config list --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("no JSON object in config list output: %s", s)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(s[start:]), &raw); err != nil {
		t.Fatalf("parse config list JSON: %v\n%s", err, s)
	}
	m := make(map[string]string, len(raw))
	for k, v := range raw {
		if k == "schema_version" {
			continue
		}
		if sv, ok := v.(string); ok {
			m[k] = sv
		}
	}
	return m
}

func TestEmbeddedConfig(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tc")

	// ===== Set and Get =====

	t.Run("config_set_and_get", func(t *testing.T) {
		bdConfig(t, bd, dir, "set", "custom.key1", "hello")
		out := bdConfig(t, bd, dir, "get", "custom.key1")
		if !strings.Contains(out, "hello") {
			t.Errorf("expected 'hello' in get output: %s", out)
		}
	})

	t.Run("config_set_overwrite", func(t *testing.T) {
		bdConfig(t, bd, dir, "set", "custom.overwrite", "first")
		bdConfig(t, bd, dir, "set", "custom.overwrite", "second")
		out := bdConfig(t, bd, dir, "get", "custom.overwrite")
		if !strings.Contains(out, "second") {
			t.Errorf("expected 'second' after overwrite: %s", out)
		}
	})

	t.Run("config_set_namespaced", func(t *testing.T) {
		bdConfig(t, bd, dir, "set", "jira.url", "https://example.atlassian.net")
		out := bdConfig(t, bd, dir, "get", "jira.url")
		if !strings.Contains(out, "https://example.atlassian.net") {
			t.Errorf("expected jira URL in output: %s", out)
		}
	})

	// beads-71zw: an unrecognized non-custom key must FAIL LOUD (warn +
	// non-zero exit + NOT write), not warn-then-write. custom.* stays the
	// escape hatch for user-defined keys.
	t.Run("config_set_unrecognized_key_rejected", func(t *testing.T) {
		out := bdConfigFail(t, bd, dir, "set", "totally.bogus.key", "somevalue")
		if !strings.Contains(out, "not a recognized config key") {
			t.Errorf("expected 'not a recognized config key' in error, got: %s", out)
		}
		// And it must NOT have been written.
		getOut := bdConfig(t, bd, dir, "get", "totally.bogus.key")
		if strings.Contains(getOut, "somevalue") {
			t.Errorf("rejected key must not be stored, but get returned: %s", getOut)
		}
	})

	t.Run("config_set_custom_key_accepted", func(t *testing.T) {
		// custom.* is the documented escape hatch — must still succeed.
		bdConfig(t, bd, dir, "set", "custom.anything", "ok")
		out := bdConfig(t, bd, dir, "get", "custom.anything")
		if !strings.Contains(out, "ok") {
			t.Errorf("expected custom.* key to be accepted+stored, got: %s", out)
		}
	})

	t.Run("config_set_and_get_linear_state_map_dotted_key", func(t *testing.T) {
		bdConfig(t, bd, dir, "set", "linear.state_map.closed", "Done")
		out := bdConfig(t, bd, dir, "get", "linear.state_map.closed")
		if strings.TrimSpace(out) != "Done" {
			t.Errorf("expected exact state_map value, got: %s", out)
		}
	})

	// ===== List =====

	t.Run("config_list", func(t *testing.T) {
		out := bdConfig(t, bd, dir, "list")
		// issue_prefix is always set by init
		if !strings.Contains(out, "issue_prefix") {
			t.Errorf("expected issue_prefix in list output: %s", out)
		}
	})

	t.Run("config_list_json", func(t *testing.T) {
		m := bdConfigListJSON(t, bd, dir)
		if _, ok := m["issue_prefix"]; !ok {
			t.Error("expected issue_prefix in JSON config list")
		}
		// Verify keys we set earlier are present
		if v, ok := m["custom.key1"]; !ok || v != "hello" {
			t.Errorf("expected custom.key1=hello, got %q", v)
		}
	})

	// ===== Unset =====

	t.Run("config_unset", func(t *testing.T) {
		bdConfig(t, bd, dir, "set", "test.removeme", "temp")
		// Verify it exists
		out := bdConfig(t, bd, dir, "get", "test.removeme")
		if !strings.Contains(out, "temp") {
			t.Fatalf("expected 'temp' before unset: %s", out)
		}
		// Unset it
		bdConfig(t, bd, dir, "unset", "test.removeme")
		// Verify it's gone — get returns "(not set)" with exit 0
		out = bdConfig(t, bd, dir, "get", "test.removeme")
		if !strings.Contains(out, "not set") {
			t.Errorf("expected 'not set' after unset: %s", out)
		}
		// Verify it's gone from list
		m := bdConfigListJSON(t, bd, dir)
		if _, ok := m["test.removeme"]; ok {
			t.Error("expected test.removeme to be absent from config list after unset")
		}
	})

	// beads-y3z2: unsetting a key that was never set must fail loud, not print
	// a false "Unset" success. DeleteConfig is idempotent (DELETE ... WHERE key
	// affects 0 rows → nil), so the CLI pre-checks existence and reports
	// honestly. Sibling of beads-v0rp (kv clear) / beads-w2tk (dep remove).
	t.Run("config_unset_nonexistent_fails", func(t *testing.T) {
		out := bdConfigFail(t, bd, dir, "unset", "never.set.key.xyz")
		if strings.Contains(out, "Unset") {
			t.Errorf("false success: unsetting a nonexistent key printed 'Unset': %s", out)
		}
		if !strings.Contains(out, "no such config key") {
			t.Errorf("expected 'no such config key' error, got: %s", out)
		}
	})

	// A key set to the empty string is still SET — unsetting it must succeed,
	// not be misreported as absent (GetConfig alone can't distinguish "" from
	// missing, so the pre-check uses membership).
	t.Run("config_unset_empty_value_key_succeeds", func(t *testing.T) {
		bdConfig(t, bd, dir, "set", "custom.empty_value_key", "")
		out := bdConfig(t, bd, dir, "unset", "custom.empty_value_key")
		if !strings.Contains(out, "Unset") {
			t.Errorf("expected 'Unset' for a key set to empty string: %s", out)
		}
	})

	// ===== Validate =====
	// Note: config validate checks dolt server connectivity which doesn't
	// apply to embedded mode, so we skip it here.

	// ===== Error Cases =====

	t.Run("config_get_missing_key", func(t *testing.T) {
		// get on missing key returns "(not set)" with exit 0
		out := bdConfig(t, bd, dir, "get", "nonexistent.key.xyz")
		if !strings.Contains(out, "not set") {
			t.Errorf("expected 'not set' for missing key: %s", out)
		}
	})

	t.Run("config_set_no_args", func(t *testing.T) {
		bdConfigFail(t, bd, dir, "set")
	})

	t.Run("config_unset_no_args", func(t *testing.T) {
		bdConfigFail(t, bd, dir, "unset")
	})
}

// TestEmbeddedConfigUnsetYamlOnlyKey covers the beads-o8h2 YAML-only-key branch
// of `bd config unset` (config.IsYamlOnlyKey → UnsetYamlConfig →
// commentOutYamlKey). commentOutYamlKey no-ops silently when the key was never
// present, so the branch used to print a false "Unset" rc=0 that a CI/agent
// gate reads as proof. Distinct file-parsing surface from the DB/proxied paths
// fixed by beads-y3z2.
func TestEmbeddedConfigUnsetYamlOnlyKey(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tcy")

	// export.auto is a project-scoped yaml-only key (export.* prefix).
	t.Run("unset_yaml_only_never_set_fails", func(t *testing.T) {
		out := bdConfigFail(t, bd, dir, "unset", "export.auto")
		if strings.Contains(out, "Unset") {
			t.Errorf("false success: unsetting a never-set yaml-only key printed 'Unset': %s", out)
		}
		if !strings.Contains(out, "no such config key") {
			t.Errorf("expected 'no such config key' error, got: %s", out)
		}
	})

	// A yaml-only key that IS set must unset successfully; a second unset of
	// the now-commented-out key must fail loud (matched=false).
	t.Run("unset_yaml_only_set_then_double_unset", func(t *testing.T) {
		bdConfig(t, bd, dir, "set", "export.auto", "true")
		out := bdConfig(t, bd, dir, "unset", "export.auto")
		if !strings.Contains(out, "Unset") {
			t.Errorf("expected 'Unset' for a set yaml-only key: %s", out)
		}
		out2 := bdConfigFail(t, bd, dir, "unset", "export.auto")
		if strings.Contains(out2, "Unset") {
			t.Errorf("false success: second unset of an already-unset key printed 'Unset': %s", out2)
		}
		if !strings.Contains(out2, "no such config key") {
			t.Errorf("expected 'no such config key' on second unset, got: %s", out2)
		}
	})
}

// TestEmbeddedConfigConcurrent exercises config operations concurrently.
func TestEmbeddedConfigConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cx")

	const numWorkers = 8

	type workerResult struct {
		worker int
		err    error
	}

	results := make([]workerResult, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			r := workerResult{worker: worker}

			// Each worker sets its own namespaced keys
			for i := 0; i < 5; i++ {
				key := fmt.Sprintf("worker%d.key%d", worker, i)
				value := fmt.Sprintf("value-%d-%d", worker, i)

				cmd := exec.Command(bd, "config", "set", key, value)
				cmd.Dir = dir
				cmd.Env = bdEnv(dir)
				out, err := cmd.CombinedOutput()
				if err != nil {
					r.err = fmt.Errorf("set %s: %v\n%s", key, err, out)
					results[worker] = r
					return
				}
			}

			// Read back and verify
			for i := 0; i < 5; i++ {
				key := fmt.Sprintf("worker%d.key%d", worker, i)
				expected := fmt.Sprintf("value-%d-%d", worker, i)

				cmd := exec.Command(bd, "config", "get", key)
				cmd.Dir = dir
				cmd.Env = bdEnv(dir)
				out, err := cmd.CombinedOutput()
				if err != nil {
					r.err = fmt.Errorf("get %s: %v\n%s", key, err, out)
					results[worker] = r
					return
				}
				if !strings.Contains(string(out), expected) {
					r.err = fmt.Errorf("worker %d: key %s expected %q, got %q", worker, key, expected, string(out))
					results[worker] = r
					return
				}
			}

			// List all config
			cmd := exec.Command(bd, "config", "list", "--json")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("list --json: %v\n%s", err, out)
				results[worker] = r
				return
			}

			results[worker] = r
		}(w)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil && !strings.Contains(r.err.Error(), "one writer at a time") {
			t.Errorf("worker %d failed: %v", r.worker, r.err)
		}
	}

	// Verify keys only for workers that succeeded (err==nil).
	// With exclusive flock, some workers may fail with "one writer at a time".
	m := bdConfigListJSON(t, bd, dir)
	var successCount int
	for _, r := range results {
		if r.err != nil {
			continue
		}
		successCount++
		w := r.worker
		for i := 0; i < 5; i++ {
			key := fmt.Sprintf("worker%d.key%d", w, i)
			expected := fmt.Sprintf("value-%d-%d", w, i)
			if v, ok := m[key]; !ok || v != expected {
				t.Errorf("after concurrent writes: key %s expected %q, got %q (exists=%v)", key, expected, v, ok)
			}
		}
	}
	if successCount == 0 {
		t.Fatal("expected at least 1 worker to succeed")
	}
}

// bdConfigGetJSON runs "bd config get <key> --json" and returns the parsed
// object (schema_version included).
func bdConfigGetJSON(t *testing.T, bd, dir, key string) map[string]interface{} {
	t.Helper()
	cmd := exec.Command(bd, "config", "get", key, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd config get %s --json failed: %v\nstdout:\n%s\nstderr:\n%s", key, err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("no JSON object in config get output: %s", s)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(s[start:]), &raw); err != nil {
		t.Fatalf("parse config get JSON: %v\n%s", err, s)
	}
	return raw
}

// TestEmbeddedConfigGetJSONSetSignal is the teeth for beads-aj9r: `bd config
// get --json` must carry a `set` bool so a machine consumer can distinguish an
// explicitly-empty value from a never-set key — value=="" alone cannot. The
// store branch (custom.*) previously collapsed both to {value:""} with no
// signal because store.GetConfig maps sql.ErrNoRows -> ("", nil).
func TestEmbeddedConfigGetJSONSetSignal(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "aj")

	// A key explicitly set to the empty string is SET.
	bdConfig(t, bd, dir, "set", "custom.empty_but_set", "")
	got := bdConfigGetJSON(t, bd, dir, "custom.empty_but_set")
	if v, _ := got["value"].(string); v != "" {
		t.Errorf("empty-set key: expected value \"\", got %q", v)
	}
	set, ok := got["set"].(bool)
	if !ok {
		t.Fatalf("config get --json missing bool `set` field for an explicitly-set key: %v", got)
	}
	if !set {
		t.Errorf("explicitly-empty-but-set key must report set=true, got set=false (%v)", got)
	}

	// A never-set custom key must report set=false (same value:"" as above).
	got2 := bdConfigGetJSON(t, bd, dir, "custom.never_set_key_xyz")
	set2, ok := got2["set"].(bool)
	if !ok {
		t.Fatalf("config get --json missing bool `set` field for a never-set key: %v", got2)
	}
	if set2 {
		t.Errorf("never-set key must report set=false, got set=true (%v)", got2)
	}
}
