//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// bdRename runs "bd rename" with the given args and returns stdout.
func bdRename(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"rename"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd rename %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdRenameFail runs "bd rename" expecting failure; returns combined output.
func bdRenameFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"rename"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd rename %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// beads-s7ey: `bd rename --json` previously emitted plain text on BOTH success
// and error, silently ignoring the global --json flag. It must emit a JSON
// success payload and route errors through the JSON error contract.
func TestEmbeddedRenameJSON(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	t.Run("rename_json_success_emits_json", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rj")
		issue := bdCreate(t, bd, dir, "Rename JSON A", "--type", "task")
		newID := "rj-renamed1"
		out := bdRename(t, bd, dir, "--json", issue.ID, newID)
		s := strings.TrimSpace(out)
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("expected a JSON object on rename --json success, got plain text: %q", out)
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(s[start:]), &m); err != nil {
			t.Fatalf("rename --json success output is not valid JSON: %v\n%s", err, out)
		}
		if m["renamed"] != true {
			t.Errorf("expected renamed=true, got %v", m["renamed"])
		}
		if m["old_id"] != issue.ID || m["new_id"] != newID {
			t.Errorf("expected old_id=%s new_id=%s, got %v", issue.ID, newID, m)
		}
	})

	t.Run("rename_json_error_emits_json", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "re")
		// rename a nonexistent issue under --json → must be a JSON error object,
		// not plain "Error: ..." text.
		out := bdRenameFail(t, bd, dir, "--json", "re-doesnotexist", "re-newid")
		s := strings.TrimSpace(out)
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("expected a JSON error object on rename --json failure, got plain text: %q", out)
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(s[start:]), &m); err != nil {
			t.Fatalf("rename --json error output is not valid JSON: %v\n%s", err, out)
		}
		if _, ok := m["error"]; !ok {
			t.Errorf("expected an 'error' field in JSON error output, got: %v", m)
		}
	})
}
