package molecules

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// isolateMoleculeEnv points HOME and GT_ROOT at empty temp dirs so the path
// resolvers are deterministic regardless of the host's real home / orchestrator
// state. Returns the isolated home and gt-root dirs for the caller to populate.
func isolateMoleculeEnv(t *testing.T) (home, gtRoot string) {
	t.Helper()
	home = t.TempDir()
	gtRoot = t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir consults USERPROFILE on Windows; the CI/crew fleet is
	// linux, but set it too so the test is portable.
	t.Setenv("USERPROFILE", home)
	t.Setenv("GT_ROOT", gtRoot)
	return home, gtRoot
}

func TestGetTownMoleculesPath(t *testing.T) {
	t.Run("no GT_ROOT returns empty", func(t *testing.T) {
		t.Setenv("GT_ROOT", "")
		if got := getTownMoleculesPath(); got != "" {
			t.Errorf("getTownMoleculesPath() = %q, want empty when GT_ROOT unset", got)
		}
	})

	t.Run("GT_ROOT set but no molecules file returns empty", func(t *testing.T) {
		_, gtRoot := isolateMoleculeEnv(t)
		// .beads/molecules.jsonl does not exist under gtRoot.
		if got := getTownMoleculesPath(); got != "" {
			t.Errorf("getTownMoleculesPath() = %q, want empty when file absent", got)
		}
		_ = gtRoot
	})

	t.Run("GT_ROOT with molecules file returns its path", func(t *testing.T) {
		_, gtRoot := isolateMoleculeEnv(t)
		beadsDir := filepath.Join(gtRoot, ".beads")
		if err := os.MkdirAll(beadsDir, 0750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		want := filepath.Join(beadsDir, MoleculeFileName)
		if err := os.WriteFile(want, []byte(""), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := getTownMoleculesPath(); got != want {
			t.Errorf("getTownMoleculesPath() = %q, want %q", got, want)
		}
	})
}

func TestGetUserMoleculesPath(t *testing.T) {
	t.Run("no molecules file returns empty", func(t *testing.T) {
		isolateMoleculeEnv(t)
		if got := getUserMoleculesPath(); got != "" {
			t.Errorf("getUserMoleculesPath() = %q, want empty when file absent", got)
		}
	})

	t.Run("molecules file present returns its path", func(t *testing.T) {
		home, _ := isolateMoleculeEnv(t)
		beadsDir := filepath.Join(home, ".beads")
		if err := os.MkdirAll(beadsDir, 0750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		want := filepath.Join(beadsDir, MoleculeFileName)
		if err := os.WriteFile(want, []byte(""), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := getUserMoleculesPath(); got != want {
			t.Errorf("getUserMoleculesPath() = %q, want %q", got, want)
		}
	})
}

func TestNewLoader(t *testing.T) {
	// NewLoader is a trivial constructor; a nil store is acceptable here since
	// we only assert the loader is wired up (LoadAll's store use is covered by
	// the DB-backed tests).
	l := NewLoader(nil)
	if l == nil {
		t.Fatal("NewLoader(nil) = nil, want non-nil *Loader")
	}
	if l.store != nil {
		t.Errorf("NewLoader(nil).store = %v, want nil", l.store)
	}
}

// TestLoadAll_NoCatalogs exercises LoadAll's control flow when there are no
// molecules to load anywhere: built-ins are empty, and town/user/project
// catalog files are all absent. In that case the store is never touched, so a
// nil store is safe and the result must be empty (no sources, nothing loaded).
func TestLoadAll_NoCatalogs(t *testing.T) {
	isolateMoleculeEnv(t) // HOME + GT_ROOT point at empty temp dirs

	l := NewLoader(nil)

	t.Run("empty beadsDir", func(t *testing.T) {
		result, err := l.LoadAll(context.Background(), "")
		if err != nil {
			t.Fatalf("LoadAll: %v", err)
		}
		if result.Loaded != 0 {
			t.Errorf("Loaded = %d, want 0", result.Loaded)
		}
		if result.BuiltinCount != 0 {
			t.Errorf("BuiltinCount = %d, want 0", result.BuiltinCount)
		}
		if len(result.Sources) != 0 {
			t.Errorf("Sources = %v, want empty", result.Sources)
		}
	})

	t.Run("beadsDir without molecules.jsonl", func(t *testing.T) {
		beadsDir := t.TempDir() // exists but has no molecules.jsonl
		result, err := l.LoadAll(context.Background(), beadsDir)
		if err != nil {
			t.Fatalf("LoadAll: %v", err)
		}
		if result.Loaded != 0 {
			t.Errorf("Loaded = %d, want 0", result.Loaded)
		}
		if len(result.Sources) != 0 {
			t.Errorf("Sources = %v, want empty", result.Sources)
		}
	})
}

// TestLoadMoleculesFromFile_MalformedLines verifies loadMoleculesFromFile skips
// blank and unparseable JSON lines while loading the valid ones, and sets
// IsTemplate on every returned molecule.
func TestLoadMoleculesFromFile_MalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, MoleculeFileName)
	content := `{"id":"mol-good-1","title":"Good One","issue_type":"molecule","status":"open"}

{ this is not valid json }
{"id":"mol-good-2","title":"Good Two","issue_type":"molecule","status":"open"}
   `
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	molecules, err := loadMoleculesFromFile(path)
	if err != nil {
		t.Fatalf("loadMoleculesFromFile: %v", err)
	}
	if len(molecules) != 2 {
		t.Fatalf("got %d molecules, want 2 (blank + malformed lines skipped)", len(molecules))
	}
	for _, m := range molecules {
		if !m.IsTemplate {
			t.Errorf("molecule %s: IsTemplate = false, want true", m.ID)
		}
	}
	if molecules[0].ID != "mol-good-1" || molecules[1].ID != "mol-good-2" {
		t.Errorf("unexpected ids: %q, %q", molecules[0].ID, molecules[1].ID)
	}
}
