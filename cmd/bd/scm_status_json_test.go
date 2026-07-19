package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// beads-5erz: `bd github status --json` and `bd gitlab status --json` wholly
// ignored the global --json flag — runGitHubStatus/runGitLabStatus always
// printed the plaintext "GitHub/GitLab Configuration" table and returned nil,
// so a --json consumer got unparseable plaintext on stdout (and, for the
// unconfigured case, exit 0 masking "not configured"). The fix adds a --json
// branch mirroring `bd ado status` (the good sibling): a structured
// {configured,error,...} object. These tests drive the RunE with jsonOutput=1
// and an unconfigured env, asserting the stdout parses as a JSON object with
// configured:false + a non-empty error. RED before the fix (plaintext table).

func TestGitHubStatusJSONContract_5erz(t *testing.T) {
	oldDBPath, oldStore, oldJSON := dbPath, store, jsonOutput
	dbPath, store, jsonOutput = "", nil, true
	t.Cleanup(func() { dbPath, store, jsonOutput = oldDBPath, oldStore, oldJSON })

	// Unconfigured: clear every GitHub env var so validateGitHubConfig fails.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_OWNER", "")
	t.Setenv("GITHUB_REPO", "")
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_API_URL", "")

	out := captureStdout(t, func() error {
		return runGitHubStatus(&cobra.Command{}, nil)
	})

	assertSCMStatusJSON(t, "github status", out, false)
}

func TestGitLabStatusJSONContract_5erz(t *testing.T) {
	oldDBPath, oldStore, oldJSON := dbPath, store, jsonOutput
	dbPath, store, jsonOutput = "", nil, true
	t.Cleanup(func() { dbPath, store, jsonOutput = oldDBPath, oldStore, oldJSON })

	// Unconfigured: clear every GitLab env var so validateGitLabConfig fails.
	t.Setenv("GITLAB_URL", "")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_PROJECT_ID", "")
	t.Setenv("GITLAB_GROUP_ID", "")
	t.Setenv("GITLAB_DEFAULT_PROJECT_ID", "")

	out := captureStdout(t, func() error {
		return runGitLabStatus(&cobra.Command{}, nil)
	})

	assertSCMStatusJSON(t, "gitlab status", out, false)
}

// assertSCMStatusJSON verifies the --json output is a single JSON object
// carrying a "configured" bool matching wantConfigured; when not configured it
// must also carry a non-empty "error" string (the machine-readable signal that
// replaces the plaintext "❌ Not configured / Error: ..." table).
func assertSCMStatusJSON(t *testing.T, label, out string, wantConfigured bool) {
	t.Helper()
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		t.Fatalf("%s --json: stdout empty (beads-5erz)", label)
	}
	// The plaintext table starts with "GitHub"/"GitLab Configuration" — a fast
	// tell the --json branch was skipped.
	if strings.Contains(trimmed, "Configuration\n===") {
		t.Fatalf("%s --json emitted the plaintext table, not JSON (beads-5erz):\n%s", label, trimmed)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		t.Fatalf("%s --json: stdout is not a JSON object: %v\nstdout:\n%s", label, err, trimmed)
	}
	cfg, ok := obj["configured"].(bool)
	if !ok {
		t.Fatalf("%s --json: missing/!bool \"configured\": %v", label, obj)
	}
	if cfg != wantConfigured {
		t.Fatalf("%s --json: configured=%v, want %v: %v", label, cfg, wantConfigured, obj)
	}
	if !wantConfigured {
		if e, _ := obj["error"].(string); e == "" {
			t.Fatalf("%s --json: unconfigured status must carry a non-empty \"error\": %v", label, obj)
		}
	}
}
