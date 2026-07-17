package config

import (
	"os"
	"testing"
)

// GetValueSource must only report SourceEnvVar for a BEADS_<KEY> var when viper
// actually reads that var (i.e. the key is BindEnv'd to a BEADS_ name). Only
// "identity" is bound; for every other key the value path uses the BD_ prefix,
// so a set-but-unread BEADS_<KEY> must NOT be reported as env_var — otherwise
// the source lies about the value (beads-mxed): e.g. source-gated callers like
// isBackupAutoEnabled would skip a default they should apply.
func TestGetValueSource_UnboundBeadsVarNotEnvSource(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// A key whose BEADS_ form is NOT bound.
	const key = "output.title-length"
	os.Setenv("BEADS_OUTPUT_TITLE_LENGTH", "42")
	defer os.Unsetenv("BEADS_OUTPUT_TITLE_LENGTH")
	os.Unsetenv("BD_OUTPUT_TITLE_LENGTH")

	src := GetValueSource(key)
	val := GetInt(key)
	// viper never reads the unbound BEADS_ var, so the value stays default.
	if src == SourceEnvVar && val != 42 {
		t.Errorf("GetValueSource(%q)=%q but GetInt=%d (env not actually read) — source must not report env_var for an unread BEADS_ var", key, src, val)
	}
}

// The BD_ prefix is bound end-to-end, so it must still report env_var AND the
// value must reflect it (regression guard — the fix must not touch BD_).
func TestGetValueSource_BDVarStillEnvSource(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	const key = "output.title-length"
	os.Unsetenv("BEADS_OUTPUT_TITLE_LENGTH")
	os.Setenv("BD_OUTPUT_TITLE_LENGTH", "77")
	defer os.Unsetenv("BD_OUTPUT_TITLE_LENGTH")

	if src := GetValueSource(key); src != SourceEnvVar {
		t.Errorf("GetValueSource(%q) with BD_ set = %q, want env_var", key, src)
	}
	if val := GetInt(key); val != 77 {
		t.Errorf("GetInt(%q) with BD_ set = %d, want 77", key, val)
	}
}

// A BEADS_ var on a BOUND key (identity) is genuinely read by viper, so it must
// still report env_var (the fix must keep the legitimate case working).
func TestGetValueSource_BoundBeadsVarStillEnvSource(t *testing.T) {
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	os.Setenv("BEADS_IDENTITY", "alice")
	defer os.Unsetenv("BEADS_IDENTITY")

	if src := GetValueSource("identity"); src != SourceEnvVar {
		t.Errorf("GetValueSource(identity) with BEADS_IDENTITY set = %q, want env_var (it is bound)", src)
	}
}
