//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// beads-5fu1: bd kv (set/get/clear/list) and the memory commands
// (remember/memories/forget/recall) are documented --json commands (each honors
// jsonOutput on its success path), but each has an ensureDirectMode() guard whose
// failure returned a plain HandleError BEFORE the `if jsonOutput` block. In
// server/proxied-server mode (a live config — all "require direct database
// access") that left stdout empty + stderr text, so a --json consumer could not
// parse the failure. The fix routes each guard failure through
// HandleErrorRespectJSON (exact 0wp9/y2yo/xwjg/8lqh --json-error-contract class).
func TestProxiedServerKVMemoryJSONError(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// Each of these fails ensureDirectMode in proxied-server mode. Assert stdout
	// is a parseable JSON {error} object rather than empty. Commands that take a
	// positional arg get a throwaway one — the guard fires before arg use.
	cases := []struct {
		name string
		args []string
	}{
		{"kv_set", []string{"kv", "set", "k", "v"}},
		{"kv_get", []string{"kv", "get", "k"}},
		{"kv_clear", []string{"kv", "clear", "k"}},
		{"kv_list", []string{"kv", "list"}},
		{"remember", []string{"remember", "some note"}},
		{"memories", []string{"memories"}},
		{"forget", []string{"forget", "somekey"}},
		{"recall", []string{"recall", "query"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := bdProxiedInit(t, bd, "km"+tc.name[:1])
			args := append(append([]string{}, tc.args...), "--json")
			stdout, stderr, runErr := bdProxiedRunBuffers(t, bd, p.dir, args...)
			if runErr == nil {
				t.Fatalf("expected `bd %s --json` to fail in proxied mode; stdout:\n%s", strings.Join(tc.args, " "), stdout)
			}
			s := strings.TrimSpace(stdout)
			if s == "" {
				t.Fatalf("`bd %s --json` emitted empty stdout on failure — must be a JSON {error} object (beads-5fu1); stderr:\n%s", strings.Join(tc.args, " "), stderr)
			}
			var obj map[string]any
			if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
				t.Fatalf("`bd %s --json` failure stdout is not a JSON object: %v\nstdout:\n%s", strings.Join(tc.args, " "), jerr, s)
			}
			if _, ok := obj["error"]; !ok {
				t.Errorf("`bd %s --json` failure stdout has no \"error\" field: %s", strings.Join(tc.args, " "), s)
			}
		})
	}
}
