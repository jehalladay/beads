package scripts_test

// Regression guards for beads-r06.7: the release + cross-compile path folded
// under the forge gate. These lock the release contract so a future edit cannot
// silently drop a platform, strip the version-stamping ldflags / gms_pure_go
// tag, break the forge release entrypoint, or delete the preserved release
// gates. They parse the checked-in config (no goreleaser / cross toolchain
// required), so they run in CI and on a bare cluster node identically.
//
// Split (per the r06.7 PM directive): the goreleaser/macOS jobs actually cut
// the full binary matrix (that needs cross-gccs + goreleaser, CI-only). This
// file is the FINAL contract the forge release verb + release flow must honor,
// and it is buildable/testable NOW.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// readRepoFile reads a file relative to the repo root, failing the test on error.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join(sourceRepoRoot(t), rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// goreleaserConfig is a partial view of .goreleaser.yml sufficient to assert the
// build matrix contract without depending on goreleaser's own schema types.
type goreleaserConfig struct {
	Builds []struct {
		ID      string   `yaml:"id"`
		Goos    []string `yaml:"goos"`
		Goarch  []string `yaml:"goarch"`
		Tags    []string `yaml:"tags"`
		Ldflags []string `yaml:"ldflags"`
		Env     []string `yaml:"env"`
	} `yaml:"builds"`
}

func loadGoreleaser(t *testing.T) goreleaserConfig {
	t.Helper()
	var cfg goreleaserConfig
	if err := yaml.Unmarshal([]byte(readRepoFile(t, ".goreleaser.yml")), &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yml: %v", err)
	}
	if len(cfg.Builds) == 0 {
		t.Fatal(".goreleaser.yml declares no builds")
	}
	return cfg
}

// TestGoreleaserCoversReleaseMatrix asserts the release build matrix still
// covers the platforms beads ships. linux/amd64 is called out explicitly in the
// r06.7 acceptance criteria; the rest guard against a platform silently
// disappearing from the goreleaser config.
func TestGoreleaserCoversReleaseMatrix(t *testing.T) {
	cfg := loadGoreleaser(t)

	// os/arch pairs that must be produced by goreleaser (darwin is built by the
	// dedicated goreleaser-macos CI job, asserted separately).
	want := map[string]bool{
		"linux/amd64":   false,
		"linux/arm64":   false,
		"windows/amd64": false,
		"freebsd/amd64": false,
		"android/arm64": false,
	}
	for _, b := range cfg.Builds {
		for _, goos := range b.Goos {
			for _, goarch := range b.Goarch {
				want[goos+"/"+goarch] = true
			}
		}
	}
	for pair, found := range want {
		if !found {
			t.Errorf(".goreleaser.yml no longer builds %s (release matrix regression)", pair)
		}
	}
}

// TestGoreleaserBuildsUseGmsPureGoTag asserts every goreleaser build carries the
// gms_pure_go tag. Dropping it re-links ICU into the release binary — the exact
// portability regression the ICU policy + release.yml ldd check exist to catch.
func TestGoreleaserBuildsUseGmsPureGoTag(t *testing.T) {
	cfg := loadGoreleaser(t)
	for _, b := range cfg.Builds {
		found := false
		for _, tag := range b.Tags {
			if tag == "gms_pure_go" {
				found = true
			}
		}
		if !found {
			t.Errorf("goreleaser build %q missing gms_pure_go tag", b.ID)
		}
	}
}

// TestGoreleaserBuildsVersionStamp asserts every goreleaser build stamps the
// version constants via ldflags. An unstamped release binary reports Build="dev"
// / Version="1.1.0-rc.1" regardless of the tag — a silent provenance regression.
func TestGoreleaserBuildsVersionStamp(t *testing.T) {
	cfg := loadGoreleaser(t)
	for _, b := range cfg.Builds {
		joined := strings.Join(b.Ldflags, "\n")
		for _, sym := range []string{"main.Version=", "main.Build="} {
			if !strings.Contains(joined, sym) {
				t.Errorf("goreleaser build %q ldflags do not stamp %s", b.ID, sym)
			}
		}
	}
}

