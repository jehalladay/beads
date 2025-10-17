# BD Daemon Architecture for Concurrent Access

## Problem Statement

Multiple AI agents running concurrently (via beads-mcp) cause:
- **SQLite write corruption**: Counter stuck, UNIQUE constraint failures
- **Git index.lock contention**: All agents auto-export → all try to commit simultaneously
- **Data loss risk**: Concurrent SQLite writers without coordination
- **Poor performance**: Redundant exports, 4x git operations for same changes

## Current Architecture (Broken)

```
Agent 1 → beads-mcp 1 → bd CLI → SQLite DB (direct write)
Agent 2 → beads-mcp 2 → bd CLI → SQLite DB (direct write)  ← RACE CONDITIONS
Agent 3 → beads-mcp 3 → bd CLI → SQLite DB (direct write)
Agent 4 → beads-mcp 4 → bd CLI → SQLite DB (direct write)
                                     ↓
                                 4x concurrent git export/commit
```

## Proposed Architecture (Daemon-Mediated)

```
Agent 1 → beads-mcp 1 → bd client ──┐
Agent 2 → beads-mcp 2 → bd client ──┼──> bd daemon → SQLite DB
Agent 3 → beads-mcp 3 → bd client ──┤   (single writer)     ↓
Agent 4 → beads-mcp 4 → bd client ──┘                   git export
                                                        (batched,
                                                         serialized)
```

### Key Changes

1. **bd daemon becomes mandatory** for multi-agent scenarios
2. **All bd commands become RPC clients** when daemon is running
3. **Daemon owns SQLite** - single writer, no races
4. **Daemon batches git operations** - one export cycle per interval
5. **Unix socket IPC** - simple, fast, local-only

## Implementation Plan

### Phase 1: RPC Infrastructure

**New files:**
- `internal/rpc/protocol.go` - Request/response types
- `internal/rpc/server.go` - Unix socket server in daemon
- `internal/rpc/client.go` - Client detection & dispatch

**Operations to support:**
```go
type Request struct {
    Operation string          // "create", "update", "list", "close", etc.
    Args      json.RawMessage // Operation-specific args
}

type Response struct {
    Success bool
    Data    json.RawMessage // Operation result
    Error   string
}
```

**Socket location:** `~/.beads/bd.sock` or `.beads/bd.sock` (per-repo)

### Phase 2: Client Auto-Detection

**bd command behavior:**
1. Check if daemon socket exists & responsive
2. If yes: Send RPC request, print response
3. If no: Run command directly (backward compatible)

**Example:**
```go
func main() {
    if client := rpc.TryConnect(); client != nil {
        // Daemon is running - use RPC
        resp := client.Execute(cmd, args)
        fmt.Println(resp)
        return
    }

    // No daemon - run directly (current behavior)
    executeLocally(cmd, args)
}
```

### Phase 3: Daemon SQLite Ownership

**Daemon startup:**
1. Open SQLite connection (exclusive)
2. Start RPC server on Unix socket
3. Start git sync loop (existing functionality)
4. Process RPC requests serially

**Git operations:**
- Batch exports every 5 seconds (not per-operation)
- Single commit with all changes
- Prevent concurrent git operations entirely

### Phase 4: Atomic Operations

**ID generation:**
```go
// In daemon process only
func (d *Daemon) generateID(prefix string) (string, error) {
    d.mu.Lock()
    defer d.mu.Unlock()

    // No races - daemon is single writer
    return d.storage.NextID(prefix)
}
```

**Transaction support:**
```go
// RPC can request multi-operation transactions
type BatchRequest struct {
    Operations []Request
    Atomic     bool // All-or-nothing
}
```

## Migration Strategy

### Stage 1: Opt-In (v0.10.0)
- Daemon RPC code implemented
- bd commands detect daemon, fall back to direct
- Users can `bd daemon start` for multi-agent scenarios
- **No breaking changes** - direct mode still works

