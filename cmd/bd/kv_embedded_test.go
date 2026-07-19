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

// bdKV runs "bd kv" with the given args and returns stdout.
func bdKV(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"kv"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd kv %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdKVFail runs "bd kv" expecting failure.
func bdKVFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"kv"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd kv %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdKVListJSON runs "bd kv list --json" and returns parsed map.
func bdKVListJSON(t *testing.T, bd, dir string) map[string]string {
	t.Helper()
	cmd := exec.Command(bd, "kv", "list", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd kv list --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.Index(s, "{")
	if start < 0 {
		return map[string]string{}
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(s[start:]), &raw); err != nil {
		t.Fatalf("parse kv list JSON: %v\n%s", err, s)
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

func TestEmbeddedKV(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tk")

	// ===== Set and Get =====

	t.Run("kv_set_and_get", func(t *testing.T) {
		bdKV(t, bd, dir, "set", "mykey", "myvalue")
		out := bdKV(t, bd, dir, "get", "mykey")
		if !strings.Contains(out, "myvalue") {
			t.Errorf("expected 'myvalue' in get output: %s", out)
		}
	})

	t.Run("kv_set_overwrite", func(t *testing.T) {
		bdKV(t, bd, dir, "set", "overkey", "first")
		bdKV(t, bd, dir, "set", "overkey", "second")
		out := bdKV(t, bd, dir, "get", "overkey")
		if !strings.Contains(out, "second") {
			t.Errorf("expected 'second' after overwrite: %s", out)
		}
	})

	t.Run("kv_set_special_chars", func(t *testing.T) {
		bdKV(t, bd, dir, "set", "special", "hello world with spaces")
		out := bdKV(t, bd, dir, "get", "special")
		if !strings.Contains(out, "hello world with spaces") {
			t.Errorf("expected value with spaces: %s", out)
		}
	})

	// ===== List =====

	t.Run("kv_list", func(t *testing.T) {
		out := bdKV(t, bd, dir, "list")
		if !strings.Contains(out, "mykey") {
			t.Errorf("expected 'mykey' in list output: %s", out)
		}
	})

	t.Run("kv_list_json", func(t *testing.T) {
		m := bdKVListJSON(t, bd, dir)
		if v, ok := m["mykey"]; !ok || v != "myvalue" {
			t.Errorf("expected mykey=myvalue in JSON, got %q (exists=%v)", v, ok)
		}
	})

	// ===== Clear =====

	t.Run("kv_clear", func(t *testing.T) {
		bdKV(t, bd, dir, "set", "clearme", "temporary")
		out := bdKV(t, bd, dir, "get", "clearme")
		if !strings.Contains(out, "temporary") {
			t.Fatalf("expected 'temporary' before clear: %s", out)
		}
		bdKV(t, bd, dir, "clear", "clearme")
		// Verify it's gone from list
		m := bdKVListJSON(t, bd, dir)
		if _, ok := m["clearme"]; ok {
			t.Error("expected clearme to be absent from kv list after clear")
		}
	})

	// ===== Error Cases =====

	t.Run("kv_get_missing_key", func(t *testing.T) {
		// get on nonexistent key — check behavior
		cmd := exec.Command(bd, "kv", "get", "nonexistent_key_xyz")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		// May succeed with empty or fail — either way no crash
		_ = err
		_ = out
	})

	// beads-7qkq: `bd kv get <missing> --json` must exit rc0 (not rc1),
	// mirroring `bd config get <missing> --json` (which returns a set:false
	// result at rc0). Both are lookup-by-key commands that emit an explicit
	// found/set boolean; a successful lookup that finds nothing is a RESULT, not
	// an error — the found:false field communicates the miss, so failing the
	// exit code is redundant + a cross-command divergence that trips
	// `bd kv get k --json; test $? …` scripts. The TEXT branch keeps rc1
	// (shell ergonomics: `bd kv get k || default`), only the --json contract
	// is corrected.
	t.Run("kv_get_missing_json_exits_zero", func(t *testing.T) {
		cmd := exec.Command(bd, "kv", "get", "nonexistent_key_7qkq", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			t.Fatalf("bd kv get <missing> --json must exit 0 (miss is a result, not an error — mirror config get, beads-7qkq); got err=%v\nstdout:%s\nstderr:%s", err, stdout.String(), stderr.String())
		}
		// The JSON payload must still communicate the miss via found:false.
		var obj map[string]interface{}
		s := strings.TrimSpace(stdout.String())
		if start := strings.Index(s, "{"); start >= 0 {
			s = s[start:]
		}
		if e := json.Unmarshal([]byte(s), &obj); e != nil {
			t.Fatalf("kv get --json output is not valid JSON: %v\n%s", e, stdout.String())
		}
		if found, ok := obj["found"].(bool); !ok || found {
			t.Errorf("expected found:false for a missing key, got: %v", obj["found"])
		}
	})

	// The TEXT branch still signals non-zero on a miss (unchanged ergonomics).
	t.Run("kv_get_missing_text_exits_nonzero", func(t *testing.T) {
		cmd := exec.Command(bd, "kv", "get", "nonexistent_key_7qkq_text")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if err := cmd.Run(); err == nil {
			t.Errorf("bd kv get <missing> (text mode) should still exit non-zero for shell ergonomics (beads-7qkq scopes the change to --json only)")
		}
	})

	t.Run("kv_set_no_args", func(t *testing.T) {
		bdKVFail(t, bd, dir, "set")
	})

	t.Run("kv_get_no_args", func(t *testing.T) {
		bdKVFail(t, bd, dir, "get")
	})

	t.Run("kv_clear_no_args", func(t *testing.T) {
		bdKVFail(t, bd, dir, "clear")
	})

	// beads-v0rp: clearing a key that does not exist must fail loud, not print
	// a false "Cleared"/deleted:true success. DeleteConfig is idempotent (an
	// unconditional DELETE that no-ops on a missing key), so the CLI pre-checks
	// the key exists and reports honestly. Sibling of the landed dep-remove /
	// label-remove fixes (beads-w2tk/yaux).
	t.Run("kv_clear_nonexistent_fails", func(t *testing.T) {
		out := bdKVFail(t, bd, dir, "clear", "never_set_this_key")
		if strings.Contains(out, "Cleared") {
			t.Errorf("false success: clearing a nonexistent key printed 'Cleared': %s", out)
		}
		if !strings.Contains(out, "no key 'never_set_this_key' to clear") {
			t.Errorf("expected a 'no key ... to clear' error, got: %s", out)
		}
	})

	// A key set to the empty string still EXISTS, so clearing it must succeed
	// (guards against a GetConfig=="" false-negative that would treat an
	// empty-valued key as missing).
	t.Run("kv_clear_empty_value_key_succeeds", func(t *testing.T) {
		bdKV(t, bd, dir, "set", "empty_valued", "")
		out := bdKV(t, bd, dir, "clear", "empty_valued")
		if !strings.Contains(out, "Cleared") {
			t.Errorf("expected an empty-valued key to clear successfully, got: %s", out)
		}
		m := bdKVListJSON(t, bd, dir)
		if _, ok := m["empty_valued"]; ok {
			t.Error("expected empty_valued to be absent from kv list after clear")
		}
	})
}

// TestEmbeddedKVConcurrent exercises kv operations concurrently.
func TestEmbeddedKVConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "kx")

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

			// Each worker sets its own keys
			for i := 0; i < 5; i++ {
				key := fmt.Sprintf("w%d-k%d", worker, i)
				value := fmt.Sprintf("v%d-%d", worker, i)

				cmd := exec.Command(bd, "kv", "set", key, value)
				cmd.Dir = dir
				cmd.Env = bdEnv(dir)
				out, err := cmd.CombinedOutput()
				if err != nil {
					r.err = fmt.Errorf("set %s: %v\n%s", key, err, out)
					results[worker] = r
					return
				}
			}

			// Read back
			for i := 0; i < 5; i++ {
				key := fmt.Sprintf("w%d-k%d", worker, i)
				expected := fmt.Sprintf("v%d-%d", worker, i)

				cmd := exec.Command(bd, "kv", "get", key)
				cmd.Dir = dir
				cmd.Env = bdEnv(dir)
				out, err := cmd.CombinedOutput()
				if err != nil {
					r.err = fmt.Errorf("get %s: %v\n%s", key, err, out)
					results[worker] = r
					return
				}
				if !strings.Contains(string(out), expected) {
					r.err = fmt.Errorf("key %s expected %q, got %q", key, expected, string(out))
					results[worker] = r
					return
				}
			}

			// Clear one key
			clearKey := fmt.Sprintf("w%d-k0", worker)
			cmd := exec.Command(bd, "kv", "clear", clearKey)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("clear %s: %v\n%s", clearKey, err, out)
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

	// Verify remaining keys only for workers that succeeded (err==nil).
	// With exclusive flock, some workers may fail with "one writer at a time".
	m := bdKVListJSON(t, bd, dir)
	var successCount int
	for _, r := range results {
		if r.err != nil {
			continue
		}
		successCount++
		w := r.worker
		clearedKey := fmt.Sprintf("w%d-k0", w)
		if _, ok := m[clearedKey]; ok {
			t.Errorf("expected %s to be cleared", clearedKey)
		}
		for i := 1; i < 5; i++ {
			key := fmt.Sprintf("w%d-k%d", w, i)
			expected := fmt.Sprintf("v%d-%d", w, i)
			if v, ok := m[key]; !ok || v != expected {
				t.Errorf("key %s expected %q, got %q (exists=%v)", key, expected, v, ok)
			}
		}
	}
	if successCount == 0 {
		t.Fatal("expected at least 1 worker to succeed")
	}
}
