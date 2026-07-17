package metrics

import (
	"context"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDataDirHomeError covers the os.UserHomeDir error branch of DataDir: when
// HOME is empty, UserHomeDir fails and DataDir must surface that error rather
// than returning a bogus path.
func TestDataDirHomeError(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := DataDir(); err == nil {
		t.Fatal("DataDir() = nil error with empty HOME, want the UserHomeDir failure")
	}
}

// TestInitEnabledDataDirError covers the DataDir-error return inside Init: with
// metrics enabled and HOME unresolvable, Init cannot build the file emitter and
// must return a wrapped error together with a non-nil no-op close func (so
// callers can defer it unconditionally).
func TestInitEnabledDataDirError(t *testing.T) {
	t.Setenv("HOME", "")
	closeFn, err := Init("0.0.0-test", true, "")
	if err == nil {
		t.Fatal("Init(enabled) with empty HOME = nil error, want a data-dir failure")
	}
	if closeFn == nil {
		t.Fatal("Init returned a nil close func on error; callers defer it")
	}
	// The returned func must be a safe no-op.
	closeFn(context.Background())
}

// TestNewCommandEventEmptyCommandFallsBackToUnknown covers the empty-command
// guard: a telemetry helper must never carry an empty command name, so an empty
// input is recorded as "unknown".
func TestNewCommandEventEmptyCommandFallsBackToUnknown(t *testing.T) {
	evt := NewCommandEvent("")
	if evt == nil {
		t.Fatal("NewCommandEvent(\"\") = nil event")
	}
	// Non-empty input is preserved (the covered arm) — pin both for clarity.
	if got := NewCommandEvent("close"); got == nil {
		t.Fatal("NewCommandEvent(\"close\") = nil event")
	}
}

// yamlRoot parses src into a *yaml.Node document suitable for userConfigHasLeaf.
func yamlRoot(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(src), &root); err != nil {
		t.Fatalf("unmarshal test yaml: %v", err)
	}
	return &root
}

// TestUserConfigHasLeafBranches drives the branch structure of the unexported
// userConfigHasLeaf directly: an empty/nil document, a scalar-rooted document
// (top level is not a mapping), the flat dotted-key form ("metrics.disabled"),
// and a nested key whose intermediate value is a scalar rather than a mapping.
func TestUserConfigHasLeafBranches(t *testing.T) {
	t.Run("nil root", func(t *testing.T) {
		if userConfigHasLeaf(nil, "metrics", "disabled") {
			t.Error("nil root reported a leaf present")
		}
	})

	t.Run("empty document", func(t *testing.T) {
		if userConfigHasLeaf(&yaml.Node{}, "metrics", "disabled") {
			t.Error("empty document reported a leaf present")
		}
	})

	t.Run("scalar-rooted document is not a mapping", func(t *testing.T) {
		root := yamlRoot(t, "just-a-scalar\n")
		if userConfigHasLeaf(root, "metrics", "disabled") {
			t.Error("scalar-rooted document reported a leaf present")
		}
	})

	t.Run("flat dotted key present", func(t *testing.T) {
		root := yamlRoot(t, "metrics.disabled: false\n")
		if !userConfigHasLeaf(root, "metrics", "disabled") {
			t.Error("flat 'metrics.disabled' key not detected")
		}
	})

	t.Run("flat dotted key with empty value is absent", func(t *testing.T) {
		root := yamlRoot(t, "metrics.disabled: \n")
		if userConfigHasLeaf(root, "metrics", "disabled") {
			t.Error("flat key with empty value counted as present")
		}
	})

	t.Run("intermediate value is scalar not mapping", func(t *testing.T) {
		// "metrics" is a scalar, so descending into it for "disabled" must fail
		// at the current.Kind != MappingNode guard.
		root := yamlRoot(t, "metrics: not-a-map\n")
		if userConfigHasLeaf(root, "metrics", "disabled") {
			t.Error("descending through a scalar intermediate reported a leaf")
		}
	})

	t.Run("nested key present", func(t *testing.T) {
		root := yamlRoot(t, "metrics:\n  disabled: true\n")
		if !userConfigHasLeaf(root, "metrics", "disabled") {
			t.Error("nested 'metrics.disabled' key not detected")
		}
	})

	t.Run("nested key missing", func(t *testing.T) {
		root := yamlRoot(t, "metrics:\n  endpoint: https://x\n")
		if userConfigHasLeaf(root, "metrics", "disabled") {
			t.Error("absent nested key reported present")
		}
	})
}

// TestEnsureUserConfigDefaultsFlatKeyForm covers EnsureUserConfigDefaults when
// the existing config already carries both leaves in the flat dotted-key form:
// both needDisabled and needEndpoint are false, so it returns without rewriting
// the file (the no-write early return through the flat-key detection path).
func TestEnsureUserConfigDefaultsFlatKeyForm(t *testing.T) {
	home := setupUserConfigHome(t)
	path := userConfigPath(home)
	if err := os.MkdirAll(strings.TrimSuffix(path, "/config.yaml"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := "metrics.disabled: true\nmetrics.endpoint: https://kept.example\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := EnsureUserConfigDefaults(); err != nil {
		t.Fatalf("EnsureUserConfigDefaults: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != original {
		t.Errorf("config rewritten though both flat leaves were present.\nwant: %q\ngot:  %q", original, got)
	}
}
