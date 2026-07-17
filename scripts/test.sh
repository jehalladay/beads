#!/usr/bin/env bash
# Test runner that automatically skips known broken tests

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SKIP_FILE="$REPO_ROOT/.test-skip"

# Canonical build flags (GOFLAGS=-tags=gms_pure_go, CGO_ENABLED=1).
# Opt-in ICU-path coverage remains available via scripts/test-icu-path.sh.
# shellcheck source=../.buildflags
source "$REPO_ROOT/.buildflags"
# shellcheck source=ci/lib/test-env.sh
source "$REPO_ROOT/scripts/ci/lib/test-env.sh"

beads_test_env_enter

# Build skip pattern from .test-skip file
build_skip_pattern() {
    if [[ ! -f "$SKIP_FILE" ]]; then
        echo ""
        return
    fi

    # Read non-comment, non-empty lines and join with |
    local pattern=$(grep -v '^#' "$SKIP_FILE" | grep -v '^[[:space:]]*$' | paste -sd '|' -)
    echo "$pattern"
}

# Default values
TIMEOUT="${TEST_TIMEOUT:-3m}"
# Track whether the timeout was set explicitly (env or -timeout flag). The race
# tier bumps an UNSET default (see below) but must never override an explicit
# caller choice. (beads-367)
TIMEOUT_EXPLICIT=""
[[ -n "${TEST_TIMEOUT:-}" ]] && TIMEOUT_EXPLICIT="1"
# Default timeout for the race tier when the caller did not set one. Race
# instrumentation slows tests ~7-10x, so the 3m default produces FALSE "panic:
# test timed out" failures on subprocess/Dolt-backed tests. CI's full race run
# uses 30m (.github/workflows/nightly.yml); match that. (beads-367)
RACE_TIMEOUT="${TEST_RACE_TIMEOUT:-30m}"
GO_TEST_PKG_PARALLEL="${GO_TEST_PKG_PARALLEL:-4}"
GO_TEST_PARALLEL="${GO_TEST_PARALLEL:-4}"
SKIP_PATTERN=$(build_skip_pattern)
VERBOSE="${TEST_VERBOSE:-}"
RUN_PATTERN="${TEST_RUN:-}"
COVERAGE="${TEST_COVER:-}"
COVERPROFILE="${TEST_COVERPROFILE:-/tmp/beads.coverage.out}"
COVERPKG="${TEST_COVERPKG:-}"
# Race detector tier (beads-r06.8). Enable via `--race`/`-race` or TEST_RACE=1
# (make test-race). The race detector needs CGO, which .buildflags defaults on.
RACE="${TEST_RACE:-}"

# Shared-node CPU-contention mitigation (beads-cn5). On shared cluster nodes,
# many crew each run `go test` concurrently; Go defaults GOMAXPROCS to all cores
# and applies no nice level, so N crew × all-cores oversubscribes the node
# (measured load ~4x nproc) and every crew's TDD loop stalls. We degrade
# gracefully by (a) running under `nice` so concurrent loops share CPU fairly,
# and (b) capping this invocation's GOMAXPROCS to ~half the cores so N builds
# don't each grab every core. Both are overridable; set to empty to disable.
#   BEADS_TEST_NICE      nice increment (default 10; "" = no nice)
#   BEADS_TEST_GOMAXPROCS GOMAXPROCS for this run (default: max(2, nproc/2); "" = don't set)
NICE_LEVEL="${BEADS_TEST_NICE-10}"
if [[ -n "${BEADS_TEST_GOMAXPROCS+x}" ]]; then
    # Caller set it explicitly (possibly to empty, meaning "don't cap").
    GOMAXPROCS_CAP="${BEADS_TEST_GOMAXPROCS}"
else
    _ncpu="$(nproc 2>/dev/null || echo 4)"
    GOMAXPROCS_CAP="$(( _ncpu / 2 ))"
    (( GOMAXPROCS_CAP < 2 )) && GOMAXPROCS_CAP=2
fi

# Parse arguments
PACKAGES=()
while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--verbose)
            VERBOSE="-v"
            shift
            ;;
        -timeout)
            TIMEOUT="$2"
            TIMEOUT_EXPLICIT="1"
            shift 2
            ;;
        -race|--race)
            RACE="1"
            shift
            ;;
        -run)
            RUN_PATTERN="$2"
            shift 2
            ;;
        -skip)
            # Allow additional skip patterns
            if [[ -n "$SKIP_PATTERN" ]]; then
                SKIP_PATTERN="$SKIP_PATTERN|$2"
            else
                SKIP_PATTERN="$2"
            fi
            shift 2
            ;;
        *)
            PACKAGES+=("$1")
            shift
            ;;
    esac
done

