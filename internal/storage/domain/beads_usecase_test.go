package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

// fakeBeadsDirFSRepo is a scriptable BeadsDirFSRepository for driving
// BeadsDirFSUseCase without touching the filesystem.
type fakeBeadsDirFSRepo struct {
	resolution BeadsDirResolution
	isLocal    bool
	cfg        *configfile.Config
	cfgErr     error

	// failOn: name of the first write method that should return an error.
	failOn  string
	callLog []string
}

func (f *fakeBeadsDirFSRepo) err(name string) error {
	f.callLog = append(f.callLog, name)
	if f.failOn == name {
		return errors.New(name + " failed")
	}
	return nil
}

func (f *fakeBeadsDirFSRepo) ResolveBeadsDirPath(ctx context.Context) BeadsDirResolution {
	return f.resolution
}
func (f *fakeBeadsDirFSRepo) BeadsDirIsLocal(ctx context.Context) bool { return f.isLocal }
func (f *fakeBeadsDirFSRepo) CreateBeadsDir(ctx context.Context) error {
	return f.err("CreateBeadsDir")
}
func (f *fakeBeadsDirFSRepo) BeadsDirExists(ctx context.Context) (bool, error) { return false, nil }
func (f *fakeBeadsDirFSRepo) WriteBeadsGitignore(ctx context.Context) error {
	return f.err("WriteBeadsGitignore")
}
func (f *fakeBeadsDirFSRepo) BeadsGitignoreExists(ctx context.Context) (bool, error) {
	return false, nil
}
func (f *fakeBeadsDirFSRepo) WriteProjectGitignore(ctx context.Context) error {
	return f.err("WriteProjectGitignore")
}
func (f *fakeBeadsDirFSRepo) ProjectGitignoreExists(ctx context.Context) (bool, error) {
	return false, nil
}
func (f *fakeBeadsDirFSRepo) WriteInteractionsLog(ctx context.Context) error {
	return f.err("WriteInteractionsLog")
}
func (f *fakeBeadsDirFSRepo) WriteReadme(ctx context.Context) error { return f.err("WriteReadme") }
func (f *fakeBeadsDirFSRepo) WriteMetadataJSON(ctx context.Context, content []byte) error {
	return f.err("WriteMetadataJSON")
}
func (f *fakeBeadsDirFSRepo) ReadMetadataJSON(ctx context.Context) ([]byte, error) { return nil, nil }
func (f *fakeBeadsDirFSRepo) WriteConfigYAML(ctx context.Context, content []byte) error {
	return f.err("WriteConfigYAML")
}
func (f *fakeBeadsDirFSRepo) ReadConfigYAML(ctx context.Context) ([]byte, error) { return nil, nil }
func (f *fakeBeadsDirFSRepo) ReadBeadsConfig(ctx context.Context) (*configfile.Config, error) {
	return f.cfg, f.cfgErr
}
func (f *fakeBeadsDirFSRepo) WriteProxiedServerClientInfo(ctx context.Context, info *configfile.ProxiedServerClientInfo) error {
	return f.err("WriteProxiedServerClientInfo")
}
func (f *fakeBeadsDirFSRepo) ReadProxiedServerClientInfo(ctx context.Context) (*configfile.ProxiedServerClientInfo, error) {
	return nil, nil
}

func TestBeadsDirFSUseCase_ResolveBeadsDir(t *testing.T) {
	t.Parallel()
	repo := &fakeBeadsDirFSRepo{resolution: BeadsDirResolution{BeadsDir: "/tmp/.beads", HasExplicit: true}}
	uc := NewBeadsDirFSUseCase(repo, BeadsDirFSAdapters{})
	got := uc.ResolveBeadsDir(context.Background())
	if got.BeadsDir != "/tmp/.beads" || !got.HasExplicit {
		t.Fatalf("got %+v, want passthrough of repo resolution", got)
	}
}

