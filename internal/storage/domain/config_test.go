package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// fakeConfigRepo is a hermetic ConfigSQLRepository stub. Each method returns
// the configured value/error; unset methods return zero values so a test only
// wires the calls it exercises.
type fakeConfigRepo struct {
	metadata      map[string]string
	metadataErr   error
	config        map[string]string
	configErr     error
	customTypes   []string
	customTypeErr error
	allowed       string
	allowedErr    error
	infra         map[string]bool
	infraErr      error
	allConfig     map[string]string
	allConfigErr  error
	statuses      []types.CustomStatus
	statusErr     error
	statusNames   []string
	statusNameErr error
	adaptiveCfg   AdaptiveIDConfig
	adaptiveErr   error

	setConfigCalls    map[string]string
	deleteConfigCalls []string
}

func (f *fakeConfigRepo) GetMetadata(_ context.Context, key string) (string, error) {
	if f.metadataErr != nil {
		return "", f.metadataErr
	}
	return f.metadata[key], nil
}
func (f *fakeConfigRepo) SetMetadata(context.Context, string, string) error      { return nil }
func (f *fakeConfigRepo) SetLocalMetadata(context.Context, string, string) error { return nil }
func (f *fakeConfigRepo) GetConfig(_ context.Context, key string) (string, error) {
	if f.configErr != nil {
		return "", f.configErr
	}
	return f.config[key], nil
}
func (f *fakeConfigRepo) SetConfig(_ context.Context, key, value string) error {
	if f.setConfigCalls == nil {
		f.setConfigCalls = map[string]string{}
	}
	f.setConfigCalls[key] = value
	return f.configErr
}
func (f *fakeConfigRepo) DeleteConfig(_ context.Context, key string) error {
	f.deleteConfigCalls = append(f.deleteConfigCalls, key)
	return f.configErr
}
func (f *fakeConfigRepo) GetAllConfig(context.Context) (map[string]string, error) {
	return f.allConfig, f.allConfigErr
}
func (f *fakeConfigRepo) GetCustomTypes(context.Context) ([]string, error) {
	return f.customTypes, f.customTypeErr
}
func (f *fakeConfigRepo) GetAllowedPrefixes(context.Context) (string, error) {
	return f.allowed, f.allowedErr
}
func (f *fakeConfigRepo) GetAdaptiveIDConfig(context.Context) (AdaptiveIDConfig, error) {
	return f.adaptiveCfg, f.adaptiveErr
}
func (f *fakeConfigRepo) GetCustomStatuses(context.Context) ([]types.CustomStatus, error) {
	return f.statuses, f.statusErr
}
func (f *fakeConfigRepo) ListAllStatusNames(context.Context) ([]string, error) {
	return f.statusNames, f.statusNameErr
}
func (f *fakeConfigRepo) GetInfraTypes(context.Context) (map[string]bool, error) {
	return f.infra, f.infraErr
}

var errRepo = errors.New("repo boom")

