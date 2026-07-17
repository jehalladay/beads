package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/config"
)

// beads-3q1n: SECURITY teeth — bd config list/show/set must never print a
// secret key's cleartext VALUE. IsSecretKey existed but was applied only to the
// git-write-safety guard, never to display. These tests exercise the display
// chokepoints and assert the secret value is redacted.

const testSecret = "ghp_thisIsASecretTokenValue123"

// seedGithubTokenConfig writes github.token=testSecret into a fresh temp
// project config.yaml and chdirs into it, then re-initializes config so
// collectConfigEntries reads the seeded value deterministically. Seeding via
// the on-disk config (not an env-var → viper binding, which is initialization-
// order sensitive) makes these display-redaction teeth hermetic.
func seedGithubTokenConfig(t *testing.T) {
	t.Helper()
	t.Setenv("BEADS_TEST_IGNORE_REPO_CONFIG", "")
	tmp := t.TempDir()
	beadsDir := filepath.Join(tmp, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"),
		[]byte("github:\n  token: "+testSecret+"\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	t.Chdir(tmp)
	config.ResetForTesting()
	if err := config.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// Precondition: the seeded secret is actually visible to the config layer,
	// so a later "not present" failure means a redaction/collection bug, not a
	// seeding miss.
	if got := config.GetString("github.token"); got != testSecret {
		t.Fatalf("seed precondition failed: github.token = %q, want %q", got, testSecret)
	}
}

// collectConfigEntries feeds both the human table (printConfigEntries) and the
// --json output; redaction happens once at collection, so asserting on its
// result covers every config-show display path.
func TestCollectConfigEntriesRedactsSecrets(t *testing.T) {
	seedGithubTokenConfig(t)

	entries := collectConfigEntries()

	var found bool
	for _, e := range entries {
		if e.Key == "github.token" {
			found = true
			if strings.Contains(e.Value, testSecret) {
				t.Errorf("collectConfigEntries LEAKED secret value for github.token: %q", e.Value)
			}
			if e.Value != config.RedactedSecretPlaceholder {
				t.Errorf("github.token value = %q, want redacted placeholder %q", e.Value, config.RedactedSecretPlaceholder)
			}
		}
	}
	if !found {
		t.Fatalf("github.token not present in collected entries; got %d entries", len(entries))
	}
}

// The env-override warning path of `bd config list` echoes the live env value;
// it must redact a secret key too (beads-3q1n).
func TestShowConfigYAMLOverridesRedactsSecretEnv(t *testing.T) {
	t.Setenv("BEADS_TEST_IGNORE_REPO_CONFIG", "1")
	config.ResetForTesting()
	if err := config.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// GITHUB_TOKEN is the env var for the github.token secret key.
	t.Setenv("GITHUB_TOKEN", testSecret)

	dbConfig := map[string]string{
		"github.token": "db-old-value",
	}

	out := captureStdout(t, func() error {
		showConfigYAMLOverrides(dbConfig)
		return nil
	})

	if strings.Contains(out, testSecret) {
		t.Errorf("showConfigYAMLOverrides LEAKED secret env value:\n%s", out)
	}
}

// redactUnlessShown is the single display-site gate: it redacts by default and
// returns the raw value only when the --show-secrets flag (showSecrets) is set.
// These teeth pin BOTH directions of the escape hatch (beads-3q1n ruling B +
// --show-secrets).
func TestRedactUnlessShownHonorsShowSecretsFlag(t *testing.T) {
	orig := showSecrets
	t.Cleanup(func() { showSecrets = orig })

	// Default (flag off): secret value is redacted, non-secret is passed through.
	showSecrets = false
	if got := redactUnlessShown("github.token", testSecret); got != config.RedactedSecretPlaceholder {
		t.Errorf("default redactUnlessShown(secret) = %q, want redacted placeholder", got)
	}
	if got := redactUnlessShown("routing.mode", "auto"); got != "auto" {
		t.Errorf("default redactUnlessShown(non-secret) = %q, want %q", got, "auto")
	}

	// --show-secrets on: the raw secret value comes through (deliberate read-back).
	showSecrets = true
	if got := redactUnlessShown("github.token", testSecret); got != testSecret {
		t.Errorf("--show-secrets redactUnlessShown(secret) = %q, want raw %q", got, testSecret)
	}
}

// collectConfigEntries must honor --show-secrets: with the flag set, the secret
// value is present raw; with it clear, it is redacted (the default asserted in
// TestCollectConfigEntriesRedactsSecrets).
func TestCollectConfigEntriesShowSecretsRevealsRaw(t *testing.T) {
	seedGithubTokenConfig(t)

	orig := showSecrets
	t.Cleanup(func() { showSecrets = orig })
	showSecrets = true

	entries := collectConfigEntries()
	var found bool
	for _, e := range entries {
		if e.Key == "github.token" {
			found = true
			if e.Value != testSecret {
				t.Errorf("with --show-secrets, github.token = %q, want raw %q", e.Value, testSecret)
			}
		}
	}
	if !found {
		t.Fatalf("github.token not present in collected entries; got %d entries", len(entries))
	}
}

// printConfigEntries renders exactly what collectConfigEntries produced, so a
// redacted entry stays redacted at the terminal.
func TestPrintConfigEntriesShowsRedactedPlaceholder(t *testing.T) {
	out := captureStdout(t, func() error {
		printConfigEntries([]configEntry{
			{Key: "github.token", Value: config.RedactedSecretPlaceholder, Source: "config.yaml"},
			{Key: "routing.mode", Value: "auto", Source: "default"},
		})
		return nil
	})
	if strings.Contains(out, testSecret) {
		t.Errorf("printConfigEntries leaked secret:\n%s", out)
	}
	if !strings.Contains(out, config.RedactedSecretPlaceholder) {
		t.Errorf("expected redacted placeholder in output:\n%s", out)
	}
}