// TestMacosReleaseJobBuildsDarwinArm64 asserts the release workflow's macOS job
// still cross/native-builds darwin arm64 AND amd64 with the same gms_pure_go tag
// and version stamping. darwin-arm64 is named directly in the r06.7 acceptance
// criteria; it is produced here (not by goreleaser) because embedded Dolt needs
// CGO on a native mac.
func TestMacosReleaseJobBuildsDarwinArm64(t *testing.T) {
	wf := readRepoFile(t, ".github/workflows/release.yml")
	if !strings.Contains(wf, "goreleaser-macos:") {
		t.Fatal("release.yml no longer defines the goreleaser-macos job")
	}
	for _, arch := range []string{"arm64", "amd64"} {
		if !strings.Contains(wf, "GOARCH: "+arch) {
			t.Errorf("release.yml macOS job no longer builds darwin/%s", arch)
		}
	}
	if !strings.Contains(wf, "gms_pure_go") {
		t.Error("release.yml macOS build dropped the gms_pure_go tag")
	}
	if !strings.Contains(wf, "-X main.Version=") || !strings.Contains(wf, "-X main.Build=") {
		t.Error("release.yml macOS build no longer stamps main.Version / main.Build")
	}
	// Portability guard: the macOS build must reject a leaked ICU dependency.
	if !strings.Contains(wf, "otool -L") || !strings.Contains(wf, "icu") {
		t.Error("release.yml macOS job no longer verifies the absence of an ICU runtime dependency")
	}
}

// TestForgeReleaseVerbWiredToMake asserts forge.toml maps the release + package
// verbs onto the real make targets, so `forge release` runs the version-stamped
// gms_pure_go build rather than forge's bare `go build ./...` default. This is
// the r06.7 forge entrypoint; it composes with the r06.2 build/test/lint/check
// mapping under the same [build.commands] table.
func TestForgeReleaseVerbWiredToMake(t *testing.T) {
	toml := readRepoFile(t, "forge.toml")
	// Must declare the release + package verbs in [build.commands].
	for _, verb := range []string{"release", "package"} {
		re := regexp.MustCompile(`(?m)^\s*` + verb + `\s*=`)
		if !re.MatchString(toml) {
			t.Errorf("forge.toml [build.commands] does not define the %q verb", verb)
		}
	}
	// The release verb must route through make (the real gms_pure_go+ldflags
	// build contract), not a bare go build.
	relLine := findCommandLine(toml, "release")
	if relLine == "" {
		t.Fatal("forge.toml has no release command line to inspect")
	}
	if !strings.Contains(relLine, "make ") {
		t.Errorf("forge.toml release verb does not route through make: %q", relLine)
	}
}

