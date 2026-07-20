//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedServerReopenJSONContract_efyts proves the proxied `bd reopen --json`
// handler mirrors the DIRECT path's --json contract on the two legs beads-j43d
// did not cover (beads-efyts, aocj/proxied-twin-lag class):
//
//  1. ALREADY-OPEN NO-OP: a reopen of an already-open issue is an idempotent
//     success (rc0). The direct path (reopen.go, beads-hxc2) reflects the current
//     state into the --json array; the proxied handler previously returned
//     reopened:false which the batch loop skipped → EMPTY stdout under --json.
//     Fixed: reflect it into the reopened array (alreadyOpen leg).
//  2. PER-ITEM ERRORS: not-found + the guard-fail legs (closed-epic-parent /
//     supersede / duplicate) emitted bare fmt.Fprintf(os.Stderr,...) plaintext
//     regardless of --json (the en28/fg6 stderr-interleave twin). Fixed: route
//     through a deferred reporter that, under --json, flushes JSON error objects
//     to stderr on partial/no-op success and stays clean on a wholly-failed batch
//     (where j43d's terminal stdout JSON error is the sole error).
//
// The mutation-verify discriminator for leg 1 is that stdout is a non-empty JSON
// array reflecting the already-open issue (neuter the alreadyOpen reflect → RED
// empty stdout). For leg 2 it is that a per-item failure under --json produces a
// parseable JSON error object, not bare plaintext (neuter the reporter routing →
// RED bare plaintext). Regression guards assert the untouched legs (a real
// reopen success array, j43d's wholly-failed stdout error) still hold.
func TestProxiedServerReopenJSONContract_efyts(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// parseJSONArray parses the leading JSON array from stdout (the reopened
	// issues payload). Fails if none is present.
	parseJSONArray := func(t *testing.T, stdout string) []*types.Issue {
		t.Helper()
		start := strings.Index(stdout, "[")
		if start < 0 {
			t.Fatalf("beads-efyts: expected a JSON array on stdout, got:\n%s", stdout)
		}
		var issues []*types.Issue
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &issues); err != nil {
			t.Fatalf("beads-efyts: stdout is not a parseable JSON array: %v\nraw:\n%s", err, stdout[start:])
		}
		return issues
	}

	// assertJSONErrorObject asserts s contains exactly one parseable JSON error
	// object with a non-empty "error" string, and no bare plaintext leaked before
	// it (the en28/fg6 contract). Returns the error message.
	assertJSONErrorObject := func(t *testing.T, label, s string) string {
		t.Helper()
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("beads-efyts: %s expected a JSON error object, got bare/empty:\n%s", label, s)
		}
		// Nothing but whitespace should precede the JSON object — a bare plaintext
		// line before it is exactly the pre-fix regression.
		if pre := strings.TrimSpace(s[:start]); pre != "" {
			t.Errorf("beads-efyts: %s leaked plaintext before the JSON error object (en28/fg6 twin): %q", label, pre)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(s[start:])), &obj); err != nil {
			t.Fatalf("beads-efyts: %s error stream is not a parseable JSON object: %v\nraw:\n%s", label, err, s[start:])
		}
		// The error may be nested under data{} when the JSON envelope is on.
		msg, _ := obj["error"].(string)
		if msg == "" {
			if data, ok := obj["data"].(map[string]interface{}); ok {
				msg, _ = data["error"].(string)
			}
		}
		if msg == "" {
			t.Errorf("beads-efyts: %s expected a non-empty 'error' field, got: %v", label, obj)
		}
		return msg
	}

	// LEG 1: already-open reopen under --json reflects the issue (not empty stdout).
	t.Run("already_open_noop_reflects_issue_array", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "eao")
		iss := bdProxiedCreate(t, bd, p.dir, "efyts already-open target")
		// Do NOT close it — it is already open, so reopen is an idempotent no-op.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "reopen", iss.ID, "--json")
		if err != nil {
			t.Fatalf("beads-efyts: already-open reopen --json should succeed (rc0 no-op), got err=%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.TrimSpace(stdout) == "" {
			t.Fatalf("beads-efyts: already-open reopen --json emitted EMPTY stdout (the un-mirrored hxc2 twin) — expected a reflected issue array\nstderr:\n%s", stderr)
		}
		issues := parseJSONArray(t, stdout)
		if len(issues) != 1 || issues[0].ID != iss.ID {
			t.Fatalf("beads-efyts: expected the already-open issue %s reflected in the array, got: %+v", iss.ID, issues)
		}
		if issues[0].Status != types.StatusOpen {
			t.Errorf("beads-efyts: reflected issue status = %q, want open", issues[0].Status)
		}
	})

	// LEG 2a: not-found id under --json → JSON error object on stdout (wholly
	// failed batch, j43d's terminal error covers it), not bare stderr.
	t.Run("not_found_json_error_object", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "enf")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "reopen", "nope-does-not-exist", "--json")
		if err == nil {
			t.Fatalf("beads-efyts: reopen of a not-found id --json must fail non-zero, got success\nstdout:\n%s", stdout)
		}
		if strings.Contains(stdout+stderr, "panic") {
			t.Fatalf("beads-efyts: not-found reopen --json panicked:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		// Wholly-failed batch: j43d emits the terminal error on stdout.
		msg := assertJSONErrorObject(t, "not-found", stdout)
		if msg == "" {
			t.Errorf("beads-efyts: not-found expected a non-empty JSON error message")
		}
	})

	// LEG 2b: PARTIAL success — one real reopen + one not-found id under --json.
	// stdout carries the reopened array (j43d keeps the plain exit on partial),
	// and the per-item not-found failure flushes as a JSON error object on
	// STDERR, never bare plaintext.
	t.Run("partial_success_per_item_error_is_json_on_stderr", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "eps")
		good := bdProxiedCreate(t, bd, p.dir, "efyts partial-good")
		bdProxiedClose(t, bd, p.dir, good.ID)
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "reopen", good.ID, "missing-id", "--json")
		// Partial success keeps the plain non-zero exit (hasError set by the
		// missing id) — err is expected here.
		if err == nil {
			t.Fatalf("beads-efyts: partial batch should exit non-zero (one id failed), got success\nstdout:\n%s", stdout)
		}
		if strings.Contains(stdout+stderr, "panic") {
			t.Fatalf("beads-efyts: partial reopen --json panicked:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		// stdout carries the reopened array for the good id.
		issues := parseJSONArray(t, stdout)
		if len(issues) != 1 || issues[0].ID != good.ID {
			t.Fatalf("beads-efyts: expected the reopened id %s on stdout, got: %+v", good.ID, issues)
		}
		// The per-item failure for the missing id must be a JSON error object on
		// stderr, NOT bare plaintext (the pre-fix regression). The reporter uses
		// jsonStderrError → an "error"-keyed object.
		if strings.TrimSpace(stderr) == "" {
			t.Fatalf("beads-efyts: expected a per-item JSON error on stderr for the missing id, got empty stderr")
		}
		assertJSONErrorObject(t, "partial-per-item", stderr)
		// And the bare pre-fix wording must be gone: a raw "Issue missing-id not
		// found\n" plaintext line (no JSON braces) is the RED tell.
		if !strings.Contains(stderr, "{") {
			t.Errorf("beads-efyts: per-item error on stderr is bare plaintext, not JSON (en28/fg6 twin):\n%s", stderr)
		}
	})

	// LEG 2c: guard-fail (supersede) under --json on a WHOLLY-failed batch →
	// terminal JSON error on stdout, and the per-item guard message does not leak
	// as bare plaintext. Exercises the closed-epic/supersede/duplicate guard
	// routing through the reporter.
	t.Run("supersede_guard_wholly_failed_json_error", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "esg")
		canonical := bdProxiedCreate(t, bd, p.dir, "efyts supersede canonical")
		old := bdProxiedCreate(t, bd, p.dir, "efyts supersede old")
		// `bd supersede old --with new` closes old + adds the supersedes edge.
		if _, _, err := bdProxiedRunBuffers(t, bd, p.dir, "supersede", old.ID, "--with", canonical.ID); err != nil {
			t.Skipf("beads-efyts: could not set up supersede edge in proxied mode (setup, not the SUT): %v", err)
		}
		// Reopening old (without --force) must be refused by the supersede guard.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "reopen", old.ID, "--json")
		if err == nil {
			t.Fatalf("beads-efyts: reopen of a superseded id --json must fail (guard), got success\nstdout:\n%s", stdout)
		}
		if strings.Contains(stdout+stderr, "panic") {
			t.Fatalf("beads-efyts: supersede-guard reopen --json panicked:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		// Wholly failed → terminal JSON error on stdout (j43d). The guard message
		// must not leak as bare plaintext on stderr.
		assertJSONErrorObject(t, "supersede-guard", stdout)
		if pre := strings.TrimSpace(stderr); pre != "" && !strings.Contains(pre, "{") {
			t.Errorf("beads-efyts: supersede guard leaked bare plaintext on stderr under --json: %q", pre)
		}
	})

	// REGRESSION GUARD: a real reopen success still emits the reopened array
	// (the untouched happy path).
	t.Run("real_reopen_success_array_unchanged", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ers")
		iss := bdProxiedCreate(t, bd, p.dir, "efyts real reopen")
		bdProxiedClose(t, bd, p.dir, iss.ID)
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "reopen", iss.ID, "--json")
		if err != nil {
			t.Fatalf("beads-efyts: real reopen --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		issues := parseJSONArray(t, stdout)
		if len(issues) != 1 || issues[0].ID != iss.ID || issues[0].Status != types.StatusOpen {
			t.Fatalf("beads-efyts: expected reopened %s (open) in the array, got: %+v", iss.ID, issues)
		}
	})
}