func TestConfigUseCase_VerifyInit(t *testing.T) {
	ctx := context.Background()

	t.Run("fully initialized", func(t *testing.T) {
		uc := NewConfigUseCase(&fakeConfigRepo{
			metadata: map[string]string{"_project_id": "proj-1"},
			config:   map[string]string{"issue_prefix": "bd"},
		})
		got, err := uc.VerifyInit(ctx)
		if err != nil {
			t.Fatalf("VerifyInit err = %v", err)
		}
		if got.ProjectID != "proj-1" || got.IssuePrefix != "bd" {
			t.Errorf("VerifyInit = %+v, want proj-1/bd", got)
		}
		if len(got.Missing) != 0 {
			t.Errorf("Missing = %v, want none", got.Missing)
		}
	})

	t.Run("both fields missing", func(t *testing.T) {
		uc := NewConfigUseCase(&fakeConfigRepo{})
		got, err := uc.VerifyInit(ctx)
		if err != nil {
			t.Fatalf("VerifyInit err = %v", err)
		}
		want := map[string]bool{"metadata._project_id": true, "config.issue_prefix": true}
		if len(got.Missing) != 2 {
			t.Fatalf("Missing = %v, want 2 entries", got.Missing)
		}
		for _, m := range got.Missing {
			if !want[m] {
				t.Errorf("unexpected missing entry %q", m)
			}
		}
	})

	t.Run("metadata read error", func(t *testing.T) {
		uc := NewConfigUseCase(&fakeConfigRepo{metadataErr: errRepo})
		if _, err := uc.VerifyInit(ctx); !errors.Is(err, errRepo) {
			t.Errorf("VerifyInit err = %v, want wrapped errRepo", err)
		}
	})

	t.Run("config read error", func(t *testing.T) {
		uc := NewConfigUseCase(&fakeConfigRepo{
			metadata:  map[string]string{"_project_id": "p"},
			configErr: errRepo,
		})
		if _, err := uc.VerifyInit(ctx); !errors.Is(err, errRepo) {
			t.Errorf("VerifyInit err = %v, want wrapped errRepo", err)
		}
	})
}

func TestConfigUseCase_IsInfraTypeCtx(t *testing.T) {
	ctx := context.Background()

	uc := NewConfigUseCase(&fakeConfigRepo{infra: map[string]bool{"agent": true, "role": true}})
	if ok, err := uc.IsInfraTypeCtx(ctx, types.IssueType("agent")); err != nil || !ok {
		t.Errorf("IsInfraTypeCtx(agent) = %v,%v want true,nil", ok, err)
	}
	if ok, err := uc.IsInfraTypeCtx(ctx, types.TypeBug); err != nil || ok {
		t.Errorf("IsInfraTypeCtx(bug) = %v,%v want false,nil", ok, err)
	}

	ucErr := NewConfigUseCase(&fakeConfigRepo{infraErr: errRepo})
	if _, err := ucErr.IsInfraTypeCtx(ctx, types.TypeBug); !errors.Is(err, errRepo) {
		t.Errorf("IsInfraTypeCtx err = %v, want wrapped errRepo", err)
	}
}

func TestConfigUseCase_Getters(t *testing.T) {
	ctx := context.Background()
	repo := &fakeConfigRepo{
		customTypes: []string{"spike", "chore"},
		statuses:    []types.CustomStatus{{Name: "wip"}},
		statusNames: []string{"open", "wip"},
		infra:       map[string]bool{"agent": true},
		allConfig:   map[string]string{"a": "1"},
		config:      map[string]string{"issue_prefix": "bd"},
	}
	uc := NewConfigUseCase(repo)

	if got, err := uc.GetCustomTypes(ctx); err != nil || len(got) != 2 {
		t.Errorf("GetCustomTypes = %v,%v", got, err)
	}
	if got, err := uc.GetCustomStatuses(ctx); err != nil || len(got) != 1 {
		t.Errorf("GetCustomStatuses = %v,%v", got, err)
	}
	if got, err := uc.ListAllStatusNames(ctx); err != nil || len(got) != 2 {
		t.Errorf("ListAllStatusNames = %v,%v", got, err)
	}
	if got, err := uc.GetInfraTypes(ctx); err != nil || !got["agent"] {
		t.Errorf("GetInfraTypes = %v,%v", got, err)
	}
	if got, err := uc.GetAllConfig(ctx); err != nil || got["a"] != "1" {
		t.Errorf("GetAllConfig = %v,%v", got, err)
	}
	if got, err := uc.GetConfig(ctx, "issue_prefix"); err != nil || got != "bd" {
		t.Errorf("GetConfig = %q,%v", got, err)
	}
}

