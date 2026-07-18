#!/usr/bin/env bash
# bg-gate.sh — a detached, pollable runner for the slow beads merge gate
# (beads-l3po).
#
# WHY THIS EXISTS
#   The beads refinery is a Claude instance. It runs the canonical merge gate
#   (scripts/ci/forge-gate.sh: CGO build + tag-assert + pure-Go compile +
#   cross-compile, and — via `make test` in the refinery's gate wrapper — the
#   full cmd/bd suite against embedded Dolt). Under shared-/fsx contention that
#   gate legitimately takes ~10min. Watched INLINE, the streamed build log fills
#   the refinery instance's context window: a fresh instance climbs to the
#   watchdog restart band mid-gate → ctx-exhausts before it can push → the MR
#   re-loops. Combined with orphan-on-restart (beads-7fid) that produced an
#   observed ~90min full-queue block. The ROOT is that a ~10min gate does not
#   fit one context window when watched inline, not a wedge.
#
#   bg-gate.sh removes the gate log from the invoker's context: it LAUNCHES the
#   gate detached (setsid + nohup, output redirected to a run-dir log file) and
#   lets the invoker POLL a tiny status file. The invoker (refinery) does:
#
#       rundir=$(scripts/ci/bg-gate.sh start)      # returns immediately, 1 line
#       # ... do other bounded work / yield the turn ...
#       scripts/ci/bg-gate.sh status "$rundir"     # prints one word: running|passed|failed
#       # only on failure, and only bounded:
#       scripts/ci/bg-gate.sh log "$rundir" --tail 50
#
#   So no matter how verbose the underlying gate is, the invoker accumulates a
#   handful of lines (a run-dir path + one status word + a bounded failure
#   tail), and its context stays flat across the whole ~10min wait.
#
#   This is the beads-repo PRIMITIVE. The refinery/formula ADOPTION (call start,
#   poll status between turns instead of streaming the gate) is the gt-side
#   one-liner that consumes it; this script is what makes that adoption possible
#   without gate drift (the detached command defaults to the SAME
#   scripts/ci/forge-gate.sh, so CI == forge == refinery-bg by construction).
#
# SUBCOMMANDS
#   start [gate-args...]     Launch the gate detached. Prints the run dir (the
#                            handle for status/log/wait) and returns immediately.
#                            Any extra args are forwarded to the gate command.
#   status <rundir>          Print exactly one word: running | passed | failed.
#   wait   <rundir> [--timeout N]
#                            Block until terminal (default 900s), print the final
#                            status word, and exit 0 iff passed (non-zero on fail
#                            or timeout). Convenience for a synchronous caller.
#   log    <rundir> [--tail N]
#                            Print a bounded tail (default 50 lines) of the gate
#                            log. Use only on failure — keeps diagnosis bounded.
#
# ENV
#   BEADS_BG_GATE_DIR   Base dir for run dirs (default /tmp/beads-bg-gate).
#   BEADS_BG_GATE_CMD   The gate command to run detached (default
#                       scripts/ci/forge-gate.sh, resolved from repo root). Split
#                       on spaces; overridable for tests (stub gate).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

BASE_DIR="${BEADS_BG_GATE_DIR:-/tmp/beads-bg-gate}"
DEFAULT_GATE="$REPO_ROOT/scripts/ci/forge-gate.sh"
GATE_CMD="${BEADS_BG_GATE_CMD:-$DEFAULT_GATE}"

usage() {
    echo "usage: bg-gate.sh {start [gate-args...] | status <rundir> | wait <rundir> [--timeout N] | log <rundir> [--tail N]}" >&2
    exit 2
}

# read_status <rundir> -> echoes running|passed|failed.
# Terminal state is recorded in <rundir>/exit (the gate's exit code) once the
# runner finishes; absence of that file means still running.
read_status() {
    local rundir="$1"
    if [ ! -d "$rundir" ]; then
        echo "bg-gate: no such run dir: $rundir" >&2
        exit 2
    fi
    if [ -f "$rundir/exit" ]; then
        local code
        code="$(cat "$rundir/exit" 2>/dev/null || echo 1)"
        if [ "$code" = "0" ]; then
            echo "passed"
        else
            echo "failed"
        fi
    else
        echo "running"
    fi
}

cmd_start() {
    mkdir -p "$BASE_DIR"
    local rundir
    rundir="$(mktemp -d "$BASE_DIR/run.XXXXXX")"
    # Record the command for debugging; never printed to the invoker.
    printf '%s ' "$GATE_CMD" "$@" > "$rundir/cmd"

    # Launch detached. A supervisor subshell runs the gate, tees output to the
    # log, and records the exit code ATOMICALLY (write to .tmp then mv) so a
    # concurrent status poll never sees a half-written code. setsid detaches it
    # from our process group; nohup + redirected fds mean nothing streams back
    # to the invoker's terminal/context. We disown so `start` returns at once.
    #
    # shellcheck disable=SC2086  # GATE_CMD intentionally word-splits (cmd + args)
    setsid bash -c '
        rundir="$1"; shift
        { "$@"; } > "$rundir/log" 2>&1
        code=$?
        echo "$code" > "$rundir/exit.tmp"
        mv -f "$rundir/exit.tmp" "$rundir/exit"
    ' _ "$rundir" $GATE_CMD "$@" >/dev/null 2>&1 &
    disown 2>/dev/null || true

    # The ONLY thing the invoker sees: the run dir handle.
    echo "$rundir"
}

cmd_status() {
    [ $# -ge 1 ] || usage
    read_status "$1"
}

cmd_wait() {
    [ $# -ge 1 ] || usage
    local rundir="$1"; shift
    local timeout=900
    while [ $# -gt 0 ]; do
        case "$1" in
            --timeout) timeout="$2"; shift 2 ;;
            *) echo "bg-gate wait: unknown arg: $1" >&2; exit 2 ;;
        esac
    done
    local waited=0
    while [ "$waited" -lt "$timeout" ]; do
        local s
        s="$(read_status "$rundir")"
        if [ "$s" != "running" ]; then
            echo "$s"
            [ "$s" = "passed" ] && exit 0 || exit 1
        fi
        sleep 1
        waited=$((waited + 1))
    done
    echo "timeout" >&2
    exit 1
}

cmd_log() {
    [ $# -ge 1 ] || usage
    local rundir="$1"; shift
    local tail_n=50
    while [ $# -gt 0 ]; do
        case "$1" in
            --tail) tail_n="$2"; shift 2 ;;
            *) echo "bg-gate log: unknown arg: $1" >&2; exit 2 ;;
        esac
    done
    if [ ! -f "$rundir/log" ]; then
        echo "bg-gate: no log for run dir: $rundir" >&2
        exit 2
    fi
    tail -n "$tail_n" "$rundir/log"
}

[ $# -ge 1 ] || usage
sub="$1"; shift
case "$sub" in
    start)  cmd_start "$@" ;;
    status) cmd_status "$@" ;;
    wait)   cmd_wait "$@" ;;
    log)    cmd_log "$@" ;;
    *)      usage ;;
esac
