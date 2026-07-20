//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedCreateDueDeferReadAfterWrite_82pv3 is the PROXIED twin of
// beads-17n4h. 17n4h truncated due_at/defer_until (+ created_at/updated_at) to
// second precision in issueops.PrepareIssueForInsert — the DIRECT/embedded
// create path. The DOMAIN create path (used by proxied-server `bd create`) does
// NOT call PrepareIssueForInsert; it went through db/issue.go
// normalizeIssueTimestamps (which only .UTC()'d created/updated) + issueRepo.Insert
// (due_at/defer_until inserted verbatim). Proxied create then emits the in-memory
// result.Issue under --json, so a relative `--due/--defer` (ParseRelativeTime
// carries ns from time.Now()) emitted NANOSECOND precision while every later read
// returned the second-truncated DATETIME column — a read-after-write mismatch on
// the uncovered proxied twin. The fix truncates in normalizeIssueTimestamps (the
// shared domain insert chokepoint).
//
// Drives the real proxied binary: create --json (relative --due/--defer), capture
// the emitted due_at/defer_until, show --json, assert byte-identical.
func TestProxiedCreateDueDeferReadAfterWrite_82pv3(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pdd")

	createOut, createErr, err := bdProxiedRunBuffers(t, bd, p.dir, "create",
		"due/defer read-after-write proxied probe", "--type", "task",
		"--due", "+6h", "--defer", "+3h", "--json")
	if err != nil {
		t.Fatalf("proxied `bd create --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, createOut, createErr)
	}
	created := parseJSONObject(t, "create", createOut)
	id, _ := created["id"].(string)
	createDueAt, _ := created["due_at"].(string)
	createDeferUntil, _ := created["defer_until"].(string)
	if id == "" || createDueAt == "" || createDeferUntil == "" {
		t.Fatalf("create-emit missing id/due_at/defer_until: %s", createOut)
	}

	showOut, showErr, err := bdProxiedRunBuffers(t, bd, p.dir, "show", id, "--json")
	if err != nil {
		t.Fatalf("proxied `bd show <id> --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, showOut, showErr)
	}
	shown := firstShownIssue(t, showOut)
	showDueAt, _ := shown["due_at"].(string)
	showDeferUntil, _ := shown["defer_until"].(string)

	if createDueAt != showDueAt {
		t.Errorf("read-after-write due_at mismatch (beads-82pv3):\n  create-emit: %q\n  show-read:   %q\n(same issue %s; proxied create-emit must be second-truncated to match the persisted column)", createDueAt, showDueAt, id)
	}
	if createDeferUntil != showDeferUntil {
		t.Errorf("read-after-write defer_until mismatch (beads-82pv3):\n  create-emit: %q\n  show-read:   %q\n(same issue %s)", createDeferUntil, showDeferUntil, id)
	}
}

// parseJSONObject extracts the first JSON object from proxied output (which may
// carry an envelope wrapper: {schema_version, ...fields} or {data:{...}}).
func parseJSONObject(t *testing.T, label, out string) map[string]interface{} {
	t.Helper()
	s := strings.TrimSpace(out)
	brace := strings.Index(s, "{")
	if brace < 0 {
		t.Fatalf("%s: no JSON object in output: %s", label, out)
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(s[brace:]), &obj); jerr != nil {
		t.Fatalf("%s: parse JSON: %v\n%s", label, jerr, out)
	}
	if data, ok := obj["data"].(map[string]interface{}); ok {
		return data
	}
	return obj
}

// firstShownIssue parses `bd show --json` output (a JSON array of issues, possibly
// enveloped) and returns the first element.
func firstShownIssue(t *testing.T, out string) map[string]interface{} {
	t.Helper()
	s := strings.TrimSpace(out)
	// Prefer an array; fall back to a data-wrapped array.
	if b := strings.Index(s, "["); b >= 0 {
		var list []map[string]interface{}
		if jerr := json.Unmarshal([]byte(s[b:]), &list); jerr == nil && len(list) > 0 {
			return list[0]
		}
	}
	obj := parseJSONObject(t, "show", out)
	if arr, ok := obj["data"].([]interface{}); ok && len(arr) > 0 {
		if m, ok := arr[0].(map[string]interface{}); ok {
			return m
		}
	}
	// show may emit a single object under some shapes.
	if _, ok := obj["due_at"]; ok {
		return obj
	}
	t.Fatalf("show: could not extract an issue from output: %s", out)
	return nil
}