func TestConfigUseCase_Getters_Errors(t *testing.T) {
	ctx := context.Background()
	uc := NewConfigUseCase(&fakeConfigRepo{
		customTypeErr: errRepo,
		statusErr:     errRepo,
		statusNameErr: errRepo,
		infraErr:      errRepo,
		allConfigErr:  errRepo,
		configErr:     errRepo,
	})

	if _, err := uc.GetCustomTypes(ctx); !errors.Is(err, errRepo) {
		t.Errorf("GetCustomTypes err = %v", err)
	}
	if _, err := uc.GetCustomStatuses(ctx); !errors.Is(err, errRepo) {
		t.Errorf("GetCustomStatuses err = %v", err)
	}
	if _, err := uc.ListAllStatusNames(ctx); !errors.Is(err, errRepo) {
		t.Errorf("ListAllStatusNames err = %v", err)
	}
	if _, err := uc.GetInfraTypes(ctx); !errors.Is(err, errRepo) {
		t.Errorf("GetInfraTypes err = %v", err)
	}
	if _, err := uc.GetAllConfig(ctx); !errors.Is(err, errRepo) {
		t.Errorf("GetAllConfig err = %v", err)
	}
	if _, err := uc.GetConfig(ctx, "k"); !errors.Is(err, errRepo) {
		t.Errorf("GetConfig err = %v", err)
	}
}

func TestConfigUseCase_SetDelete(t *testing.T) {
	ctx := context.Background()
	repo := &fakeConfigRepo{}
	uc := NewConfigUseCase(repo)

	if err := uc.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("SetConfig err = %v", err)
	}
	if repo.setConfigCalls["issue_prefix"] != "bd" {
		t.Errorf("SetConfig not forwarded: %v", repo.setConfigCalls)
	}
	if err := uc.DeleteConfig(ctx, "issue_prefix"); err != nil {
		t.Fatalf("DeleteConfig err = %v", err)
	}
	if len(repo.deleteConfigCalls) != 1 || repo.deleteConfigCalls[0] != "issue_prefix" {
		t.Errorf("DeleteConfig not forwarded: %v", repo.deleteConfigCalls)
	}
}

func TestConfigUseCase_SetDelete_Errors(t *testing.T) {
	ctx := context.Background()
	uc := NewConfigUseCase(&fakeConfigRepo{configErr: errRepo})

	if err := uc.SetConfig(ctx, "k", "v"); !errors.Is(err, errRepo) {
		t.Errorf("SetConfig err = %v, want errRepo", err)
	}
	if err := uc.DeleteConfig(ctx, "k"); !errors.Is(err, errRepo) {
		t.Errorf("DeleteConfig err = %v, want errRepo", err)
	}
}

func TestConfigUseCase_LoadCreateContext(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path", func(t *testing.T) {
		uc := NewConfigUseCase(&fakeConfigRepo{
			config:      map[string]string{"issue_prefix": "bd"},
			allowed:     "bd,hq",
			customTypes: []string{"spike"},
		})
		got, err := uc.LoadCreateContext(ctx)
		if err != nil {
			t.Fatalf("LoadCreateContext err = %v", err)
		}
		if got.IssuePrefix != "bd" || got.AllowedPrefixes != "bd,hq" || len(got.CustomTypes) != 1 {
			t.Errorf("LoadCreateContext = %+v", got)
		}
	})

	t.Run("prefix read error", func(t *testing.T) {
		uc := NewConfigUseCase(&fakeConfigRepo{configErr: errRepo})
		if _, err := uc.LoadCreateContext(ctx); !errors.Is(err, errRepo) {
			t.Errorf("LoadCreateContext err = %v", err)
		}
	})

	t.Run("allowed-prefixes read error", func(t *testing.T) {
		uc := NewConfigUseCase(&fakeConfigRepo{allowedErr: errRepo})
		if _, err := uc.LoadCreateContext(ctx); !errors.Is(err, errRepo) {
			t.Errorf("LoadCreateContext err = %v", err)
		}
	})

	t.Run("custom-types read error", func(t *testing.T) {
		uc := NewConfigUseCase(&fakeConfigRepo{customTypeErr: errRepo})
		if _, err := uc.LoadCreateContext(ctx); !errors.Is(err, errRepo) {
			t.Errorf("LoadCreateContext err = %v", err)
		}
	})
}
