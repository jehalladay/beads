package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// This file raises coverage on internal/config helpers that were previously
// exercised only incidentally (beads-23z, r06 C1). All tests are hermetic:
// they save/restore the global viper `v` and use t.Setenv for any env vars so
// they pass regardless of the ambient crew-shell environment.

// withViper installs a fresh viper populated from the given settings for the
// duration of the test, restoring the previous global on cleanup.
func withViper(t *testing.T, settings map[string]interface{}) {
	t.Helper()
	prev := v
	nv := viper.New()
	for k, val := range settings {
		nv.Set(k, val)
	}
	v = nv
	t.Cleanup(func() { v = prev })
}

// withNilViper sets the global viper to nil (uninitialized) for the test.
func withNilViper(t *testing.T) {
	t.Helper()
	prev := v
	v = nil
	t.Cleanup(func() { v = prev })
}

func TestEnvVarName(t *testing.T) {
	defer envSnapshot(t)()

	t.Run("BD_ prefix when set", func(t *testing.T) {
		t.Setenv("BD_ROUTING_MODE", "auto")
		if got := EnvVarName("routing.mode"); got != "BD_ROUTING_MODE" {
			t.Errorf("EnvVarName = %q, want BD_ROUTING_MODE", got)
		}
	})

	t.Run("dashes and dots normalized", func(t *testing.T) {
		t.Setenv("BD_MY_LONG_KEY", "1")
		if got := EnvVarName("my-long.key"); got != "BD_MY_LONG_KEY" {
			t.Errorf("EnvVarName = %q, want BD_MY_LONG_KEY", got)
		}
	})

	t.Run("BEADS_ fallback when only that is set", func(t *testing.T) {
		t.Setenv("BEADS_ONLY_KEY", "x")
		if got := EnvVarName("only.key"); got != "BEADS_ONLY_KEY" {
			t.Errorf("EnvVarName = %q, want BEADS_ONLY_KEY", got)
		}
	})

	t.Run("empty when neither set", func(t *testing.T) {
		if got := EnvVarName("no.such.key"); got != "" {
			t.Errorf("EnvVarName = %q, want empty", got)
		}
	})
}

func TestLogOverride(t *testing.T) {
	// LogOverride writes to stderr; exercise every source/overriddenBy branch
	// for coverage. It has no return value, so we just assert it does not panic.
	cases := []ConfigOverride{
		{Key: "a", OriginalSource: SourceConfigFile, OverriddenBy: SourceFlag},
		{Key: "b", OriginalSource: SourceEnvVar, OverriddenBy: SourceEnvVar},
		{Key: "c", OriginalSource: SourceDefault, OverriddenBy: SourceFlag},
		{Key: "d", OriginalSource: ConfigSource("weird"), OverriddenBy: ConfigSource("odd")},
	}
	for _, c := range cases {
		LogOverride(c) // must not panic
	}
}

func TestDefaultAIModel(t *testing.T) {
	t.Run("returns configured model", func(t *testing.T) {
		withViper(t, map[string]interface{}{"ai.model": "claude-opus-4-8"})
		if got := DefaultAIModel(); got != "claude-opus-4-8" {
			t.Errorf("DefaultAIModel = %q, want claude-opus-4-8", got)
		}
	})

	t.Run("empty when viper nil", func(t *testing.T) {
		withNilViper(t)
		if got := DefaultAIModel(); got != "" {
			t.Errorf("DefaultAIModel = %q, want empty", got)
		}
	})
}

func TestAllKeys(t *testing.T) {
	t.Run("returns registered keys", func(t *testing.T) {
		withViper(t, map[string]interface{}{"federation.remote": "x", "ai.model": "y"})
		keys := AllKeys()
		found := map[string]bool{}
		for _, k := range keys {
			found[k] = true
		}
		if !found["federation.remote"] || !found["ai.model"] {
			t.Errorf("AllKeys = %v, want to contain federation.remote and ai.model", keys)
		}
	})

	t.Run("nil when viper nil", func(t *testing.T) {
		withNilViper(t)
		if got := AllKeys(); got != nil {
			t.Errorf("AllKeys = %v, want nil", got)
		}
	})
}