# Default to all packages if none specified
if [[ ${#PACKAGES[@]} -eq 0 ]]; then
    PACKAGES=("./...")
fi

# Optional: start a single shared Dolt test server for all packages.
# When BEADS_TEST_SHARED_SERVER=1, we start one dolt sql-server and export
# BEADS_DOLT_PORT so every test package reuses it instead of spawning its own.
# This reduces 8-16+ concurrent dolt processes down to 1.
if [[ "${BEADS_TEST_SHARED_SERVER:-}" == "1" && -z "${BEADS_DOLT_PORT:-}" ]]; then
    if command -v dolt &>/dev/null; then
        SHARED_DOLT_DIR=$(mktemp -d /tmp/beads-shared-test-dolt-XXXXXX)
        DOLT_ROOT_PATH="$SHARED_DOLT_DIR"
        export DOLT_ROOT_PATH

        dolt config --global --add user.name "beads-test" 2>/dev/null
        dolt config --global --add user.email "test@beads.local" 2>/dev/null

        SHARED_DB_DIR="$SHARED_DOLT_DIR/data"
        mkdir -p "$SHARED_DB_DIR"
        (cd "$SHARED_DB_DIR" && dolt init) >/dev/null 2>&1

        # Find a free port
        SHARED_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')

        dolt sql-server -H 127.0.0.1 -P "$SHARED_PORT" --no-auto-commit \
            --data-dir "$SHARED_DB_DIR" &>/dev/null &
        SHARED_DOLT_PID=$!

        # Wait for server to accept connections (up to 30s)
        for i in $(seq 1 60); do
            if nc -z 127.0.0.1 "$SHARED_PORT" 2>/dev/null; then
                break
            fi
            sleep 0.5
        done

        if nc -z 127.0.0.1 "$SHARED_PORT" 2>/dev/null; then
            export BEADS_DOLT_PORT="$SHARED_PORT"
            export BEADS_TEST_MODE=1
            echo "Shared test Dolt server started on port $SHARED_PORT (PID $SHARED_DOLT_PID)" >&2
            cleanup_shared_server() {
                kill "$SHARED_DOLT_PID" 2>/dev/null || true
                wait "$SHARED_DOLT_PID" 2>/dev/null || true
                rm -rf "$SHARED_DOLT_DIR"
            }
            trap 'cleanup_shared_server; beads_test_env_cleanup' EXIT
        else
            echo "WARN: shared Dolt server failed to start, falling back to per-package servers" >&2
            kill "$SHARED_DOLT_PID" 2>/dev/null || true
            rm -rf "$SHARED_DOLT_DIR"
        fi
    fi
fi

# Race tier bumps an unset timeout so instrumented subprocess/Dolt-backed tests
# don't hit a FALSE "panic: test timed out" (race is ~7-10x slower). An explicit
# -timeout / TEST_TIMEOUT always wins. (beads-367)
if [[ -n "$RACE" && -z "$TIMEOUT_EXPLICIT" ]]; then
    TIMEOUT="$RACE_TIMEOUT"
fi

# Contention wrapper (beads-cn5): `nice` + a GOMAXPROCS cap so concurrent crew
# runs on a shared node degrade gracefully instead of thrashing. Kept SEPARATE
# from the go-test command so the "Running:" diagnostic below still leads with
# `go test ...` (other tooling/tests parse that line for the go-test flags).
WRAP=()
if [[ -n "$NICE_LEVEL" ]] && command -v nice >/dev/null 2>&1; then
    WRAP+=(nice -n "$NICE_LEVEL")
fi
if [[ -n "$GOMAXPROCS_CAP" ]]; then
    WRAP+=(env "GOMAXPROCS=$GOMAXPROCS_CAP")
fi

# Build go test command
CMD=(go test -p "$GO_TEST_PKG_PARALLEL" -parallel "$GO_TEST_PARALLEL" -timeout "$TIMEOUT")

if [[ -n "$RACE" ]]; then
    # The Go race detector is a cgo instrumentation pass; it cannot run under
    # CGO_ENABLED=0. .buildflags defaults CGO on, but a caller may have forced
    # it off — fail loudly rather than silently skip race coverage.
    if [[ "${CGO_ENABLED:-1}" == "0" ]]; then
        echo "ERROR: --race requires CGO_ENABLED=1 (race detector is cgo-based); refusing to run a no-op race tier" >&2
        exit 2
    fi
    CMD+=(-race)
fi

if [[ -n "$VERBOSE" ]]; then
    CMD+=(-v)
fi

if [[ -n "$SKIP_PATTERN" ]]; then
    CMD+=(-skip "$SKIP_PATTERN")
fi

if [[ -n "$RUN_PATTERN" ]]; then
    CMD+=(-run "$RUN_PATTERN")
fi

if [[ -n "$COVERAGE" ]]; then
    CMD+=(-covermode=atomic -coverprofile "$COVERPROFILE")
    if [[ -n "$COVERPKG" ]]; then
        CMD+=(-coverpkg "$COVERPKG")
    fi
fi

CMD+=("${PACKAGES[@]}")

echo "Running: ${CMD[*]}" >&2
if [[ ${#WRAP[@]} -gt 0 ]]; then
    echo "  (wrapped for shared-node contention: ${WRAP[*]})" >&2
fi
echo "Skipping: $SKIP_PATTERN" >&2
echo "" >&2

# TEST_PRINT_CMD=1 prints the fully-assembled command (wrapper + go test) and
# exits without running it — used by scripts/test_contention_test.go to assert
# command construction without a real build.
if [[ -n "${TEST_PRINT_CMD:-}" ]]; then
    echo "${WRAP[*]} ${CMD[*]}"
    exit 0
fi

"${WRAP[@]}" "${CMD[@]}"
status=$?

if [[ -n "$COVERAGE" ]]; then
    total=$(go tool cover -func="$COVERPROFILE" | awk '/^total:/ {print $NF}')
    echo "Total coverage: ${total} (profile: ${COVERPROFILE})" >&2
fi

exit $status
