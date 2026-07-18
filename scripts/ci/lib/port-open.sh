#!/usr/bin/env bash
# port-open.sh — a portable TCP-readiness probe (beads-l3po).
#
# WHY THIS EXISTS
#   The shared test Dolt server (BEADS_TEST_SHARED_SERVER=1 in scripts/test.sh)
#   is the ~33% gate speedup: one dolt sql-server for the whole suite instead of
#   8-16 per-package servers. But its readiness check used `nc -z`, and the /fsx
#   refinery/cluster nodes DO NOT ship `nc` — so on exactly those nodes the probe
#   errored "command not found", never reported ready, and test.sh SILENTLY fell
#   back to per-package servers (the slow ~10min gate). The shared-server win was
#   built but never activated where it matters.
#
#   beads_port_open probes without depending on `nc`: it prefers `nc` when
#   present, else uses the bash builtin /dev/tcp (always available in bash), else
#   python3 (already a test.sh dependency for the free-port pick). So the
#   shared-server activation no longer hinges on an optional binary.
#
# USAGE
#   source scripts/ci/lib/port-open.sh
#   if beads_port_open 127.0.0.1 3307; then echo up; fi
#
#   Returns 0 iff a TCP connection to host:port succeeds, 1 otherwise. Never
#   prints; callers decide messaging.

if [[ -n "${BEADS_CI_PORT_OPEN_SH_LOADED:-}" ]]; then
    return 0 2>/dev/null || exit 0
fi
BEADS_CI_PORT_OPEN_SH_LOADED=1

# beads_port_open <host> <port> -> 0 if the TCP port accepts a connection.
#
# BEADS_PORT_OPEN_FORCE (test seam): "nc" | "devtcp" | "python" pins the probe
# method so tests can exercise each backend deterministically regardless of
# what is installed. Unset = auto-detect (nc → /dev/tcp → python3).
beads_port_open() {
    local host="$1" port="$2"
    local method="${BEADS_PORT_OPEN_FORCE:-}"

    if [[ -z "$method" ]]; then
        if command -v nc >/dev/null 2>&1; then
            method="nc"
        else
            method="devtcp"
        fi
    fi

    case "$method" in
        nc)
            nc -z "$host" "$port" >/dev/null 2>&1
            return $?
            ;;
        devtcp)
            # bash builtin — no external dependency. Open then immediately close
            # fd 3 on the target; a refused/unreachable port fails the redirect.
            if (exec 3<>"/dev/tcp/$host/$port") 2>/dev/null; then
                exec 3>&- 3<&- 2>/dev/null || true
                return 0
            fi
            return 1
            ;;
        python)
            python3 - "$host" "$port" <<'PY' 2>/dev/null
import socket, sys
host, port = sys.argv[1], int(sys.argv[2])
s = socket.socket()
s.settimeout(1)
try:
    s.connect((host, port))
    sys.exit(0)
except OSError:
    sys.exit(1)
finally:
    s.close()
PY
            return $?
            ;;
        *)
            echo "beads_port_open: unknown BEADS_PORT_OPEN_FORCE method: $method" >&2
            return 2
            ;;
    esac
}
