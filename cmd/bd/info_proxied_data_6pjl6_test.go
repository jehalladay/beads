//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerInfoData proves `bd info` returns real DATA for hub-connected
// (proxied-server) crew (beads-6pjl6):
//
// beads-28ai fixed the reported MODE (resolveInfoMode → "proxied-server"), but
// the issue_count + config blocks were still gated behind `if store != nil`. In
// proxiedServerMode the global `store` is nil, so a hub crew saw the correct
// mode label but a ZERO issue_count and NO config — a populated hub DB looked
// empty. The fix routes those reads through the proxied UOW (infoSearchIssues /
// infoAllConfig / infoConfigValue in info_proxied_server.go), same read-
// divergence class as `bd orphans` (beads-ktlo).
//
// Mutation-verify RED: strip the usesProxiedServer() branch from infoSearchIssues
// (so it falls to `store == nil` → nil, false) → issue_count absent / zero, and
// from infoAllConfig → config absent. This test then fails.
func TestProxiedServerInfoData(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("issue_count_and_config", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pinf")
		bdProxiedCreate(t, bd, p.dir, "one", "--type", "task")
		bdProxiedCreate(t, bd, p.dir, "two", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "info", "--json")
		if err != nil {
			t.Fatalf("bd info --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd info hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}

		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("no JSON object in info output:\n%s", stdout)
		}
		var out struct {
			Mode       string            `json:"mode"`
			IssueCount *int              `json:"issue_count"`
			Config     map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(stdout[start:]), &out); err != nil {
			t.Fatalf("parse info JSON: %v\nraw: %s", err, stdout[start:])
		}

		if out.Mode != "proxied-server" {
			t.Errorf("expected mode proxied-server, got %q", out.Mode)
		}
		if out.IssueCount == nil {
			t.Fatalf("issue_count missing from proxied info output (regressed to store-nil gate):\n%s", stdout)
		}
		if *out.IssueCount != 2 {
			t.Errorf("expected issue_count 2, got %d", *out.IssueCount)
		}
		if len(out.Config) == 0 {
			t.Errorf("expected non-empty config map for proxied crew, got %v\n%s", out.Config, stdout)
		}
	})

	t.Run("schema_samples", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "psch")
		bdProxiedCreate(t, bd, p.dir, "alpha", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "info", "--schema", "--json")
		if err != nil {
			t.Fatalf("bd info --schema --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("no JSON object in info --schema output:\n%s", stdout)
		}
		var out struct {
			Schema struct {
				SampleIssueIDs []string `json:"sample_issue_ids"`
				DetectedPrefix string   `json:"detected_prefix"`
			} `json:"schema"`
		}
		if err := json.Unmarshal([]byte(stdout[start:]), &out); err != nil {
			t.Fatalf("parse info --schema JSON: %v\nraw: %s", err, stdout[start:])
		}
		if len(out.Schema.SampleIssueIDs) == 0 {
			t.Errorf("expected sample_issue_ids for proxied crew (regressed to store-nil gate):\n%s", stdout)
		}
		if out.Schema.DetectedPrefix != "psch" {
			t.Errorf("expected detected_prefix psch, got %q", out.Schema.DetectedPrefix)
		}
	})
}
