package doltserver

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

// TestIsDebugMode covers the BEADS_DOLT_DEBUG env branch and the default
// (unset → false) path of IsDebugMode.
func TestIsDebugMode(t *testing.T) {
	cases := []struct {
		env  string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"", false},
		{"0", false},
		{"no", false},
	}
	for _, tc := range cases {
		t.Run("env="+tc.env, func(t *testing.T) {
			t.Setenv("BEADS_DOLT_DEBUG", tc.env)
			if got := IsDebugMode(); got != tc.want {
				t.Errorf("IsDebugMode() with BEADS_DOLT_DEBUG=%q = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

// TestDebugProfileDir verifies DebugProfileDir returns an absolute path ending
// in the per-project server dir + dolt-pprof (per-project mode, not shared).
func TestDebugProfileDir(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "") // ensure per-project mode
	beadsDir := t.TempDir()

	got := DebugProfileDir(beadsDir)
	if !filepath.IsAbs(got) {
		t.Errorf("DebugProfileDir(%q) = %q, want absolute path", beadsDir, got)
	}
	if filepath.Base(got) != "dolt-pprof" {
		t.Errorf("DebugProfileDir base = %q, want dolt-pprof", filepath.Base(got))
	}
	// In per-project mode the profile dir lives under beadsDir.
	absBeads, _ := filepath.Abs(beadsDir)
	if !strings.HasPrefix(got, absBeads) {
		t.Errorf("DebugProfileDir = %q, want under %q", got, absBeads)
	}
}

// TestResolveServerDirPerProject covers the exported ResolveServerDir wrapper in
// per-project mode: it returns beadsDir unchanged.
func TestResolveServerDirPerProject(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	beadsDir := t.TempDir()
	if got := ResolveServerDir(beadsDir); got != beadsDir {
		t.Errorf("ResolveServerDir(%q) = %q, want unchanged beadsDir (per-project mode)", beadsDir, got)
	}
}

// TestResolveServerDirSharedMode covers the shared-server branch of the exported
// ResolveServerDir: it returns the shared server dir, not beadsDir.
func TestResolveServerDirSharedMode(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")
	t.Setenv("BEADS_SHARED_SERVER_DIR", shared)

	beadsDir := t.TempDir()
	got := ResolveServerDir(beadsDir)
	if got != shared {
		t.Errorf("ResolveServerDir in shared mode = %q, want %q", got, shared)
	}
	if got == beadsDir {
		t.Errorf("ResolveServerDir in shared mode should not return beadsDir")
	}
}

// TestResolveDoltDirEnvAbsolute covers the BEADS_DOLT_DATA_DIR absolute-path
// branch of ResolveDoltDir (highest priority after shared mode).
func TestResolveDoltDirEnvAbsolute(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	abs := t.TempDir() // TempDir is absolute
	t.Setenv("BEADS_DOLT_DATA_DIR", abs)

	beadsDir := t.TempDir()
	if got := ResolveDoltDir(beadsDir); got != abs {
		t.Errorf("ResolveDoltDir with absolute BEADS_DOLT_DATA_DIR = %q, want %q", got, abs)
	}
}

// TestResolveDoltDirEnvRelative covers the BEADS_DOLT_DATA_DIR relative-path
// branch: the value is joined onto beadsDir.
func TestResolveDoltDirEnvRelative(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "custom-dolt")

	beadsDir := t.TempDir()
	want := filepath.Join(beadsDir, "custom-dolt")
	if got := ResolveDoltDir(beadsDir); got != want {
		t.Errorf("ResolveDoltDir with relative BEADS_DOLT_DATA_DIR = %q, want %q", got, want)
	}
}

// TestResolveDoltDirDefault covers the fall-through: no shared mode, no env, no
// metadata.json → default beadsDir/dolt.
func TestResolveDoltDirDefault(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")

	beadsDir := t.TempDir() // empty dir: no metadata.json present
	want := filepath.Join(beadsDir, "dolt")
	if got := ResolveDoltDir(beadsDir); got != want {
		t.Errorf("ResolveDoltDir default = %q, want %q", got, want)
	}
}

// TestResolveDoltDirMetadataCustomDir covers the metadata.json-present branch of
// ResolveDoltDir: with no env override, a metadata.json carrying a custom
// (relative) dolt_data_dir is loaded and joined onto beadsDir.
func TestResolveDoltDirMetadataCustomDir(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")

	beadsDir := t.TempDir()
	cfg := &configfile.Config{DoltDataDir: "meta-dolt"}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("saving metadata.json: %v", err)
	}

	want := filepath.Join(beadsDir, "meta-dolt")
	if got := ResolveDoltDir(beadsDir); got != want {
		t.Errorf("ResolveDoltDir with metadata dolt_data_dir = %q, want %q", got, want)
	}
}

// TestResolveDoltDirMetadataDefault covers the metadata.json-present branch when
// the config carries no custom data dir: ResolveDoltDir falls back to
// beadsDir/dolt (via configfile.DatabasePath) rather than the no-metadata path.
func TestResolveDoltDirMetadataDefault(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")

	beadsDir := t.TempDir()
	cfg := &configfile.Config{} // metadata.json present but no custom dolt dir
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("saving metadata.json: %v", err)
	}

	want := filepath.Join(beadsDir, "dolt")
	if got := ResolveDoltDir(beadsDir); got != want {
		t.Errorf("ResolveDoltDir with empty metadata = %q, want %q", got, want)
	}
}

// TestResolveDoltDirEnvBeatsMetadata verifies the env var takes priority over a
// metadata.json custom dir (env is checked before metadata.json is loaded).
func TestResolveDoltDirEnvBeatsMetadata(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	beadsDir := t.TempDir()
	cfg := &configfile.Config{DoltDataDir: "meta-dolt"}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("saving metadata.json: %v", err)
	}
	abs := t.TempDir()
	t.Setenv("BEADS_DOLT_DATA_DIR", abs)

	if got := ResolveDoltDir(beadsDir); got != abs {
		t.Errorf("ResolveDoltDir env-over-metadata = %q, want %q", got, abs)
	}
}

