//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMetricsJSONContract pins beads-14xu: `bd metrics` (status), `bd metrics
// on|off|example` accept the global --json flag but previously ignored it
// (emitting human prose to stdout, exit 0) — the silently-ignored-flag /
// json-contract class (sibling of kvmg). Each --json invocation must emit a
// parseable JSON object on stdout. Status is the bare `bd metrics` command
// (beads-3l5q made a stray `status` positional an unknown-subcommand error).
func TestMetricsJSONContract(t *testing.T) {
	bd := buildEmbeddedBD(t)
	home, err := testTempDir("bd-metrics-json-home-*")
	if err != nil {
		t.Fatalf("temp home: %v", err)
	}
	repo, err := testTempDir("bd-metrics-json-repo-*")
	if err != nil {
		t.Fatalf("temp repo: %v", err)
	}
	initGitRepoAt(t, repo)

	parseObj := func(t *testing.T, label, out string) map[string]any {
		t.Helper()
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &obj); jerr != nil {
			t.Fatalf("%s --json should emit a JSON object on stdout, got %q: %v", label, out, jerr)
		}
		return obj
	}

	t.Run("status", func(t *testing.T) {
		// Status is the bare `bd metrics` command; a `status` positional is
		// rejected as an unknown subcommand (beads-3l5q).
		out, _ := runBdForMetrics(t, bd, repo, home, "metrics", "--json")
		obj := parseObj(t, "metrics", out)
		for _, k := range []string{"enabled", "endpoint", "env_override"} {
			if _, ok := obj[k]; !ok {
				t.Errorf("metrics --json missing %q: %v", k, obj)
			}
		}
	})

	t.Run("example", func(t *testing.T) {
		out, _ := runBdForMetrics(t, bd, repo, home, "metrics", "example", "--json")
		obj := parseObj(t, "metrics example", out)
		if _, ok := obj["example_payload"]; !ok {
			t.Errorf("metrics example --json missing example_payload: %v", obj)
		}
	})

	t.Run("on", func(t *testing.T) {
		out, _ := runBdForMetrics(t, bd, repo, home, "metrics", "on", "--json")
		obj := parseObj(t, "metrics on", out)
		if enabled, _ := obj["enabled"].(bool); !enabled {
			t.Errorf("metrics on --json should report enabled:true, got %v", obj)
		}
		if _, ok := obj["changed"]; !ok {
			t.Errorf("metrics on --json missing changed: %v", obj)
		}
	})

	t.Run("off", func(t *testing.T) {
		out, _ := runBdForMetrics(t, bd, repo, home, "metrics", "off", "--json")
		obj := parseObj(t, "metrics off", out)
		if enabled, _ := obj["enabled"].(bool); enabled {
			t.Errorf("metrics off --json should report enabled:false, got %v", obj)
		}
		if _, ok := obj["changed"]; !ok {
			t.Errorf("metrics off --json missing changed: %v", obj)
		}
	})
}
