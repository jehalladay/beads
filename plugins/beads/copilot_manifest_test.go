package beadsplugin

import (
	"encoding/json"
	"testing"
)

// TestCopilotPluginManifestEmbedded guards the //go:embed directive: a renamed
// or moved .copilot-plugin/plugin.json (or a broken "all:" prefix on the
// dot-directory) would silently embed an empty string. Assert the accessor
// returns non-empty, valid JSON.
func TestCopilotPluginManifestEmbedded(t *testing.T) {
	got := CopilotPluginManifest()
	if got == "" {
		t.Fatal("CopilotPluginManifest() is empty — the go:embed of .copilot-plugin/plugin.json did not resolve")
	}

	var manifest map[string]any
	if err := json.Unmarshal([]byte(got), &manifest); err != nil {
		t.Fatalf("embedded manifest is not valid JSON: %v", err)
	}

	// Spot-check the load-bearing top-level keys so a structurally-broken
	// manifest (e.g. a truncated embed) fails loudly.
	if name, _ := manifest["name"].(string); name != "beads" {
		t.Errorf("manifest name = %q, want \"beads\"", manifest["name"])
	}
	for _, key := range []string{"version", "description", "hooks"} {
		if _, ok := manifest[key]; !ok {
			t.Errorf("manifest missing expected top-level key %q", key)
		}
	}
}

// TestCopilotPluginManifestStable verifies the accessor is a pure getter:
// repeated calls return identical content.
func TestCopilotPluginManifestStable(t *testing.T) {
	if a, b := CopilotPluginManifest(), CopilotPluginManifest(); a != b {
		t.Error("CopilotPluginManifest() returned different content across calls")
	}
}
