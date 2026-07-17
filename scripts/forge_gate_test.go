package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// forge-gate.sh (beads-r06.3 / A3) is the single source of truth for the
// "forge build GREEN" gate: git pre-push, CI, and forge all shell out to it, so
// CI == forge by construction. These tests exercise the gate's decision logic
// hermetically by putting a stub `go` toolchain on PATH so no real ~2min build
// runs in the unit-test tier. The stub build writes a fake `bd` whose `version`
// output we control, letting us assert the two behaviors that matter:
//
//   1. GREEN: when the emitted binary reports the ldflags-stamped short SHA, the
//      gate passes (exit 0).
//   2. RED: when the binary reports Build="dev" (what a bare `go build` with no
//      -ldflags produces), the tag-assert FAILS the gate (non-zero) — this is
//      what prevents a bare build from masquerading as green.
//
// We run with --fast so the test covers the build + tag-assert path (the
// forge pre_build hook path) without the cross-compile stage. A separate,
// tag-gated smoke test (TestForgeGateFullBuildSmoke) optionally runs the real
// gate end-to-end when BEADS_FORGE_GATE_SMOKE=1 is set.

// writeStubGo installs a fake `go` on a fresh bin dir that, on `go build ... -o
// <out> ...`, writes an executable shell script to <out> emulating `bd
// version`. buildVersion is what the fake bd prints for `bd version`.
// It returns the bin dir to prepend to PATH.
func writeStubGo(t *testing.T, buildVersion string) string {
	t.Helper()
	bin := t.TempDir()

	// The stub parses the `-o <output>` pair out of its args (as the gate's
	// `go build ... -o "$WORK/bd" ...` invocation supplies) and writes a fake
	// bd there. For any non-build subcommand (test -c, etc.) it just succeeds,
	// honoring -o if present so compile-check outputs exist.
	stub := `#!/usr/bin/env bash
set -euo pipefail
out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then out="$a"; fi
  prev="$a"
done
if [ -n "$out" ]; then
  cat > "$out" <<EOS
#!/usr/bin/env bash
if [ "\$1" = "version" ]; then echo "bd version 1.1.0-rc.1 (` + buildVersion + `)"; exit 0; fi
exit 0
EOS
  chmod +x "$out"
fi
exit 0
`
	goPath := filepath.Join(bin, "go")
	if err := os.WriteFile(goPath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// runForgeGate execs scripts/ci/forge-gate.sh with the given args and a PATH
// that has stubBin (the fake go) prepended. Returns combined output + exit code.
func runForgeGate(t *testing.T, stubBin string, args ...string) (string, int) {
	t.Helper()
	repo := sourceRepoRoot(t)
	full := append([]string{filepath.Join(repo, "scripts", "ci", "forge-gate.sh")}, args...)
	out, code := runScriptWithPath(t, repo, stubBin, full...)
	return out, code
}

// runScriptWithPath runs `bash <args...>` in repo with binDir prepended to
// PATH, returning combined output + exit code (non-zero is not a fatal test
// error; the caller asserts on the code).
func runScriptWithPath(t *testing.T, repo, binDir string, args ...string) (string, int) {
	t.Helper()
	return runScriptWithFullPath(t, repo, binDir+string(os.PathListSeparator)+os.Getenv("PATH"), args...)
}

// runScriptWithFullPath is like runScriptWithPath but takes the child's
// complete PATH verbatim (no inherited PATH is appended). The no-toolchain test
// uses this to guarantee `go` is genuinely absent from the child environment.
func runScriptWithFullPath(t *testing.T, repo, fullPath string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+fullPath)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("failed to run %v: %v\n%s", args, err, out)
		}
	}
	return string(out), code
}

// pathWithoutGo returns the current PATH with every directory that contains a
// `go` executable removed, so a child process using it cannot resolve a Go
// toolchain. ok is false when the sanitized PATH would no longer be able to
// resolve the shell tools the gate needs (bash, git) — e.g. when `go` lives in
// a shared dir like /usr/bin alongside them — in which case the no-toolchain
// path cannot be cleanly exercised on this host and the caller should skip.
func pathWithoutGo(t *testing.T) (path string, ok bool) {
	t.Helper()
	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, "go")); err == nil && !info.IsDir() {
			continue // drop any dir that provides `go`
		}
		kept = append(kept, dir)
	}
	sanitized := strings.Join(kept, string(os.PathListSeparator))
	// The gate script itself and its helpers need bash + git; if either is no
	// longer resolvable, we cannot exercise the no-toolchain path cleanly.
	for _, tool := range []string{"bash", "git"} {
		if !toolResolvable(tool, sanitized) {
			return "", false
		}
	}
	return sanitized, true
}

// toolResolvable reports whether name resolves to an executable in one of the
// directories of the given PATH string.
func toolResolvable(name, path string) bool {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// shortHead returns the current git short SHA (matching what forge-gate.sh
// computes via `git rev-parse --short HEAD`).
func shortHead(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = sourceRepoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestForgeGatePassesWhenBinaryIsTagStamped(t *testing.T) {
	// Fake go writes a bd that reports the exact short SHA the gate computes,
	// so the tag-assert should pass.
	sha := shortHead(t)
	stub := writeStubGo(t, sha)

	out, code := runForgeGate(t, stub, "--fast")
	if code != 0 {
		t.Fatalf("expected gate to PASS with tag-stamped binary, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "ldflags stamp present") {
		t.Errorf("expected tag-assert success message, got:\n%s", out)
	}
}

func TestForgeGateFailsOnBareBuild(t *testing.T) {
	// Fake go writes a bd that reports Build="dev" — what a bare `go build`
	// (no -ldflags -X main.Build) produces. The gate must reject it.
	stub := writeStubGo(t, "dev")

	out, code := runForgeGate(t, stub, "--fast")
	if code == 0 {
		t.Fatalf("expected gate to FAIL on bare (dev) build, but it passed:\n%s", out)
	}
	if !strings.Contains(out, "does not report the ldflags-stamped build") {
		t.Errorf("expected tag-assert failure message, got:\n%s", out)
	}
}

func TestForgeGateFailsWhenNoToolchain(t *testing.T) {
	// The gate must fail loudly (not silently skip) when no `go` toolchain is
	// reachable. Simply prepending an empty dir to PATH is NOT enough: on any
	// host with `go` on the inherited system PATH, the gate would still resolve
	// go and pass (the bug beads-59g fixes). So build a child PATH with every
	// go-providing directory removed.
	//
	// The gate also resolves the pinned town toolchain /fsx/ubuntu/goroot-1262
	// by *absolute* path (independent of PATH), so where that exists we cannot
	// exercise the toolchain-absent branch and must skip.
	if _, err := os.Stat("/fsx/ubuntu/goroot-1262/bin/go"); err == nil {
		t.Skip("pinned /fsx fallback toolchain present; gate resolves it by absolute path, so the no-toolchain branch cannot be exercised here")
	}
	sanitized, ok := pathWithoutGo(t)
	if !ok {
		t.Skip("cannot exercise no-toolchain path: `go` shares a PATH dir with bash/git on this host")
	}

	repo := sourceRepoRoot(t)
	full := []string{filepath.Join(repo, "scripts", "ci", "forge-gate.sh"), "--fast"}
	out, code := runScriptWithFullPath(t, repo, sanitized, full...)
	if code == 0 {
		t.Fatalf("expected gate to FAIL with no toolchain, but it passed:\n%s", out)
	}
	if !strings.Contains(out, "no 'go' toolchain") {
		t.Errorf("expected no-toolchain failure message, got:\n%s", out)
	}
}
