//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerUpdateJSONStderrError is the teeth for the beads-vuyx
// update-leg: on a per-item failure under --json, the proxied update handler
// must emit a STRUCTURED JSON error object to STDERR (fg6 contract: stdout
// stays a pure success payload, stderr carries one JSON error per failed id),
// matching the direct update path (update.go reportItemError). Before the fix
// applyUpdateProxiedOne hardcoded plain-text fmt.Fprintf(os.Stderr,...), so a
// --json consumer parsing stderr for per-item errors got an unparseable line.
func TestProxiedServerUpdateJSONStderrError(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("ghost_id_error_is_json_on_stderr", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ujse")
		good := bdProxiedCreate(t, bd, p.dir, "Good one", "--type", "task")

		// Partial batch: one good id + one ghost, under --json.
		stdout, stderr, err := bdProxiedUpdateRaw(t, bd, p.dir, good.ID, "ghost-9999", "--assignee", "alice", "--json")
		if err == nil {
			t.Fatalf("partial update with a ghost id should exit non-zero; stdout=%q stderr=%q", stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("hit the nil-store path: %s / %s", stdout, stderr)
		}

		// The per-item error must be a parseable JSON object on STDERR (not a
		// bare plain-text line).
		se := strings.TrimSpace(stderr)
		if se == "" {
			t.Fatalf("stderr empty — expected a JSON per-item error object (beads-vuyx); stdout=%q", stdout)
		}
		// stderr may carry more than one JSON object; assert the first parses
		// and has an "error" field.
		dec := json.NewDecoder(strings.NewReader(se))
		var obj map[string]any
		if jerr := dec.Decode(&obj); jerr != nil {
			t.Fatalf("stderr per-item error is not a JSON object (beads-vuyx): %v\nstderr:\n%s", jerr, se)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("expected an \"error\" field in the --json stderr object, got: %s", se)
		}

		// The good id must still have been updated (partial success), and stdout
		// must stay a pure JSON success payload (no plain-text contamination).
		got := bdProxiedShow(t, bd, p.dir, good.ID)
		if got.Assignee != "alice" {
			t.Errorf("good id should still be updated on a partial batch, assignee=%q want alice", got.Assignee)
		}
		if so := strings.TrimSpace(stdout); so != "" {
			var any0 any
			if jerr := json.Unmarshal([]byte(so), &any0); jerr != nil {
				t.Errorf("stdout must stay parseable JSON on a partial --json update, got: %s", so)
			}
		}
	})
}
