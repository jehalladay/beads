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

// bdHooks runs "bd hooks" with the given args and returns stdout.
func bdHooks(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"hooks"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd hooks %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func TestEmbeddedHooks(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tk")

	t.Run("hooks_install", func(t *testing.T) {
		out := bdHooks(t, bd, dir, "install")
		_ = out // Should succeed without error
	})

	t.Run("hooks_list", func(t *testing.T) {
		out := bdHooks(t, bd, dir, "list")
		if len(strings.TrimSpace(out)) == 0 {
			t.Error("expected non-empty hooks list output")
		}
	})

	// beads-nbhp: bd hooks list --json inner hook objects must use snake_case
	// keys (name/installed/version/is_shim/outdated) from the HookStatus json
	// tags — not the raw Go PascalCase field names (Name/Installed/IsShim...),
	// which broke the house style. field-casing class (jyaw/8slh/7mm8).
	t.Run("hooks_list_json_snake_case", func(t *testing.T) {
		out := bdHooks(t, bd, dir, "list", "--json")
		s := strings.TrimSpace(out)
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("hooks list --json: no JSON object in output:\n%s", out)
		}
		var doc struct {
			Hooks []map[string]interface{} `json:"hooks"`
		}
		if err := json.Unmarshal([]byte(s[start:]), &doc); err != nil {
			t.Fatalf("hooks list --json: unparseable: %v\n%s", err, out)
		}
		if len(doc.Hooks) == 0 {
			t.Fatalf("hooks list --json: expected at least one hook object:\n%s", out)
		}
		h := doc.Hooks[0]
		for _, want := range []string{"name", "installed", "version", "is_shim", "outdated"} {
			if _, ok := h[want]; !ok {
				t.Errorf("hooks list --json inner object missing snake_case key %q: %v", want, h)
			}
		}
		for _, bad := range []string{"Name", "Installed", "Version", "IsShim", "Outdated"} {
			if _, ok := h[bad]; ok {
				t.Errorf("hooks list --json inner object still leaks PascalCase key %q (nbhp regression): %v", bad, h)
			}
		}
	})

	t.Run("hooks_uninstall", func(t *testing.T) {
		out := bdHooks(t, bd, dir, "uninstall")
		_ = out // Should succeed without error

		// List after uninstall should still work
		bdHooks(t, bd, dir, "list")
	})
}

func TestEmbeddedHooksConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hk")

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
			cmd := exec.Command(bd, "hooks", "list")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("hooks list (worker %d): %v\n%s", worker, err, out)
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
}
