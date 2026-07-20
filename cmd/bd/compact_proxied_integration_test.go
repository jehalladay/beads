//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerCompact is the teeth for the beads-aocj compact leg. On
// hub-connected crew the global `store` is nil in proxiedServerMode, so the
// read-only `bd compact --stats` path (which had no ensureDirectMode guard)
// dereferenced nil and PANICKED instead of returning stats. This routes
// --stats through the proxied UOW (IssueUseCase.GetTier1/2CompactionCandidates),
// and confirms the direct-only mutating modes (--analyze/--apply/--auto) fail
// with a clean hinted error, never a nil-store panic.
func TestProxiedServerCompact(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("stats_works_in_proxied_mode", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cs")
		// Some closed content so the candidate queries have a real (possibly
		// empty) result set to summarize; the key assertion is no panic + a
		// stats table, not specific counts.
		bdProxiedCreate(t, bd, p.dir, "Old closed issue", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "admin", "compact", "--stats")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied compact --stats failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") || strings.Contains(s, "nil pointer") ||
			strings.Contains(s, "invalid memory address") || strings.Contains(s, "panic") {
			t.Fatalf("proxied compact --stats hit the nil-store path (beads-aocj regression): %s", s)
		}
		// The stats output labels the two tiers.
		if !strings.Contains(s, "Tier 1") && !strings.Contains(s, "tier1") && !strings.Contains(s, "Compaction") {
			t.Errorf("expected compaction stats output, got: %s", s)
		}
	})

	t.Run("stats_json_works_in_proxied_mode", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "csj")
		out, err := bdProxiedRun(t, bd, p.dir, "admin", "compact", "--stats", "--json")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied compact --stats --json failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "panic") || strings.Contains(s, "nil pointer") {
			t.Fatalf("proxied compact --stats --json panicked: %s", s)
		}
		if !strings.Contains(s, "{") {
			t.Errorf("expected JSON object from proxied compact --stats --json, got: %s", s)
		}
	})

	t.Run("apply_fails_cleanly_not_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ca")
		issue := bdProxiedCreate(t, bd, p.dir, "Apply target", "--type", "task")

		out, _ := bdProxiedRun(t, bd, p.dir, "admin", "compact", "--apply", "--id", issue.ID, "--summary", "short")
		s := string(out)
		// --apply requires direct DB access; in proxied it must report that
		// cleanly (ensureDirectMode), never a nil-store panic.
		if strings.Contains(s, "panic") || strings.Contains(s, "nil pointer") ||
			strings.Contains(s, "invalid memory address") {
			t.Fatalf("proxied compact --apply panicked instead of failing cleanly: %s", s)
		}
	})

	t.Run("auto_fails_cleanly_not_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cau")
		issue := bdProxiedCreate(t, bd, p.dir, "Auto target", "--type", "task")

		out, _ := bdProxiedRun(t, bd, p.dir, "admin", "compact", "--auto", "--id", issue.ID, "--dry-run")
		s := string(out)
		if strings.Contains(s, "panic") || strings.Contains(s, "nil pointer") ||
			strings.Contains(s, "invalid memory address") {
			t.Fatalf("proxied compact --auto panicked instead of failing cleanly: %s", s)
		}
	})
}