### Stage 2: Recommended (v0.11.0)
- Document multi-agent workflow requires daemon
- MCP server README says "start daemon for concurrent agents"
- Detection warning: "Multiple bd processes detected, consider using daemon"

### Stage 3: Required for Multi-Agent (v1.0.0)
- bd detects concurrent access patterns
- Refuses to run without daemon if lock contention detected
- Error: "Concurrent access detected. Start daemon: `bd daemon start`"

## Benefits

✅ **No SQLite corruption** - single writer
✅ **No git lock contention** - batched, serialized operations
✅ **Atomic ID generation** - no counter corruption
✅ **Better performance** - fewer redundant exports
✅ **Backward compatible** - graceful fallback to direct mode
✅ **Simple protocol** - Unix sockets, JSON payloads

## Trade-offs

⚠️ **Daemon must be running** for multi-agent workflows
⚠️ **One more process** to manage (`bd daemon start/stop`)
⚠️ **Complexity** - RPC layer adds code & maintenance
⚠️ **Single point of failure** - if daemon crashes, all agents blocked

## Open Questions

1. **Per-repo or global daemon?**
   - Per-repo: `.beads/bd.sock` (supports multiple repos)
   - Global: `~/.beads/bd.sock` (simpler, but only one repo at a time)
   - **Recommendation:** Per-repo, use `--db` path to determine socket location

2. **Daemon crash recovery?**
   - Client auto-starts daemon if socket missing?
   - Or require manual `bd daemon start`?
   - **Recommendation:** Auto-start with exponential backoff

3. **Concurrent read optimization?**
   - Reads could bypass daemon (SQLite supports concurrent readers)
   - But complex: need to detect read-only vs read-write commands
   - **Recommendation:** Start simple, all ops through daemon

4. **Transaction API for clients?**
   - MCP tools often do multi-step operations
   - Would benefit from `BEGIN/COMMIT` style transactions
   - **Recommendation:** Phase 4 feature, not MVP

## Success Metrics

- ✅ 4 concurrent agents can run without errors
- ✅ No UNIQUE constraint failures on ID generation
- ✅ No git index.lock errors
- ✅ SQLite counter stays in sync with actual issues
- ✅ Graceful fallback when daemon not running

## Related Issues

- bd-668: Git index.lock contention (root cause)
- bd-670: ID generation retry on UNIQUE constraint
- bd-654: Concurrent tmp file collisions (already fixed)
- bd-477: Phase 1 daemon command (git sync only - now expanded)
- bd-279: Tests for concurrent scenarios
- bd-271: Epic for multi-device support

## Next Steps

1. **Ultrathink**: Validate this design with user
2. **File epic**: Create bd-??? for daemon RPC architecture
3. **Break down work**: Phase 1 subtasks (protocol, server, client)
4. **Start implementation**: Begin with protocol.go

---

## Phase 4: Atomic Operations and Stress Testing (COMPLETED - bd-114)

**Status:** ✅ Complete

**Implementation:**
- Batch/transaction API for multi-step operations
- Request timeout and cancellation support
- Connection management optimization
- Comprehensive stress tests (4-10 concurrent agents)
- Performance benchmarks vs direct mode

**Results:**
- Daemon mode is **2x faster** than direct mode
- Zero ID collisions in 1000+ concurrent creates
- All acceptance criteria validated
- Full test coverage with stress tests

**Documentation:** See [DAEMON_STRESS_TEST.md](DAEMON_STRESS_TEST.md) for details.

**Files Added:**
- `internal/rpc/stress_test.go` - Stress tests with 4-10 agents
- `internal/rpc/bench_test.go` - Performance benchmarks
- `DAEMON_STRESS_TEST.md` - Full documentation

**Files Modified:**
- `internal/rpc/protocol.go` - Added OpBatch and batch types
- `internal/rpc/server.go` - Implemented batch handler
- `internal/rpc/client.go` - Added timeout support and Batch method
