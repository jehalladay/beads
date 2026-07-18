//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// beads-0wp9: `bd statuses --json` / `bd types --json` are documented --json
// commands, but each has an ensureDirectMode() guard whose failure returned a
// plain HandleError BEFORE the `if jsonOutput` success block. In proxied-server
// mode (a live config — these commands "require direct database access") that
// left stdout empty + stderr text, so a --json consumer could not parse the
// failure. The fix routes the guard failure through HandleErrorRespectJSON so
// stdout carries a JSON {error} object (xwjg/8lqh --json-error-contract class).
func TestProxiedServerStatusesTypesJSONError(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// Both commands fail ensureDirectMode in proxied-server mode. Assert stdout
	// is a parseable JSON {error} object rather than empty.
	for _, cmd := range []string{"statuses", "types"} {
		cmd := cmd
		t.Run(cmd, func(t *testing.T) {
			p := bdProxiedInit(t, bd, "wpt"+cmd[:1])
			stdout, stderr, runErr := bdProxiedRunBuffers(t, bd, p.dir, cmd, "--json")
			if runErr == nil {
				t.Fatalf("expected bd %s --json to fail in proxied mode; stdout:\n%s", cmd, stdout)
			}
			s := strings.TrimSpace(stdout)
			if s == "" {
				t.Fatalf("bd %s --json emitted empty stdout on failure — must be a JSON {error} object (beads-0wp9); stderr:\n%s", cmd, stderr)
			}
			var obj map[string]any
			if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
				t.Fatalf("bd %s --json failure stdout is not a JSON object: %v\nstdout:\n%s", cmd, jerr, s)
			}
			if _, ok := obj["error"]; !ok {
				t.Errorf("bd %s --json failure stdout has no \"error\" field: %s", cmd, s)
			}
		})
	}
}
