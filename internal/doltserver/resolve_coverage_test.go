package doltserver

import (
	"os"
	"path/filepath"
	"testing"
)

// -- IsDebugMode (env + config) --

func TestIsDebugMode_Env(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "True"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("BEADS_DOLT_DEBUG", v)
			if !IsDebugMode() {
				t.Errorf("IsDebugMode() = false for BEADS_DOLT_DEBUG=%q, want true", v)
			}
		})
	}
	t.Run("unset", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_DEBUG", "")
		// With the env cleared and no config key set, debug mode is off.
		if IsDebugMode() {
			t.Error("IsDebugMode() = true with env cleared, want false")
		}
	})
	t.Run("non-truthy", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_DEBUG", "0")
		if IsDebugMode() {
			t.Error("IsDebugMode() = true for BEADS_DOLT_DEBUG=0, want false")
		}
	})
}

// -- DebugProfileDir (pure path derivation) --

func TestDebugProfileDir(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "") // force per-project (beadsDir) resolution

	beadsDir := t.TempDir()
	got := DebugProfileDir(beadsDir)
	want := filepath.Join(beadsDir, "dolt-pprof")
	if got != want {
		t.Errorf("DebugProfileDir(%q) = %q, want %q", beadsDir, got, want)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("DebugProfileDir returned non-absolute path %q", got)
	}
}

// -- rotateDebugProfile (best-effort; exercises the no-profile early return
// and the rename path) --

func TestRotateDebugProfile_NoProfile_Noop(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	beadsDir := t.TempDir()
	// No cpu.pprof exists → early return, no panic, nothing created.
	rotateDebugProfile(beadsDir)
	profDir := DebugProfileDir(beadsDir)
	if entries, err := os.ReadDir(profDir); err == nil && len(entries) > 0 {
		t.Errorf("rotateDebugProfile created %d entries for a missing profile", len(entries))
	}
}

func TestRotateDebugProfile_RotatesExisting(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	beadsDir := t.TempDir()
	profDir := DebugProfileDir(beadsDir)
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatalf("mkdir profDir: %v", err)
	}
	src := filepath.Join(profDir, debugProfileFilename)
	if err := os.WriteFile(src, []byte("profile-bytes"), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	rotateDebugProfile(beadsDir)

	// The live profile should be renamed away (a rotated cpu-<ts>.pprof remains).
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("cpu.pprof still present after rotate (err=%v)", err)
	}
	entries, err := os.ReadDir(profDir)
	if err != nil {
		t.Fatalf("read profDir: %v", err)
	}
	var rotated int
	for _, e := range entries {
		if e.Name() != debugProfileFilename {
			rotated++
		}
	}
	if rotated != 1 {
		t.Errorf("want exactly 1 rotated profile, got %d", rotated)
	}
}

func TestRotateDebugProfile_EmptyProfile_Noop(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	beadsDir := t.TempDir()
	profDir := DebugProfileDir(beadsDir)
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatalf("mkdir profDir: %v", err)
	}
	src := filepath.Join(profDir, debugProfileFilename)
	if err := os.WriteFile(src, nil, 0o644); err != nil {
		t.Fatalf("write empty profile: %v", err)
	}

	rotateDebugProfile(beadsDir)

	// A zero-byte profile is not worth rotating; it stays put.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("empty cpu.pprof should be left in place, stat err=%v", err)
	}
}

// -- ResolveDoltDir (env override + default) --

func TestResolveDoltDir_Default(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	beadsDir := t.TempDir()
	// No metadata.json → default .beads/dolt path.
	got := ResolveDoltDir(beadsDir)
	want := filepath.Join(beadsDir, "dolt")
	if got != want {
		t.Errorf("ResolveDoltDir(%q) = %q, want %q", beadsDir, got, want)
	}
}

func TestResolveDoltDir_EnvAbsolute(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	abs := t.TempDir()
	t.Setenv("BEADS_DOLT_DATA_DIR", abs)
	beadsDir := t.TempDir()
	if got := ResolveDoltDir(beadsDir); got != abs {
		t.Errorf("ResolveDoltDir with absolute env = %q, want %q", got, abs)
	}
}

func TestResolveDoltDir_EnvRelative(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "custom-dolt")
	beadsDir := t.TempDir()
	got := ResolveDoltDir(beadsDir)
	want := filepath.Join(beadsDir, "custom-dolt")
	if got != want {
		t.Errorf("ResolveDoltDir with relative env = %q, want %q", got, want)
	}
}

// -- LogPath --

func TestLogPath(t *testing.T) {
	beadsDir := t.TempDir()
	got := LogPath(beadsDir)
	want := filepath.Join(beadsDir, "dolt-server.log")
	if got != want {
		t.Errorf("LogPath(%q) = %q, want %q", beadsDir, got, want)
	}
}

// -- DetectCorruptManifest exported wrapper (no log signature → nil) --

func TestDetectCorruptManifest_NoLog_ReturnsNil(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	beadsDir := t.TempDir()
	// No dolt-server.log present → logHasCorruptManifestError is false → nil.
	dirs, err := DetectCorruptManifest(beadsDir)
	if err != nil {
		t.Fatalf("DetectCorruptManifest: unexpected error %v", err)
	}
	if dirs != nil {
		t.Errorf("DetectCorruptManifest = %v, want nil (no log signature)", dirs)
	}
}