// findCommandLine returns the RHS of a `verb = "..."` assignment in forge.toml.
func findCommandLine(toml, verb string) string {
	re := regexp.MustCompile(`(?m)^\s*` + verb + `\s*=\s*"([^"]*)"`)
	m := re.FindStringSubmatch(toml)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// TestMakefileReleaseBuildTarget asserts the Makefile exposes the release-build
// target that forge release maps onto, and that it version-stamps all four
// provenance symbols with the gms_pure_go tag. This is the local, buildable-now
// producer of the version-stamped host-arch binary (linux/amd64 on a linux
// runner, darwin/arm64 on a mac) that mirrors the goreleaser ldflags.
func TestMakefileReleaseBuildTarget(t *testing.T) {
	mk := readRepoFile(t, "Makefile")
	if !regexp.MustCompile(`(?m)^release-build:`).MatchString(mk) {
		t.Fatal("Makefile has no release-build target")
	}
	// The target's recipe (up to the next blank line) must stamp all four
	// release provenance symbols and carry the build tag. The recipe uses make
	// variables ($(RELEASE_LDFLAGS), $(BUILD_TAGS)) for these, so expand
	// simply-defined vars first — the guard then follows the indirection.
	recipe := expandMakeVars(mk, makeRecipe(mk, "release-build"))
	if recipe == "" {
		t.Fatal("release-build target has an empty recipe")
	}
	for _, sym := range []string{"main.Version=", "main.Build=", "main.Commit=", "main.Branch="} {
		if !strings.Contains(recipe, sym) {
			t.Errorf("release-build recipe does not stamp %s", sym)
		}
	}
	if !strings.Contains(recipe, "gms_pure_go") {
		t.Error("release-build recipe does not build with -tags gms_pure_go")
	}
}

// expandMakeVars resolves `$(VAR)` references in text using simply-expanded
// (`VAR := ...` / `VAR ?= ...`) definitions found in mk. It iterates a few
// passes to resolve nested references (e.g. RELEASE_LDFLAGS referencing
// GIT_BUILD). Unknown vars are left as-is.
func expandMakeVars(mk, text string) string {
	defs := map[string]string{}
	// Match `NAME := value` or `NAME ?= value` (single-line; continuation
	// backslashes are collapsed first).
	collapsed := strings.ReplaceAll(mk, "\\\n", " ")
	re := regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*[:?]?=\s*(.*)$`)
	for _, m := range re.FindAllStringSubmatch(collapsed, -1) {
		if _, seen := defs[m[1]]; !seen {
			defs[m[1]] = m[2]
		}
	}
	out := text
	for i := 0; i < 5; i++ {
		before := out
		for name, val := range defs {
			out = strings.ReplaceAll(out, "$("+name+")", val)
		}
		if out == before {
			break
		}
	}
	return out
}

// makeRecipe extracts the recipe body of a Makefile target: every line after
// `target:` up to the next target header (a column-0 `name:` line). This
// deliberately includes col-0 `ifeq/else/endif` conditional directives and the
// tab-indented commands they guard, so a recipe that puts its real build command
// inside a platform conditional is still captured.
func makeRecipe(mk, target string) string {
	lines := strings.Split(mk, "\n")
	targetHeader := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*:`)
	var out []string
	inTarget := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, target+":") {
			inTarget = true
			continue
		}
		if inTarget {
			// Stop at the next target header (but not the target's own body,
			// which is tab-indented or a conditional directive).
			if targetHeader.MatchString(ln) {
				break
			}
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}

// TestReleaseGatesPreserved asserts the historical release-gate records are
// still present. r06.7 acceptance explicitly requires "No regression to
// existing release gates (be-*-gate.md)".
func TestReleaseGatesPreserved(t *testing.T) {
	dir := filepath.Join(sourceRepoRoot(t), "release-gates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read release-gates/: %v", err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "be-") && strings.HasSuffix(e.Name(), "-gate.md") {
			count++
		}
	}
	// There are 8 be-*-gate.md records at the time of r06.7; guard against
	// accidental deletion (allow additions).
	if count < 8 {
		t.Errorf("release-gates/ has only %d be-*-gate.md files; expected >= 8 (gate records must be preserved)", count)
	}
}

// TestVersionVarsExistForLdflags asserts the four ldflag target symbols exist in
// version.go. If any is renamed, the -X stamps in goreleaser / the macOS job /
// release-build become silent no-ops and the release binary loses provenance.
func TestVersionVarsExistForLdflags(t *testing.T) {
	src := readRepoFile(t, "cmd/bd/version.go")
	for _, v := range []string{"Version", "Build", "Commit", "Branch"} {
		re := regexp.MustCompile(`(?m)^\s*` + v + `\s*=`)
		if !re.MatchString(src) {
			t.Errorf("cmd/bd/version.go no longer declares %s (ldflags -X main.%s would be a no-op)", v, v)
		}
	}
}
