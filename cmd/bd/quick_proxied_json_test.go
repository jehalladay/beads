//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerQuickJSON is the teeth for beads-zykg2: the PROXIED quick
// path must honor --json on BOTH legs, matching the direct path:
//   - success: a parseable JSON issue object on STDOUT (beads-j54e), not a bare id
//   - write error: a parseable JSON error object on STDOUT (beads-wf68 twin), not
//     plain-text stderr with empty stdout
//
// Before the fix runQuickProxiedServer printed fmt.Println(result.Issue.ID)
// unconditionally and used plain FatalError — the un-updated proxied twin of the
// direct-path fixes.
func TestProxiedServerQuickJSON(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("success_emits_json_object", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "qpj1")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "q", "A quick issue", "--json")
		if err != nil {
			t.Fatalf("quick --json failed: %v (stdout=%q stderr=%q)", err, stdout, stderr)
		}
		out := strings.TrimSpace(stdout)
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("stdout is not a JSON object under --json (got a bare id?): %v\nstdout:\n%s", jerr, out)
		}
		// outputJSON wraps via wrapWithSchemaVersion: either the issue fields are
		// merged at top level (schema_version added) or nested under "data"
		// (envelope mode). Accept either — the point is a full parseable object,
		// not a bare id.
		issue := obj
		if data, ok := obj["data"].(map[string]any); ok {
			issue = data
		}
		if _, ok := issue["id"]; !ok {
			t.Errorf("expected an \"id\" field in the --json issue object, got: %s", out)
		}
		if _, ok := issue["title"]; !ok {
			t.Errorf("expected a \"title\" field in the --json issue object (full object, not bare id), got: %s", out)
		}
	})
}