// TestResolveDoltDirSharedMode covers the shared-server branch of ResolveDoltDir.
func TestResolveDoltDirSharedMode(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")
	t.Setenv("BEADS_SHARED_SERVER_DIR", shared)

	beadsDir := t.TempDir()
	got := ResolveDoltDir(beadsDir)
	want := filepath.Join(shared, "dolt")
	if got != want {
		t.Errorf("ResolveDoltDir in shared mode = %q, want %q", got, want)
	}
}

// TestLogPath verifies the exported LogPath joins the server log filename onto
// beadsDir.
func TestLogPath(t *testing.T) {
	beadsDir := t.TempDir()
	got := LogPath(beadsDir)
	want := filepath.Join(beadsDir, "dolt-server.log")
	if got != want {
		t.Errorf("LogPath(%q) = %q, want %q", beadsDir, got, want)
	}
}

// TestLockPath covers the unexported lockPath helper (path composition).
func TestLockPath(t *testing.T) {
	beadsDir := t.TempDir()
	got := lockPath(beadsDir)
	want := filepath.Join(beadsDir, "dolt-server.lock")
	if got != want {
		t.Errorf("lockPath(%q) = %q, want %q", beadsDir, got, want)
	}
}

// TestRotateDebugProfileNoProfile verifies rotateDebugProfile is a no-op (no
// panic, no file created) when no cpu.pprof exists — the common non-debug path.
func TestRotateDebugProfileNoProfile(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	beadsDir := t.TempDir()

	// Should silently return: no profile dir / no cpu.pprof.
	rotateDebugProfile(beadsDir)

	// Confirm nothing was created / rotated.
	profDir := DebugProfileDir(beadsDir)
	if entries, err := os.ReadDir(profDir); err == nil && len(entries) > 0 {
		t.Errorf("rotateDebugProfile created %d entries in %q, want none", len(entries), profDir)
	}
}

