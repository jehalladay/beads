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
	cmd := exec.Command("bash", args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	// With an empty bin dir on PATH (no `go`, and the /fsx fallback absent in
	// most CI), the gate must fail loudly rather than silently skip. We can't
	// guarantee the /fsx fallback is missing on the cluster build host, so this
	// test only asserts a hard failure when neither is available; skip if the
	// fallback toolchain exists.
	if _, err := os.Stat("/fsx/ubuntu/goroot-1262/bin/go"); err == nil {
		t.Skip("pinned fallback toolchain present; no-toolchain path not exercised here")
	}
	empty := t.TempDir()
	out, code := runForgeGate(t, empty, "--fast")
	if code == 0 {
		t.Fatalf("expected gate to FAIL with no toolchain, but it passed:\n%s", out)
	}
	if !strings.Contains(out, "no 'go' toolchain") {
		t.Errorf("expected no-toolchain failure message, got:\n%s", out)
	}
}
