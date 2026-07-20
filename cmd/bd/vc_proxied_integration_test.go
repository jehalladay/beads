//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerVC proves bd vc {merge,commit,status} no longer nil-panic in
// proxied-server mode (beads-6iwwf, aocj proxied-routing class / VCS leg).
//
// vc.go is NOT in noDbCommands and every subcommand used the nil global `store`
// (Merge/Commit/CommitMergeResolution/ResolveConflicts/GetCurrentCommit/
// CurrentBranch/Status) in proxiedServerMode → nil panic (sibling of the
// beads-jr2h4 branch and beads-i2v77 merge-slot repros).
//
// There is no proxied/UOW version-control path, and the store factory refuses
// to open a direct store in proxied config. So, like `bd branch` /
// `bd merge-slot` / `compact --analyze`, the correct behavior in proxied-server
// mode is to FAIL LOUD with a clear message (converting the panic to a clean,
// --json-contract-correct error), NOT to succeed. This test asserts that
// fail-loud contract on all three subcommands.
func TestProxiedServerVC(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	assertCleanProxiedRefusal := func(t *testing.T, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-6iwwf: bd vc unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", stdout)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "panic") ||
			strings.Contains(combined, "nil pointer") ||
			strings.Contains(combined, "invalid memory address") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("beads-6iwwf: bd vc PANICKED/nil-crashed in proxied mode (should be a clean fail-loud error):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(combined, "proxied-server mode") {
			t.Errorf("beads-6iwwf: expected the purpose-built 'not available in proxied-server mode' guard message, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	}

	// argv per subcommand (merge needs ExactArgs(1); commit uses -m to bypass the
	// message-required path — the guard runs first regardless).
	cases := []struct {
		name string
		args []string
	}{
		{"vc_merge", []string{"vc", "merge", "some-branch"}},
		{"vc_commit", []string{"vc", "commit", "-m", "msg"}},
		{"vc_status", []string{"vc", "status"}},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name+"_refuses_cleanly", func(t *testing.T) {
			p := bdProxiedInit(t, bd, "v"+c.name[3:6])
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, c.args...)
			assertCleanProxiedRefusal(t, stdout, stderr, err)
		})
	}

	// --json must emit a single JSON error OBJECT on stdout (the 8lqh contract),
	// not plaintext and not a panic backtrace. Assert on every subcommand.
	for _, c := range cases {
		c := c
		t.Run(c.name+"_json_error_contract", func(t *testing.T) {
			p := bdProxiedInit(t, bd, "j"+c.name[3:6])
			args := append(append([]string{}, c.args...), "--json")
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, args...)
			if err == nil {
				t.Fatalf("beads-6iwwf: bd %s --json unexpectedly SUCCEEDED in proxied mode:\nstdout:\n%s", c.name, stdout)
			}
			if strings.Contains(stdout+stderr, "panic") || strings.Contains(stdout+stderr, "storage is nil") {
				t.Fatalf("beads-6iwwf: bd %s --json panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", c.name, stdout, stderr)
			}
			start := strings.Index(stdout, "{")
			if start < 0 {
				t.Fatalf("beads-6iwwf: --json error must be a JSON object on stdout, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &obj); jerr != nil {
				t.Fatalf("beads-6iwwf: stdout is not a parseable JSON object: %v\nraw:\n%s", jerr, stdout[start:])
			}
			msg, ok := obj["error"].(string)
			if !ok || msg == "" {
				t.Errorf("beads-6iwwf: expected a non-empty 'error' field in the --json error doc, got: %v", obj)
			}
			if !strings.Contains(msg, "proxied-server mode") {
				t.Errorf("beads-6iwwf: expected the proxied guard message in the JSON error, got: %q", msg)
			}
		})
	}
}