// TestRotateDebugProfileRenames covers the rotation branch: a non-empty
// cpu.pprof is renamed to a timestamped cpu-<ts>.pprof and the original name is
// consumed. Fully hermetic (no server).
func TestRotateDebugProfileRenames(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	beadsDir := t.TempDir()

	profDir := DebugProfileDir(beadsDir)
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatalf("mkdir profDir: %v", err)
	}
	src := filepath.Join(profDir, "cpu.pprof")
	if err := os.WriteFile(src, []byte("profile-data"), 0o600); err != nil {
		t.Fatalf("write cpu.pprof: %v", err)
	}

	rotateDebugProfile(beadsDir)

	// The original cpu.pprof should be gone, replaced by a rotated copy.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("cpu.pprof still present after rotate (err=%v)", err)
	}
	entries, err := os.ReadDir(profDir)
	if err != nil {
		t.Fatalf("read profDir: %v", err)
	}
	var rotated int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cpu-") && strings.HasSuffix(e.Name(), ".pprof") {
			rotated++
		}
	}
	if rotated != 1 {
		t.Errorf("found %d rotated cpu-*.pprof files, want 1", rotated)
	}
}

// TestRecoverCorruptManifestPositive covers the positive recovery path of the
// unexported recoverCorruptManifest: given a log with the corrupt-manifest
// signature and a noms dir that looks corrupt (manifest present, empty
// journal.idx, empty oldgen), the corrupt .dolt is backed up and reinitialized.
// File-based + `dolt init` only (no sql-server).
func TestRecoverCorruptManifestPositive(t *testing.T) {
	if _, lookErr := exec.LookPath("dolt"); lookErr != nil {
		t.Skip("dolt binary not available; skipping reinit path")
	}
	beadsDir := t.TempDir()

	// Log carrying the corrupt-manifest signature.
	if err := os.WriteFile(logPath(beadsDir),
		[]byte("startup...\nerror: root hash doesn't exist\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	// Corrupt db layout: <doltDir>/dbX/.dolt/noms/{manifest, empty journal.idx}
	doltDir := filepath.Join(beadsDir, "dolt")
	nomsDir := filepath.Join(doltDir, "dbX", ".dolt", "noms")
	if err := os.MkdirAll(filepath.Join(nomsDir, "oldgen"), 0o755); err != nil {
		t.Fatalf("mkdir noms: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nomsDir, "manifest"), []byte("m"), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nomsDir, "journal.idx"), []byte{}, 0o600); err != nil {
		t.Fatalf("write journal.idx: %v", err)
	}

	backups, err := recoverCorruptManifest(beadsDir, doltDir)
	if err != nil {
		t.Fatalf("recoverCorruptManifest err = %v, want nil", err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly 1", backups)
	}
	if !strings.Contains(backups[0], ".corrupt.backup") {
		t.Errorf("backup path %q missing .corrupt.backup suffix", backups[0])
	}
	// The backup dir must exist, and the db must have been reinitialized.
	if _, statErr := os.Stat(backups[0]); statErr != nil {
		t.Errorf("backup dir missing: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(doltDir, "dbX", ".dolt")); statErr != nil {
		t.Errorf("reinitialized .dolt missing: %v", statErr)
	}
}

// TestIsRunningNoPIDFile covers the common case: no PID file present means the
// server is reported not-running with no error.
func TestIsRunningNoPIDFile(t *testing.T) {
	beadsDir := t.TempDir()
	st, err := IsRunning(beadsDir)
	if err != nil {
		t.Fatalf("IsRunning(empty dir) error = %v, want nil", err)
	}
	if st == nil || st.Running {
		t.Errorf("IsRunning(empty dir) = %+v, want Running=false", st)
	}
}

// TestIsRunningCorruptPIDFile covers the corrupt-PID-file branch: a non-numeric
// PID file is treated as stale, cleared, and reported not-running.
func TestIsRunningCorruptPIDFile(t *testing.T) {
	beadsDir := t.TempDir()
	pidFile := filepath.Join(beadsDir, PIDFileName)
	if err := os.WriteFile(pidFile, []byte("not-a-number"), 0o600); err != nil {
		t.Fatalf("writing corrupt PID file: %v", err)
	}

	st, err := IsRunning(beadsDir)
	if err != nil {
		t.Fatalf("IsRunning(corrupt PID) error = %v, want nil", err)
	}
	if st == nil || st.Running {
		t.Errorf("IsRunning(corrupt PID) = %+v, want Running=false", st)
	}
	// The stale PID file should have been removed.
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Errorf("corrupt PID file was not cleared (stat err=%v)", statErr)
	}
}

// TestIsRunningDeadPID covers the dead-process branch: a PID that is not alive
// clears tracked state and reports not-running. PID 2^31-1 is used because it
// is far outside any real PID range on the test host.
func TestIsRunningDeadPID(t *testing.T) {
	beadsDir := t.TempDir()
	pidFile := filepath.Join(beadsDir, PIDFileName)
	portFile := filepath.Join(beadsDir, PortFileName)
	if err := os.WriteFile(pidFile, []byte("2147483646"), 0o600); err != nil {
		t.Fatalf("writing PID file: %v", err)
	}
	if err := os.WriteFile(portFile, []byte("3399"), 0o600); err != nil {
		t.Fatalf("writing port file: %v", err)
	}

	st, err := IsRunning(beadsDir)
	if err != nil {
		t.Fatalf("IsRunning(dead PID) error = %v, want nil", err)
	}
	if st == nil || st.Running {
		t.Errorf("IsRunning(dead PID) = %+v, want Running=false", st)
	}
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Errorf("PID file for dead process was not cleared (stat err=%v)", statErr)
	}
}

// TestDetectCorruptManifestNoLog covers the exported DetectCorruptManifest
// wrapper on its common path: with no dolt server log present, there is no
// corrupt-manifest signature to match, so it returns no matches and no error.
func TestDetectCorruptManifestNoLog(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")

	beadsDir := t.TempDir() // no dolt-server.log
	dirs, err := DetectCorruptManifest(beadsDir)
	if err != nil {
		t.Fatalf("DetectCorruptManifest(no log) error = %v, want nil", err)
	}
	if len(dirs) != 0 {
		t.Errorf("DetectCorruptManifest(no log) = %v, want no matches", dirs)
	}
}

// TestRecoverCorruptManifestNoPrecondition covers the exported
// RecoverCorruptManifest wrapper when the corrupt-manifest precondition does not
// hold (no log signature): it must be a safe no-op returning no backups.
func TestRecoverCorruptManifestNoPrecondition(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")

	beadsDir := t.TempDir()
	backups, err := RecoverCorruptManifest(beadsDir)
	if err != nil {
		t.Fatalf("RecoverCorruptManifest(no precondition) error = %v, want nil", err)
	}
	if len(backups) != 0 {
		t.Errorf("RecoverCorruptManifest(no precondition) = %v, want no backups", backups)
	}
}

// TestStopWithForceNotRunning covers the not-running branch of StopWithForce:
// with no server, it returns ErrServerNotRunning and cleans up leftover state
// files (here, none exist). No live server is touched.
func TestStopWithForceNotRunning(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	beadsDir := t.TempDir()

	err := StopWithForce(beadsDir, false)
	if !errors.Is(err, ErrServerNotRunning) {
		t.Errorf("StopWithForce(no server) err = %v, want ErrServerNotRunning", err)
	}
}

// TestEnsureRunningDetailedExternalMode covers the External-mode branch of
// EnsureRunningDetailed: an explicit dolt_server_port in metadata.json marks the
// server externally managed, so auto-start is suppressed and it returns an error
// without starting anything.
func TestEnsureRunningDetailedExternalMode(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_AUTO_START", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")

	beadsDir := t.TempDir()
	cfg := &configfile.Config{DoltServerPort: 3399} // explicit port => external
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("saving metadata.json: %v", err)
	}

	port, startedByUs, err := EnsureRunningDetailed(beadsDir)
	if err == nil {
		t.Fatalf("EnsureRunningDetailed(external) err = nil, want suppressed-auto-start error")
	}
	if startedByUs {
		t.Errorf("EnsureRunningDetailed(external) startedByUs = true, want false")
	}
	if port != 0 {
		t.Errorf("EnsureRunningDetailed(external) port = %d, want 0", port)
	}
}

// TestEnsureRunningDetailedAutoStartDisabled covers the auto-start-disabled
// branch: BEADS_DOLT_AUTO_START=0 suppresses spawning even in owned mode.
func TestEnsureRunningDetailedAutoStartDisabled(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_AUTO_START", "0")

	beadsDir := t.TempDir() // no metadata.json => owned mode, but auto-start off

	port, startedByUs, err := EnsureRunningDetailed(beadsDir)
	if err == nil {
		t.Fatalf("EnsureRunningDetailed(auto-start off) err = nil, want disabled error")
	}
	if startedByUs || port != 0 {
		t.Errorf("EnsureRunningDetailed(auto-start off) = (port=%d, startedByUs=%v), want (0, false)", port, startedByUs)
	}
}

// TestKillStaleServersAutoStartDisabled covers the no-op guard of
// KillStaleServers: when auto-start is disabled the server is externally
// managed and must never be killed.
func TestKillStaleServersAutoStartDisabled(t *testing.T) {
	t.Setenv("BEADS_DOLT_AUTO_START", "0")
	beadsDir := t.TempDir()

	killed, err := KillStaleServers(beadsDir)
	if err != nil {
		t.Fatalf("KillStaleServers(auto-start off) err = %v, want nil", err)
	}
	if len(killed) != 0 {
		t.Errorf("KillStaleServers(auto-start off) killed %v, want none", killed)
	}
}

// TestKillStaleServersForDirGuards exercises killStaleServersForDir's pure
// decision logic with injected process-list/inDir/kill funcs (no real
// processes): empty list, external mode, and missing-PID-file all yield no
// kills; a valid canonical PID kills only OTHER in-dir orphans.
func TestKillStaleServersForDirGuards(t *testing.T) {
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_AUTO_START", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")

	noKill := func(int) error { t.Fatalf("kill must not be called"); return nil }
	allIn := func(int, string) bool { return true }

	t.Run("empty pid list", func(t *testing.T) {
		beadsDir := t.TempDir()
		killed, err := killStaleServersForDir(beadsDir, nil, allIn, noKill)
		if err != nil || len(killed) != 0 {
			t.Errorf("empty list = (%v, %v), want (nil, nil)", killed, err)
		}
	})

	t.Run("external mode", func(t *testing.T) {
		beadsDir := t.TempDir()
		cfg := &configfile.Config{DoltServerPort: 3399}
		if err := cfg.Save(beadsDir); err != nil {
			t.Fatalf("save: %v", err)
		}
		killed, err := killStaleServersForDir(beadsDir, []int{111, 222}, allIn, noKill)
		if err != nil || len(killed) != 0 {
			t.Errorf("external mode = (%v, %v), want (nil, nil)", killed, err)
		}
	})

	t.Run("no pid file", func(t *testing.T) {
		beadsDir := t.TempDir() // owned mode, but no PID file
		killed, err := killStaleServersForDir(beadsDir, []int{111}, allIn, noKill)
		if err != nil || len(killed) != 0 {
			t.Errorf("no pid file = (%v, %v), want (nil, nil)", killed, err)
		}
	})

	t.Run("kills other in-dir orphan, preserves canonical", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(beadsDir, PIDFileName), []byte("111"), 0o600); err != nil {
			t.Fatalf("write pid: %v", err)
		}
		var killedArg []int
		kill := func(pid int) error { killedArg = append(killedArg, pid); return nil }
		killed, err := killStaleServersForDir(beadsDir, []int{111, 222}, allIn, kill)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		// 111 is canonical (preserved); 222 is an in-dir orphan (killed).
		if len(killed) != 1 || killed[0] != 222 {
			t.Errorf("killed = %v, want [222]", killed)
		}
		if len(killedArg) != 1 || killedArg[0] != 222 {
			t.Errorf("kill called with %v, want [222]", killedArg)
		}
	})
}
