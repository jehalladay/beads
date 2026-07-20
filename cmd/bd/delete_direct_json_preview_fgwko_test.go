//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// beads-fgwko: the DIRECT-path delete preview leg (bd delete <id> without
// --force, no proxied server) ignored --json and unconditionally printed the
// human "⚠️  DELETE PREVIEW" plaintext with rc=0, so a --json consumer got
// unparseable output. Its own proxied twin (runDeleteProxiedPreview) already
// honors --json. This end-to-end test drives the real bd binary in DIRECT mode
// (the embedded harness runs no proxied server) and asserts the preview leg
// now emits the twin's JSON envelope instead of plaintext.
func TestDeleteDirectPreview_json_fgwko(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// A blocker with an inbound dependent, so the preview has a non-zero
	// dependencies_removed count (exercises the dry-run count plumbing).
	blocker := bdCreateSilent(t, bd, dir, "Blocker", "--type", "task")
	dependent := bdCreateSilent(t, bd, dir, "Dependent", "--type", "task")
	if out, err := bdRunWithFlockRetry(t, bd, dir, "dep", "add", dependent, blocker); err != nil {
		t.Fatalf("dep add failed: %v\n%s", err, out)
	}

	out, err := bdRunWithFlockRetry(t, bd, dir, "delete", blocker, "--json")
	if err != nil {
		t.Fatalf("delete --json preview failed: %v\n%s", err, out)
	}
	s := string(out)

	// It must NOT leak the human preview plaintext.
	if strings.Contains(s, "DELETE PREVIEW") || strings.Contains(s, "This operation cannot be undone") {
		t.Fatalf("delete --json preview leaked human plaintext: %q", s)
	}

	// It must be a single parseable JSON object matching the proxied twin's
	// envelope keys.
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("no JSON object in preview output: %q", s)
	}
	var env struct {
		WouldDelete         string   `json:"would_delete"`
		DependenciesRemoved int      `json:"dependencies_removed"`
		ReferencesUpdated   int      `json:"references_updated"`
		Connected           []string `json:"connected"`
		DryRun              bool     `json:"dry_run"`
	}
	if derr := json.Unmarshal([]byte(s[start:]), &env); derr != nil {
		t.Fatalf("preview output is not valid JSON: %v\nraw: %q", derr, s[start:])
	}

	if env.WouldDelete != blocker {
		t.Errorf("would_delete = %q, want %q", env.WouldDelete, blocker)
	}
	if env.DependenciesRemoved < 1 {
		t.Errorf("dependencies_removed = %d, want >= 1 (the inbound dependent)", env.DependenciesRemoved)
	}
	if !env.DryRun {
		t.Errorf("dry_run = false, want true (this is a preview)")
	}

	// It is a PREVIEW: the issue must still exist afterward (dry-run, no write).
	showOut, showErr := bdRunWithFlockRetry(t, bd, dir, "show", blocker, "--json")
	if showErr != nil {
		t.Fatalf("preview must not delete the issue, but show failed: %v\n%s", showErr, showOut)
	}
}
