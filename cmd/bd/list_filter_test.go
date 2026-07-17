package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/types"
)

func TestListFilterConfig_CustomStatusNames(t *testing.T) {
	cfg := listFilterConfig{customStatuses: []types.CustomStatus{
		{Name: "triage", Category: types.CategoryActive},
		{Name: "shipped", Category: types.CategoryDone},
	}}
	got := cfg.customStatusNames()
	want := []string{"triage", "shipped"}
	if len(got) != len(want) {
		t.Fatalf("customStatusNames() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("customStatusNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Empty case returns an empty (non-nil) slice.
	if names := (listFilterConfig{}).customStatusNames(); len(names) != 0 {
		t.Errorf("empty customStatusNames() = %v, want empty", names)
	}
}

func TestListFilterConfig_InfraTypes(t *testing.T) {
	// Empty infraSet falls back to the domain defaults.
	def := listFilterConfig{}.infraTypes()
	if len(def) == 0 {
		t.Fatal("infraTypes() with no infraSet should return domain defaults, got empty")
	}
	seen := map[string]bool{}
	for _, x := range def {
		seen[x] = true
	}
	for _, want := range []string{"agent", "role", "message"} {
		if !seen[want] {
			t.Errorf("default infraTypes() missing %q (got %v)", want, def)
		}
	}

	// Explicit infraSet is enumerated.
	cfg := listFilterConfig{infraSet: map[string]bool{"widget": true}}
	got := cfg.infraTypes()
	if len(got) != 1 || got[0] != "widget" {
		t.Errorf("infraTypes() = %v, want [widget]", got)
	}
}

func TestListFilterConfig_IsInfra(t *testing.T) {
	// No custom set: delegate to domain defaults.
	base := listFilterConfig{}
	if !base.isInfra("agent") {
		t.Error("isInfra(agent) with defaults = false, want true")
	}
	if base.isInfra("bug") {
		t.Error("isInfra(bug) with defaults = true, want false")
	}

	// Custom set overrides the defaults entirely.
	cfg := listFilterConfig{infraSet: map[string]bool{"widget": true}}
	if !cfg.isInfra("widget") {
		t.Error("isInfra(widget) with custom set = false, want true")
	}
	if cfg.isInfra("agent") {
		t.Error("isInfra(agent) with custom set (no agent) = true, want false")
	}
}

func TestValidStatusList(t *testing.T) {
	base := validStatusList(nil)
	if base == "" {
		t.Fatal("validStatusList(nil) is empty")
	}
	for _, want := range []string{"open", "in_progress", "closed", "pinned", "hooked"} {
		if !strings.Contains(base, want) {
			t.Errorf("validStatusList(nil) missing %q: %s", want, base)
		}
	}

	withCustom := validStatusList([]string{"triage", "review"})
	if !strings.Contains(withCustom, "triage") || !strings.Contains(withCustom, "review") {
		t.Errorf("validStatusList with custom missing entries: %s", withCustom)
	}
}

// --- loadListFilterConfig / loadDirectListFilterConfig ---

type fakeConfigSource struct {
	statuses []types.CustomStatus
	types_   []string
	infra    map[string]bool
	errAt    string // "statuses" | "types" | "infra" | ""
}

func (f fakeConfigSource) GetCustomStatuses(context.Context) ([]types.CustomStatus, error) {
	if f.errAt == "statuses" {
		return nil, errors.New("boom-statuses")
	}
	return f.statuses, nil
}
func (f fakeConfigSource) GetCustomTypes(context.Context) ([]string, error) {
	if f.errAt == "types" {
		return nil, errors.New("boom-types")
	}
	return f.types_, nil
}
func (f fakeConfigSource) GetInfraTypes(context.Context) (map[string]bool, error) {
	if f.errAt == "infra" {
		return nil, errors.New("boom-infra")
	}
	return f.infra, nil
}

func TestLoadListFilterConfig_Success(t *testing.T) {
	src := fakeConfigSource{
		statuses: []types.CustomStatus{{Name: "triage", Category: types.CategoryActive}},
		types_:   []string{"spike"},
		infra:    map[string]bool{"widget": true},
	}
	cfg, err := loadListFilterConfig(context.Background(), src)
	if err != nil {
		t.Fatalf("loadListFilterConfig() error = %v", err)
	}
	if len(cfg.customStatuses) != 1 || cfg.customStatuses[0].Name != "triage" {
		t.Errorf("customStatuses = %v", cfg.customStatuses)
	}
	if len(cfg.customTypes) != 1 || cfg.customTypes[0] != "spike" {
		t.Errorf("customTypes = %v", cfg.customTypes)
	}
	if !cfg.infraSet["widget"] {
		t.Errorf("infraSet = %v, want widget:true", cfg.infraSet)
	}
}

func TestLoadListFilterConfig_EmptyCustomTypesFallsBackToYAML(t *testing.T) {
	// With no DB custom types, the loader falls back to the YAML-config list.
	// We don't assert a specific value (env-dependent) — just that it does not
	// carry the (empty) DB result forward when the fallback is non-empty, and
	// that the call succeeds.
	src := fakeConfigSource{types_: nil}
	cfg, err := loadListFilterConfig(context.Background(), src)
	if err != nil {
		t.Fatalf("loadListFilterConfig() error = %v", err)
	}
	// customTypes should equal whatever the YAML fallback returns.
	yaml := config.GetCustomTypesFromYAML()
	if len(cfg.customTypes) != len(yaml) {
		t.Errorf("customTypes len = %d, want YAML-fallback len %d", len(cfg.customTypes), len(yaml))
	}
}

func TestLoadListFilterConfig_Errors(t *testing.T) {
	for _, at := range []string{"statuses", "types", "infra"} {
		t.Run(at, func(t *testing.T) {
			_, err := loadListFilterConfig(context.Background(), fakeConfigSource{errAt: at})
			if err == nil {
				t.Fatalf("expected error when %s source fails", at)
			}
		})
	}
}

func TestLoadDirectListFilterConfig_NilStore(t *testing.T) {
	// A nil store short-circuits to the YAML custom-types default with no DB call.
	cfg, err := loadDirectListFilterConfig(context.Background(), nil)
	if err != nil {
		t.Fatalf("loadDirectListFilterConfig(nil) error = %v", err)
	}
	if len(cfg.customStatuses) != 0 {
		t.Errorf("nil-store customStatuses = %v, want empty", cfg.customStatuses)
	}
	if cfg.infraSet != nil {
		t.Errorf("nil-store infraSet = %v, want nil", cfg.infraSet)
	}
}