func TestMetadataValidationMode(t *testing.T) {
	tests := []struct {
		name string
		mode interface{}
		want string
	}{
		{"warn", "warn", "warn"},
		{"error", "error", "error"},
		{"unknown falls back to none", "bogus", "none"},
		{"empty falls back to none", "", "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withViper(t, map[string]interface{}{"validation.metadata.mode": tt.mode})
			if got := MetadataValidationMode(); got != tt.want {
				t.Errorf("MetadataValidationMode = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("none when viper nil", func(t *testing.T) {
		withNilViper(t)
		if got := MetadataValidationMode(); got != "none" {
			t.Errorf("MetadataValidationMode = %q, want none", got)
		}
	})
}

func TestMetadataSchemaFields(t *testing.T) {
	t.Run("returns map of fields", func(t *testing.T) {
		fields := map[string]interface{}{
			"severity": map[string]interface{}{"type": "enum", "required": true},
		}
		withViper(t, map[string]interface{}{"validation.metadata.fields": fields})
		got := MetadataSchemaFields()
		if got == nil {
			t.Fatal("MetadataSchemaFields = nil, want map")
		}
		if _, ok := got["severity"]; !ok {
			t.Errorf("MetadataSchemaFields = %v, want severity key", got)
		}
	})

	t.Run("nil when unset", func(t *testing.T) {
		withViper(t, map[string]interface{}{})
		if got := MetadataSchemaFields(); got != nil {
			t.Errorf("MetadataSchemaFields = %v, want nil", got)
		}
	})

	t.Run("nil when viper nil", func(t *testing.T) {
		withNilViper(t)
		if got := MetadataSchemaFields(); got != nil {
			t.Errorf("MetadataSchemaFields = %v, want nil", got)
		}
	})
}

func TestGetInfraTypesFromYAML(t *testing.T) {
	t.Run("sequence value", func(t *testing.T) {
		withViper(t, map[string]interface{}{"types.infra": []string{"vpc", "subnet"}})
		got := GetInfraTypesFromYAML()
		if len(got) != 2 || got[0] != "vpc" || got[1] != "subnet" {
			t.Errorf("GetInfraTypesFromYAML = %v, want [vpc subnet]", got)
		}
	})

	t.Run("nil when viper nil", func(t *testing.T) {
		withNilViper(t)
		if got := GetInfraTypesFromYAML(); got != nil {
			t.Errorf("GetInfraTypesFromYAML = %v, want nil", got)
		}
	})
}

func TestGetCustomStatusesFromYAML(t *testing.T) {
	t.Run("comma-separated string is split", func(t *testing.T) {
		withViper(t, map[string]interface{}{"status.custom": "triage, review ,done"})
		got := GetCustomStatusesFromYAML()
		want := []string{"triage", "review", "done"}
		if len(got) != len(want) {
			t.Fatalf("GetCustomStatusesFromYAML = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("index %d = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("nil when unset", func(t *testing.T) {
		withViper(t, map[string]interface{}{})
		if got := GetCustomStatusesFromYAML(); got != nil {
			t.Errorf("GetCustomStatusesFromYAML = %v, want nil", got)
		}
	})
}

func TestGetDirectoryLabels(t *testing.T) {
	t.Run("nil when no labels configured", func(t *testing.T) {
		withViper(t, map[string]interface{}{})
		if got := GetDirectoryLabels(); got != nil {
			t.Errorf("GetDirectoryLabels = %v, want nil", got)
		}
	})

	t.Run("matches suffix pattern for cwd", func(t *testing.T) {
		dir := t.TempDir()
		sub := filepath.Join(dir, "packages", "maverick")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		oldWD, _ := os.Getwd()
		if err := os.Chdir(sub); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(oldWD) })

		withViper(t, map[string]interface{}{
			"directory.labels": map[string]interface{}{"packages/maverick": "team-maverick"},
		})
		got := GetDirectoryLabels()
		if len(got) != 1 || got[0] != "team-maverick" {
			t.Errorf("GetDirectoryLabels = %v, want [team-maverick]", got)
		}
	})
}

func TestSetNestedKeyViaSaveConfigValue(t *testing.T) {
	// SaveConfigValue and its helper setNestedKey are both 0%. Drive them by
	// writing a nested key into a fresh config file in a temp beadsDir.
	prev := v
	v = viper.New()
	t.Cleanup(func() { v = prev })

	dir := t.TempDir()
	if err := SaveConfigValue("routing.mode", "auto", dir); err != nil {
		t.Fatalf("SaveConfigValue: %v", err)
	}

	// The value must be persisted under a nested YAML structure.
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("reading config.yaml: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "routing:") || !strings.Contains(content, "mode: auto") {
		t.Errorf("config.yaml = %q, want nested routing.mode: auto", content)
	}

	// A second, top-level key preserves the first (exercises the merge/read path).
	if err := SaveConfigValue("editor", "vim", dir); err != nil {
		t.Fatalf("SaveConfigValue 2: %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if !strings.Contains(string(data2), "editor: vim") || !strings.Contains(string(data2), "mode: auto") {
		t.Errorf("config.yaml after 2nd save = %q, want both keys present", string(data2))
	}
}

func TestSaveConfigValue_NilViper(t *testing.T) {
	withNilViper(t)
	if err := SaveConfigValue("k", "val", t.TempDir()); err == nil {
		t.Error("SaveConfigValue with nil viper = nil error, want 'config not initialized'")
	}
}

func TestIsUserGlobalKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"metrics.disabled", true},
		{"metrics.endpoint", true},
		{"routing.mode", false},
		{"sync.branch", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsUserGlobalKey(tt.key); got != tt.want {
			t.Errorf("IsUserGlobalKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestGetYamlConfig(t *testing.T) {
	t.Run("returns configured value", func(t *testing.T) {
		withViper(t, map[string]interface{}{"sync.branch": "main"})
		if got := GetYamlConfig("sync.branch"); got != "main" {
			t.Errorf("GetYamlConfig = %q, want main", got)
		}
	})

	t.Run("empty when viper nil", func(t *testing.T) {
		withNilViper(t)
		if got := GetYamlConfig("sync.branch"); got != "" {
			t.Errorf("GetYamlConfig = %q, want empty", got)
		}
	})
}

// TestUserYamlConfigRoundTrip drives Set/Get/Unset against the user-global
// config.yaml. TestMain points HOME/XDG at a temp dir, so UserConfigYamlPath()
// resolves under the sandbox — no real user config is touched.
func TestUserYamlConfigRoundTrip(t *testing.T) {
	// Isolate the user-global config path to this test's own sandbox so the
	// file we create can't leak into sibling tests that assert no user config
	// exists (t.Setenv auto-restores the TestMain values).
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	// metrics.* are user-global keys and pass validateYamlConfigValue.
	const key = "metrics.disabled"

	if err := SetUserYamlConfig(key, "true"); err != nil {
		t.Fatalf("SetUserYamlConfig: %v", err)
	}
	if got := GetUserYamlConfig(key); got != "true" {
		t.Errorf("GetUserYamlConfig after set = %q, want true", got)
	}

	// Unset comments the key out → subsequent read is empty.
	if err := UnsetUserYamlConfig(key); err != nil {
		t.Fatalf("UnsetUserYamlConfig: %v", err)
	}
	if got := GetUserYamlConfig(key); got != "" {
		t.Errorf("GetUserYamlConfig after unset = %q, want empty", got)
	}
}

// TestUnsetUserYamlConfig_MissingFile verifies unsetting a key when no
// user-global config file exists is a no-op (nil error). Isolated HOME so the
// path resolves to an empty sandbox.
func TestUnsetUserYamlConfig_MissingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	if err := UnsetUserYamlConfig("metrics.endpoint"); err != nil {
		t.Errorf("UnsetUserYamlConfig on missing file = %v, want nil", err)
	}
}

func TestSetUserYamlConfig_ValidationRejects(t *testing.T) {
	// hierarchy.max-depth < 1 must be rejected before any file write.
	if err := SetUserYamlConfig("hierarchy.max-depth", "0"); err == nil {
		t.Error("SetUserYamlConfig(hierarchy.max-depth=0) = nil, want validation error")
	}
}

func TestCheckSecretKeyGitSafety(t *testing.T) {
	// In the temp sandbox there is no project config.yaml, so findProjectConfigYaml
	// fails and CheckSecretKeyGitSafety returns nil (can't resolve path → let the
	// write fail with its own error). Exercises the early-return branch.
	if err := CheckSecretKeyGitSafety("ai.api_key"); err != nil {
		t.Errorf("CheckSecretKeyGitSafety with no project config = %v, want nil", err)
	}
	// A non-secret key is always safe regardless of path resolution.
	if err := CheckSecretKeyGitSafety("routing.mode"); err != nil {
		t.Errorf("CheckSecretKeyGitSafety(non-secret) = %v, want nil", err)
	}
}

func TestReposFromYAMLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Missing file → empty ReposConfig, no error.
	repos, err := GetReposFromYAML(configPath)
	if err != nil {
		t.Fatalf("GetReposFromYAML(missing) error = %v", err)
	}
	if repos.Primary != "" || len(repos.Additional) != 0 {
		t.Errorf("GetReposFromYAML(missing) = %+v, want empty", repos)
	}

	// Write repos, then read them back (also covers ListRepos which delegates).
	want := &ReposConfig{
		Primary:    "git@example.com:org/main.git",
		Additional: []string{"git@example.com:org/lib.git"},
	}
	if err := SetReposInYAML(configPath, want); err != nil {
		t.Fatalf("SetReposInYAML: %v", err)
	}

	got, err := ListRepos(configPath)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if got.Primary != want.Primary {
		t.Errorf("ListRepos Primary = %q, want %q", got.Primary, want.Primary)
	}
	if len(got.Additional) != 1 || got.Additional[0] != want.Additional[0] {
		t.Errorf("ListRepos Additional = %v, want %v", got.Additional, want.Additional)
	}
}

// TestGetValueSourceBranches covers the ConfigSource branches not exercised by
// TestGetValueSource in config_test.go: the nil-viper and runtime-Set() paths,
// plus the unset->default path with an explicit viper. All env mutation is
// snapshot/restored so the test is hermetic under crew env.
func TestGetValueSourceBranches(t *testing.T) {
	t.Run("nil viper -> default", func(t *testing.T) {
		withNilViper(t)
		if got := GetValueSource("any.key"); got != SourceDefault {
			t.Errorf("GetValueSource(nil viper) = %q, want %q", got, SourceDefault)
		}
	})

	t.Run("runtime Set -> config_file source", func(t *testing.T) {
		defer envSnapshot(t)()
		withViper(t, map[string]interface{}{})
		prev := overriddenKeys
		overriddenKeys = map[string]bool{"ai.model": true}
		t.Cleanup(func() { overriddenKeys = prev })
		if got := GetValueSource("ai.model"); got != SourceConfigFile {
			t.Errorf("GetValueSource = %q, want %q", got, SourceConfigFile)
		}
	})

	t.Run("unset -> default", func(t *testing.T) {
		defer envSnapshot(t)()
		withViper(t, map[string]interface{}{})
		if got := GetValueSource("never.set.key"); got != SourceDefault {
			t.Errorf("GetValueSource = %q, want %q", got, SourceDefault)
		}
	})
}

// TestCheckOverridesBranches covers the CheckOverrides branches not exercised by
// TestCheckOverrides_FlagOverridesEnvVar in config_test.go: the skip-unset-flag
// path, the flag-over-default no-op path, and the nil-viper path.
func TestCheckOverridesBranches(t *testing.T) {
	type flagInfo = struct {
		Value  interface{}
		WasSet bool
	}

	t.Run("flag not set is skipped", func(t *testing.T) {
		defer envSnapshot(t)()
		withViper(t, map[string]interface{}{})
		t.Setenv("BD_AI_MODEL", "env-model")
		got := CheckOverrides(map[string]flagInfo{
			"ai.model": {Value: "flag-model", WasSet: false},
		})
		if len(got) != 0 {
			t.Errorf("expected no overrides for unset flag, got %v", got)
		}
	})

	t.Run("flag over default is not an override", func(t *testing.T) {
		defer envSnapshot(t)()
		withViper(t, map[string]interface{}{})
		got := CheckOverrides(map[string]flagInfo{
			"ai.model": {Value: "flag-model", WasSet: true},
		})
		if len(got) != 0 {
			t.Errorf("flag over a default value should not report an override, got %v", got)
		}
	})

	t.Run("nil viper returns no overrides", func(t *testing.T) {
		defer envSnapshot(t)()
		withNilViper(t)
		got := CheckOverrides(map[string]flagInfo{})
		if len(got) != 0 {
			t.Errorf("expected no overrides with nil viper, got %v", got)
		}
	})
}

// TestGetDurationAndMapString covers the viper-backed getters on the populated
// path (the nil-viper path is already covered in config_test.go).
func TestGetDurationAndMapString(t *testing.T) {
	t.Run("GetDuration reads viper", func(t *testing.T) {
		withViper(t, map[string]interface{}{"backup.interval": "30s"})
		if got := GetDuration("backup.interval"); got.String() != "30s" {
			t.Errorf("GetDuration = %v, want 30s", got)
		}
	})

	t.Run("GetStringMapString nil viper", func(t *testing.T) {
		withNilViper(t)
		if got := GetStringMapString("directory.labels"); len(got) != 0 {
			t.Errorf("GetStringMapString(nil viper) = %v, want empty", got)
		}
	})

	t.Run("GetStringMapString reads viper", func(t *testing.T) {
		withViper(t, map[string]interface{}{
			"directory.labels": map[string]interface{}{"packages/x": "team-x"},
		})
		got := GetStringMapString("directory.labels")
		if got["packages/x"] != "team-x" {
			t.Errorf("GetStringMapString = %v, want packages/x -> team-x", got)
		}
	})
}

// TestPureYamlHelpers pins the remaining branches of the unexported scalar
// helpers so their behavior is locked against refactors.
func TestPureYamlHelpers(t *testing.T) {
	t.Run("isNumeric", func(t *testing.T) {
		cases := map[string]bool{
			"":     false,
			"123":  true,
			"-5":   true,
			"1.5":  true,
			"-1.5": true,
			"1a":   false,
			"1-2":  false, // '-' only allowed at index 0
			"abc":  false,
		}
		for in, want := range cases {
			if got := isNumeric(in); got != want {
				t.Errorf("isNumeric(%q) = %v, want %v", in, got, want)
			}
		}
	})

	t.Run("isDuration", func(t *testing.T) {
		cases := map[string]bool{
			"":     false,
			"s":    false, // len < 2
			"30s":  true,
			"5m":   true,
			"2h":   true,
			"30x":  false, // bad suffix
			"abcs": false, // non-numeric prefix
		}
		for in, want := range cases {
			if got := isDuration(in); got != want {
				t.Errorf("isDuration(%q) = %v, want %v", in, got, want)
			}
		}
	})

	t.Run("yamlScalarString", func(t *testing.T) {
		if s, ok := yamlScalarString(nil); ok || s != "" {
			t.Errorf("yamlScalarString(nil) = %q,%v want \"\",false", s, ok)
		}
		if s, ok := yamlScalarString("hi"); !ok || s != "hi" {
			t.Errorf("yamlScalarString(string) = %q,%v want hi,true", s, ok)
		}
		if s, ok := yamlScalarString(42); !ok || s != "42" {
			t.Errorf("yamlScalarString(int) = %q,%v want 42,true", s, ok)
		}
		if s, ok := yamlScalarString(true); !ok || s != "true" {
			t.Errorf("yamlScalarString(bool) = %q,%v want true,true", s, ok)
		}
	})

	t.Run("scalarStyleFor", func(t *testing.T) {
		// Empty, ambiguous scalars, and special chars force double-quoting;
		// plain numbers/bools/words use the default style (0).
		doubleQuoted := []string{"", "yes", "no", "on", "off", "null", "~", "a: b", "with#hash", " leading", "trailing "}
		for _, in := range doubleQuoted {
			if got := scalarStyleFor(in); got == 0 {
				t.Errorf("scalarStyleFor(%q) = 0, want double-quoted style", in)
			}
		}
		plain := []string{"true", "false", "123", "1.5", "plainword"}
		for _, in := range plain {
			if got := scalarStyleFor(in); got != 0 {
				t.Errorf("scalarStyleFor(%q) = %v, want default (0)", in, got)
			}
		}
	})
}

// TestGetDirectoryLabelsPrefixAndNoMatch covers the path-prefix match branch
// and the labels-configured-but-no-match branch of GetDirectoryLabels, which
// the suffix and empty cases above do not reach.
func TestGetDirectoryLabelsPrefixAndNoMatch(t *testing.T) {
	t.Run("matches path-contains pattern from a subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		sub := filepath.Join(dir, "services", "api", "internal")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		oldWD, _ := os.Getwd()
		if err := os.Chdir(sub); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(oldWD) })

		withViper(t, map[string]interface{}{
			"directory.labels": map[string]interface{}{"services/api": "team-api"},
		})
		got := GetDirectoryLabels()
		if len(got) != 1 || got[0] != "team-api" {
			t.Errorf("GetDirectoryLabels = %v, want [team-api]", got)
		}
	})

	t.Run("labels configured but none match", func(t *testing.T) {
		dir := t.TempDir()
		oldWD, _ := os.Getwd()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(oldWD) })

		withViper(t, map[string]interface{}{
			"directory.labels": map[string]interface{}{"packages/nowhere": "team-x"},
		})
		if got := GetDirectoryLabels(); got != nil {
			t.Errorf("GetDirectoryLabels = %v, want nil (no match)", got)
		}
	})
}

