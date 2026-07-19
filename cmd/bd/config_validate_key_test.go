package main

import (
	"strings"
	"testing"
)

func TestIsRecognizedConfigKey(t *testing.T) {
	recognized := []string{
		"export.auto", "dolt.auto-push", "jira.url", "custom.anything",
		"doctor.suppress.git-hooks", "no-git-ops", "beads.role",
		"status.custom", "ai.model", "backup.enabled", "import.path",
		"dolt.local-only",
	}
	for _, key := range recognized {
		if !isRecognizedConfigKey(key) {
			t.Errorf("isRecognizedConfigKey(%q) = false, want true", key)
		}
	}

	unrecognized := []string{
		"totally.bogus", "exprot.auto", "xport.path", "nodb",
	}
	for _, key := range unrecognized {
		if isRecognizedConfigKey(key) {
			t.Errorf("isRecognizedConfigKey(%q) = true, want false", key)
		}
	}
}

// TestCompactionConfigKeysRecognized pins beads-22pp: the 8 compaction knobs
// are seeded as defaults (migration 0016), exposed by config list/get, and
// live-read by the compaction engine via their bare key, so they must be
// settable via `bd config set` — isRecognizedConfigKey must accept them.
func TestCompactionConfigKeysRecognized(t *testing.T) {
	compactionKeys := []string{
		"compaction_enabled",
		"compact_tier1_days", "compact_tier1_dep_levels",
		"compact_tier2_days", "compact_tier2_dep_levels", "compact_tier2_commits",
		"compact_batch_size", "compact_parallel_workers",
	}
	for _, key := range compactionKeys {
		if !isRecognizedConfigKey(key) {
			t.Errorf("isRecognizedConfigKey(%q) = false, want true (beads-22pp: engine-read compaction knob must be settable)", key)
		}
	}
}

// TestCompactionConfigKeyTypeValidation pins beads-22pp: compaction_enabled is
// a bool and the tier/batch/worker knobs are ints, so `bd config set` must
// reject a type-invalid value at set-time (the engine parses them via
// ParseBool/Atoi and would otherwise choke latently).
func TestCompactionConfigKeyTypeValidation(t *testing.T) {
	// Valid values pass.
	valid := map[string]string{
		"compaction_enabled":       "true",
		"compact_tier1_days":       "60",
		"compact_tier1_dep_levels": "3",
		"compact_tier2_days":       "120",
		"compact_tier2_dep_levels": "5",
		"compact_tier2_commits":    "100",
		"compact_batch_size":       "50",
		"compact_parallel_workers": "4",
	}
	for key, val := range valid {
		if err := validateConfigValueType(key, val); err != nil {
			t.Errorf("validateConfigValueType(%q, %q) = %v, want nil", key, val, err)
		}
	}
	// Type-invalid values are rejected.
	invalid := map[string]string{
		"compaction_enabled":       "yesplease",
		"compact_tier1_days":       "thirty",
		"compact_parallel_workers": "1.5",
	}
	for key, val := range invalid {
		if err := validateConfigValueType(key, val); err == nil {
			t.Errorf("validateConfigValueType(%q, %q) = nil, want type error", key, val)
		}
	}
}

func TestConfigHelpMentionsDoltLocalOnly(t *testing.T) {
	if !strings.Contains(configCmd.Long, "bd config set dolt.local-only true") {
		t.Fatalf("config help missing dolt.local-only example:\n%s", configCmd.Long)
	}
}

func TestSuggestConfigKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"exprot.auto", "export.auto"},
		{"exoprt.path", "export.path"},
		{"totally.bogus", ""},
	}
	for _, tt := range tests {
		got := suggestConfigKey(tt.input)
		if got != tt.want {
			t.Errorf("suggestConfigKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRejectProtectedConfigKey(t *testing.T) {
	rejectedKeys := []string{"issue_prefix", "issue-prefix"}
	for _, key := range rejectedKeys {
		msg, rejected := rejectProtectedConfigKey(key)
		if !rejected {
			t.Errorf("rejectProtectedConfigKey(%q) = (_, false), want rejected", key)
			continue
		}
		// Error message must surface the three lifecycle alternatives.
		wantSubstrings := []string{"bd init --prefix", "bd bootstrap", "bd rename-prefix"}
		for _, want := range wantSubstrings {
			if !strings.Contains(msg, want) {
				t.Errorf("rejectProtectedConfigKey(%q) message missing %q; got:\n%s", key, want, msg)
			}
		}
	}

	allowedKeys := []string{"allowed_prefixes", "export.auto", "status.custom", "custom.anything"}
	for _, key := range allowedKeys {
		if _, rejected := rejectProtectedConfigKey(key); rejected {
			t.Errorf("rejectProtectedConfigKey(%q) = (_, true), want not rejected", key)
		}
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"export", "exprot", 2},
		{"dolt", "bolt", 1},
		{"abc", "", 3},
	}
	for _, tt := range tests {
		got := levenshteinDistance(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
