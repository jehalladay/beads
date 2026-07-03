#!/usr/bin/env bash
# forge-gate.sh — the single source of truth for the beads "forge build GREEN"
# gate (beads-r06.3 / A3).
#
# WHY THIS EXISTS
#   CI and the local commit/push path must run the *same* build, or they drift
#   (the failure mode A3 closes: a hand-maintained `go build/test` list in the
#   workflows that slowly diverges from what developers run locally). This
#   script is that one entrypoint. It is invoked identically by:
#     - the git pre-push hook (.githooks/pre-push, push to main),
#     - the CI workflows (.github/workflows/*.yml), and
#     - forge (via forge.toml [build.hooks]/[build.commands]; see INTERIM note).
#   Because all three shell out to THIS file, CI == forge == local by
#   construction.
#
# WHAT "GREEN" MEANS HERE
#   The canonical beads build contract (see Makefile / .buildflags):
#     CGO_ENABLED=1, -tags gms_pure_go, -ldflags "-X main.Build=<short-sha>".
#   The gate:
#     1. builds cmd/bd with those exact flags (linux/native),
#     2. asserts the emitted binary is the *tagged* build — i.e. `bd version`
#        reports the git short SHA baked via ldflags, NOT the "dev" default.
#        This is what proves a bare `go build ./...` (no tags, no ldflags)
#        cannot masquerade as a green gate,
#     3. compiles the pure-Go (CGO_ENABLED=0, gms_pure_go) build + test binaries
#        so the ICU-free portable path stays green (mirrors the CI
#        check-cmd-bd-puregeo-tests job),
#     4. cross-compiles linux-amd64 + darwin-arm64 (acceptance: "cross-compile
#        linux-amd64 + darwin-arm64 bd produced by the gate"). darwin uses
#        CGO_ENABLED=0 as a compile-smoke; the CGO=1 signed release binaries are
#        produced natively on macOS by release.yml (beads-r06.7), not here.
#
# INTERIM vs FINAL (beads-r06.3 depends on beads-r06.2 / forge patch beads-2hk)
#   forge 0.2.0's go backend ignores [build.commands] overrides and its
#   [build.hooks] run under a hardcoded 120s subprocess timeout (build.py:137),
#   while a cold beads CGO+embedded-dolt build is ~175s. So forge cannot yet BE
#   this gate. INTERIM: forge.toml wires a fast `pre_build` sanity hook (fits
#   120s) and the *real* gate is enforced by the pre-push hook + CI, both
#   calling this script. FINAL (once beads-2hk lands + forge is pinned): map
#   [build.commands] build -> this script so `forge build` runs the identical
#   gate and bare builds are rejected. Either way the gate logic lives here, so
#   the swap is a one-line forge.toml change with no behavior drift.
#
# USAGE
#   scripts/ci/forge-gate.sh [--fast] [--no-cross]
#     --fast      build + tag-assert only (skip pure-Go compile + cross-compile).
#                 Used by the forge pre_build hook to stay under the 120s window.
#     --no-cross  skip the cross-compile stage (build + tag-assert + pure-Go).
#
# ENV
#   Resolves the Go toolchain from GOROOT/PATH; if `go` is not already on PATH,
#   falls back to the pinned town toolchain at /fsx/ubuntu/goroot-1262 when
#   present (the cluster crew build host). CI provides `go` via setup-go.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

FAST=0
CROSS=1
for arg in "$@"; do
    case "$arg" in
        --fast) FAST=1 ;;
        --no-cross) CROSS=0 ;;
        *) echo "forge-gate: unknown argument: $arg" >&2; exit 2 ;;
    esac
done

# --- toolchain resolution -------------------------------------------------
# CI (setup-go) and most dev shells already have `go` on PATH. The cluster
# crew build host does not put the working toolchain on the default PATH, so
# fall back to the pinned GOROOT when we can't otherwise find go.
if ! command -v go >/dev/null 2>&1; then
    if [ -x /fsx/ubuntu/goroot-1262/bin/go ]; then
        export GOROOT=/fsx/ubuntu/goroot-1262
        export PATH="$GOROOT/bin:$PATH"
    fi