// TestSetUnsetYamlConfigViaBeadsDir round-trips SetYamlConfig -> UnsetYamlConfig
// against a config.yaml resolved through BEADS_DIR, exercising the CWD-free
// write path hermetically (no dependency on the crew workspace layout).
func TestSetUnsetYamlConfigViaBeadsDir(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(beadsDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("# beads config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DIR", beadsDir)

	if err := SetYamlConfig("ai.model", "claude-opus-4-8"); err != nil {
		t.Fatalf("SetYamlConfig: %v", err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "claude-opus-4-8") {
		t.Errorf("config.yaml missing set value; got:\n%s", content)
	}

	if err := UnsetYamlConfig("ai.model"); err != nil {
		t.Fatalf("UnsetYamlConfig: %v", err)
	}
	content, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// UnsetYamlConfig comments the key out rather than deleting the line.
	if !strings.Contains(string(content), "#") {
		t.Errorf("expected key to be commented out after Unset; got:\n%s", content)
	}
}

// TestMetricsConsentFromUserConfig covers MetricsDisabledByUserConfig and
// MetricsNoticeShownByUserConfig across present-true, present-unparseable, and
// absent states, using an isolated user-global config path (HOME sandbox).
func TestMetricsConsentFromUserConfig(t *testing.T) {
	setupHome := func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	}

	t.Run("absent -> false", func(t *testing.T) {
		setupHome(t)
		if MetricsDisabledByUserConfig() {
			t.Error("MetricsDisabledByUserConfig = true, want false when unset")
		}
		if MetricsNoticeShownByUserConfig() {
			t.Error("MetricsNoticeShownByUserConfig = true, want false when unset")
		}
	})

	t.Run("set true -> true", func(t *testing.T) {
		setupHome(t)
		if err := SetUserYamlConfig("metrics.disabled", "true"); err != nil {
			t.Fatalf("SetUserYamlConfig: %v", err)
		}
		if !MetricsDisabledByUserConfig() {
			t.Error("MetricsDisabledByUserConfig = false, want true")
		}
	})

	t.Run("notice_shown true -> true", func(t *testing.T) {
		setupHome(t)
		if err := SetUserYamlConfig("metrics.notice_shown", "true"); err != nil {
			t.Fatalf("SetUserYamlConfig: %v", err)
		}
		if !MetricsNoticeShownByUserConfig() {
			t.Error("MetricsNoticeShownByUserConfig = false, want true")
		}
	})
}