func TestBeadsDirFSUseCase_ResolveProxiedInit(t *testing.T) {
	t.Parallel()

	t.Run("prefix drives db name when config empty", func(t *testing.T) {
		t.Parallel()
		repo := &fakeBeadsDirFSRepo{
			resolution: BeadsDirResolution{BeadsDir: "/x", HasExplicit: false},
			isLocal:    true,
			cfg:        &configfile.Config{},
		}
		uc := NewBeadsDirFSUseCase(repo, BeadsDirFSAdapters{})
		res, err := uc.ResolveProxiedInit(context.Background(), ResolveProxiedInitParams{Prefix: "my-rig"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if res.DBName != "my_rig" {
			t.Fatalf("DBName = %q, want my_rig (prefix, dashes->underscores)", res.DBName)
		}
		if !res.IsLocal || res.ProjectID == "" {
			t.Fatalf("res = %+v, want IsLocal + generated ProjectID", res)
		}
	})

	t.Run("explicit db flag + config values win", func(t *testing.T) {
		t.Parallel()
		repo := &fakeBeadsDirFSRepo{
			cfg: &configfile.Config{DoltDatabase: "cfgdb", ProjectID: "pid-123"},
		}
		uc := NewBeadsDirFSUseCase(repo, BeadsDirFSAdapters{})
		res, err := uc.ResolveProxiedInit(context.Background(), ResolveProxiedInitParams{DBFlag: "flagdb", Prefix: "p"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if res.DBName != "flagdb" {
			t.Fatalf("DBName = %q, want flagdb (flag wins)", res.DBName)
		}
		if res.ProjectID != "pid-123" {
			t.Fatalf("ProjectID = %q, want pid-123 (config wins)", res.ProjectID)
		}
	})

	t.Run("config-db used when no flag", func(t *testing.T) {
		t.Parallel()
		repo := &fakeBeadsDirFSRepo{cfg: &configfile.Config{DoltDatabase: "cfgdb"}}
		uc := NewBeadsDirFSUseCase(repo, BeadsDirFSAdapters{})
		res, err := uc.ResolveProxiedInit(context.Background(), ResolveProxiedInitParams{})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if res.DBName != "cfgdb" {
			t.Fatalf("DBName = %q, want cfgdb", res.DBName)
		}
	})

	t.Run("default db name when nothing set", func(t *testing.T) {
		t.Parallel()
		repo := &fakeBeadsDirFSRepo{cfg: &configfile.Config{}}
		uc := NewBeadsDirFSUseCase(repo, BeadsDirFSAdapters{})
		res, err := uc.ResolveProxiedInit(context.Background(), ResolveProxiedInitParams{})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if res.DBName != configfile.DefaultDoltDatabase {
			t.Fatalf("DBName = %q, want default %q", res.DBName, configfile.DefaultDoltDatabase)
		}
	})

	t.Run("config read error propagates", func(t *testing.T) {
		t.Parallel()
		repo := &fakeBeadsDirFSRepo{cfgErr: errors.New("read boom")}
		uc := NewBeadsDirFSUseCase(repo, BeadsDirFSAdapters{})
		if _, err := uc.ResolveProxiedInit(context.Background(), ResolveProxiedInitParams{}); err == nil {
			t.Fatal("expected config read error")
		}
	})
}

func TestBeadsDirFSUseCase_InitializeBeadsDir(t *testing.T) {
	t.Parallel()

	t.Run("full happy path writes everything + runs adapters", func(t *testing.T) {
		t.Parallel()
		repo := &fakeBeadsDirFSRepo{resolution: BeadsDirResolution{BeadsDir: "/root/.beads"}}
		var nocowPath, lvPath, lvVer string
		uc := NewBeadsDirFSUseCase(repo, BeadsDirFSAdapters{
			ApplyNoCOW:        func(p string) error { nocowPath = p; return nil },
			WriteLocalVersion: func(p, v string) error { lvPath = p; lvVer = v; return nil },
		})
		res, err := uc.InitializeBeadsDir(context.Background(), InitializeBeadsDirParams{
			MetadataJSONBody:        []byte(`{}`),
			ConfigYAMLBody:          []byte("k: v"),
			ProxiedServerClientInfo: &configfile.ProxiedServerClientInfo{},
			WriteProjectGitignore:   true,
			SetNoCOW:                true,
			LocalVersion:            "1.2.3",
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if res.NoCOWErr != nil || res.LocalVersionErr != nil {
			t.Fatalf("res = %+v, want no adapter errors", res)
		}
		if nocowPath != "/root/.beads" {
			t.Fatalf("ApplyNoCOW path = %q, want /root/.beads", nocowPath)
		}
		if lvVer != "1.2.3" || lvPath != "/root/.beads/.local_version" {
			t.Fatalf("WriteLocalVersion(%q,%q), want (/root/.beads/.local_version,1.2.3)", lvPath, lvVer)
		}
		for _, want := range []string{"CreateBeadsDir", "WriteMetadataJSON", "WriteConfigYAML", "WriteProxiedServerClientInfo", "WriteInteractionsLog", "WriteReadme", "WriteProjectGitignore"} {
			if !contains(repo.callLog, want) {
				t.Fatalf("callLog %v missing %q", repo.callLog, want)
			}
		}
	})

	t.Run("minimal params skip optional writes", func(t *testing.T) {
		t.Parallel()
		repo := &fakeBeadsDirFSRepo{}
		uc := NewBeadsDirFSUseCase(repo, BeadsDirFSAdapters{})
		if _, err := uc.InitializeBeadsDir(context.Background(), InitializeBeadsDirParams{}); err != nil {
			t.Fatalf("err: %v", err)
		}
		for _, unwanted := range []string{"WriteMetadataJSON", "WriteConfigYAML", "WriteProxiedServerClientInfo", "WriteProjectGitignore"} {
			if contains(repo.callLog, unwanted) {
				t.Fatalf("callLog %v should not include %q for empty params", repo.callLog, unwanted)
			}
		}
	})

	t.Run("adapter errors are captured, not returned", func(t *testing.T) {
		t.Parallel()
		repo := &fakeBeadsDirFSRepo{resolution: BeadsDirResolution{BeadsDir: "/d"}}
		uc := NewBeadsDirFSUseCase(repo, BeadsDirFSAdapters{
			ApplyNoCOW:        func(p string) error { return errors.New("nocow boom") },
			WriteLocalVersion: func(p, v string) error { return errors.New("lv boom") },
		})
		res, err := uc.InitializeBeadsDir(context.Background(), InitializeBeadsDirParams{SetNoCOW: true, LocalVersion: "9"})
		if err != nil {
			t.Fatalf("err: %v (adapter errors should be captured in result)", err)
		}
		if res.NoCOWErr == nil || res.LocalVersionErr == nil {
			t.Fatalf("res = %+v, want both adapter errors captured", res)
		}
	})

	t.Run("repo write error aborts", func(t *testing.T) {
		t.Parallel()
		for _, step := range []string{"CreateBeadsDir", "WriteBeadsGitignore", "WriteMetadataJSON", "WriteConfigYAML", "WriteProxiedServerClientInfo", "WriteInteractionsLog", "WriteReadme", "WriteProjectGitignore"} {
			repo := &fakeBeadsDirFSRepo{failOn: step}
			uc := NewBeadsDirFSUseCase(repo, BeadsDirFSAdapters{})
			_, err := uc.InitializeBeadsDir(context.Background(), InitializeBeadsDirParams{
				MetadataJSONBody:        []byte("x"),
				ConfigYAMLBody:          []byte("y"),
				ProxiedServerClientInfo: &configfile.ProxiedServerClientInfo{},
				WriteProjectGitignore:   true,
			})
			if err == nil {
				t.Fatalf("failOn %q: expected error", step)
			}
		}
	})
}

func TestBeadsDirFSUseCase_AdapterWrappers(t *testing.T) {
	t.Parallel()

	t.Run("nil adapters return not-configured errors", func(t *testing.T) {
		t.Parallel()
		uc := NewBeadsDirFSUseCase(&fakeBeadsDirFSRepo{}, BeadsDirFSAdapters{})
		ctx := context.Background()
		checks := []struct {
			name string
			fn   func() error
		}{
			{"SetupForkExclude", func() error { return uc.SetupForkExclude(ctx, false) }},
			{"SetupStealthMode", func() error { return uc.SetupStealthMode(ctx, false) }},
			{"InstallGitHooks", func() error { return uc.InstallGitHooks(ctx, HooksInstallParams{}) }},
			{"InstallJJHooks", func() error { return uc.InstallJJHooks(ctx) }},
			{"AddAgentsInstructions", func() error { return uc.AddAgentsInstructions(ctx, AgentsFileParams{}) }},
			{"InstallClaudeProject", func() error { return uc.InstallClaudeProject(ctx, false) }},
			{"SetYAMLConfig", func() error { return uc.SetYAMLConfig(ctx, "k", "v") }},
		}
		for _, c := range checks {
			if err := c.fn(); err == nil {
				t.Fatalf("%s: expected not-configured error with nil adapter", c.name)
			}
		}
	})

	t.Run("configured adapters are delegated to", func(t *testing.T) {
		t.Parallel()
		var forkV, stealthV bool
		var gotHooks HooksInstallParams
		var jjCalled, claudeStealth bool
		var agentsFile, yamlKey, yamlVal string
		uc := NewBeadsDirFSUseCase(&fakeBeadsDirFSRepo{}, BeadsDirFSAdapters{
			SetupForkExclude:      func(v bool) error { forkV = v; return nil },
			SetupStealthMode:      func(v bool) error { stealthV = v; return nil },
			InstallGitHooks:       func(p HooksInstallParams) error { gotHooks = p; return nil },
			InstallJJHooks:        func() error { jjCalled = true; return nil },
			AddAgentsInstructions: func(p AgentsFileParams) { agentsFile = p.File },
			InstallClaudeProject:  func(s bool) error { claudeStealth = s; return nil },
			SetYAMLConfig:         func(k, v string) error { yamlKey, yamlVal = k, v; return nil },
		})
		ctx := context.Background()
		if err := uc.SetupForkExclude(ctx, true); err != nil || !forkV {
			t.Fatalf("SetupForkExclude: err=%v forkV=%v", err, forkV)
		}
		if err := uc.SetupStealthMode(ctx, true); err != nil || !stealthV {
			t.Fatalf("SetupStealthMode: err=%v stealthV=%v", err, stealthV)
		}
		if err := uc.InstallGitHooks(ctx, HooksInstallParams{Force: true}); err != nil || !gotHooks.Force {
			t.Fatalf("InstallGitHooks: err=%v gotHooks=%+v", err, gotHooks)
		}
		if err := uc.InstallJJHooks(ctx); err != nil || !jjCalled {
			t.Fatalf("InstallJJHooks: err=%v called=%v", err, jjCalled)
		}
		if err := uc.AddAgentsInstructions(ctx, AgentsFileParams{File: "AGENTS.md"}); err != nil || agentsFile != "AGENTS.md" {
			t.Fatalf("AddAgentsInstructions: err=%v file=%q", err, agentsFile)
		}
		if err := uc.InstallClaudeProject(ctx, true); err != nil || !claudeStealth {
			t.Fatalf("InstallClaudeProject: err=%v stealth=%v", err, claudeStealth)
		}
		if err := uc.SetYAMLConfig(ctx, "a", "b"); err != nil || yamlKey != "a" || yamlVal != "b" {
			t.Fatalf("SetYAMLConfig: err=%v key=%q val=%q", err, yamlKey, yamlVal)
		}
	})

	t.Run("adapter error propagates", func(t *testing.T) {
		t.Parallel()
		uc := NewBeadsDirFSUseCase(&fakeBeadsDirFSRepo{}, BeadsDirFSAdapters{
			SetupForkExclude: func(v bool) error { return errors.New("fork boom") },
		})
		if err := uc.SetupForkExclude(context.Background(), false); err == nil {
			t.Fatal("expected adapter error to propagate")
		}
	})
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