fi
if ! command -v go >/dev/null 2>&1; then
    echo "forge-gate: FATAL: no 'go' toolchain on PATH (and no /fsx/ubuntu/goroot-1262 fallback)" >&2
    exit 1
fi

# Canonical build flags (single source: .buildflags). Sets BEADS_BUILD_TAGS +
# CGO_ENABLED. We intentionally do NOT rely on GOFLAGS smuggling the tag; every
# invocation below passes -tags explicitly so the contract is visible.
# shellcheck source=../../.buildflags
source "$REPO_ROOT/.buildflags"

GIT_BUILD="$(git rev-parse --short HEAD)"
LDFLAGS="-X main.Build=${GIT_BUILD}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

section() { printf '\n\033[1m== forge-gate: %s ==\033[0m\n' "$1"; }

# --- 1. canonical build ---------------------------------------------------
section "build cmd/bd (CGO_ENABLED=1, -tags ${BEADS_BUILD_TAGS}, ldflags Build=${GIT_BUILD})"
CGO_ENABLED=1 go build -tags "${BEADS_BUILD_TAGS}" -ldflags="${LDFLAGS}" -o "$WORK/bd" ./cmd/bd
echo "built: $WORK/bd"

# --- 2. tag-assert: prove this is the ldflags-stamped build, not a bare build
section "assert emitted binary is the tagged build (rejects bare 'go build')"
version_line="$("$WORK/bd" version 2>&1 | head -1)"
echo "  bd version -> ${version_line}"
if ! printf '%s' "$version_line" | grep -q "${GIT_BUILD}"; then
    echo "forge-gate: FATAL: binary does not report the ldflags-stamped build ${GIT_BUILD}." >&2
    echo "  A bare 'go build' (no -ldflags -X main.Build) reports Build=\"dev\" and would fail here." >&2
    echo "  Got: ${version_line}" >&2
    exit 1
fi
echo "  OK: ldflags stamp present — this is the canonical gms_pure_go build."

if [ "$FAST" -eq 1 ]; then
    section "fast mode: build + tag-assert only (forge pre_build hook path) — PASS"
    exit 0
fi

# --- 3. pure-Go (CGO_ENABLED=0) compile check -----------------------------
# Mirrors the CI check-cmd-bd-puregeo-tests job: the ICU-free portable path
# must build and its test binaries must compile.
section "pure-Go compile check (CGO_ENABLED=0, -tags ${BEADS_BUILD_TAGS})"
CGO_ENABLED=0 go build -tags "${BEADS_BUILD_TAGS}" -o "$WORK/bd-purego" ./cmd/bd
CGO_ENABLED=0 go test -tags "${BEADS_BUILD_TAGS}" -c -o "$WORK/bd-cmd-test" ./cmd/bd
CGO_ENABLED=0 go test -tags "${BEADS_BUILD_TAGS}" -c -o "$WORK/bd-tracker-test" ./internal/tracker
echo "  OK: pure-Go binary + test binaries compile."

if [ "$CROSS" -eq 0 ]; then
    section "cross-compile skipped (--no-cross) — PASS"
    exit 0
fi

# --- 4. cross-compile linux-amd64 + darwin-arm64 --------------------------
# Acceptance: both targets produced by the gate. CGO_ENABLED=0 compile-smoke
# (no C cross-toolchain needed); the signed CGO=1 release binaries are built
# natively per-OS by release.yml (beads-r06.7 owns the release path).
section "cross-compile linux/amd64 (CGO_ENABLED=0, -tags ${BEADS_BUILD_TAGS})"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags "${BEADS_BUILD_TAGS}" -ldflags="${LDFLAGS}" -o "$WORK/bd-linux-amd64" ./cmd/bd
echo "  built: $(GOOS= GOARCH= file "$WORK/bd-linux-amd64" 2>/dev/null || echo "$WORK/bd-linux-amd64")"

section "cross-compile darwin/arm64 (CGO_ENABLED=0, -tags ${BEADS_BUILD_TAGS})"
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -tags "${BEADS_BUILD_TAGS}" -ldflags="${LDFLAGS}" -o "$WORK/bd-darwin-arm64" ./cmd/bd
echo "  built: $WORK/bd-darwin-arm64"

section "ALL GREEN — build + tag-assert + pure-Go + cross-compile (linux-amd64, darwin-arm64)"
