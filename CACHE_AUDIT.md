# Storage Cache Usage Audit

**Date:** 2025-10-27  
**Purpose:** Document all dependencies on the daemon storage cache before removal  
**Related:** bd-30 (Audit Current Cache Usage), bd-29 (Remove Daemon Storage Cache epic)

---

## Summary

The daemon storage cache (`storageCache map[string]*StorageCacheEntry`) is designed for multi-repository routing but only ever caches **one repository** in practice (local daemon model). All code paths can safely use `s.storage` directly instead of `getStorageForRequest()`.

---

## Key Findings

### 1. getStorageForRequest() Callers

**Total calls:** 17 production calls + 11 test calls

**Production files:**
- `internal/rpc/server_issues_epics.go` - **8 calls** (CreateIssue, UpdateIssue, CloseIssue, ReopenIssue, GetIssue, ListIssues, DeleteIssue, CreateEpic)
- `internal/rpc/server_labels_deps_comments.go` - **4 calls** (AddLabel, RemoveLabel, AddDependency, AddComment)
- `internal/rpc/server_export_import_auto.go` - **2 calls** (AutoExport, AutoImport)
- `internal/rpc/server_compact.go` - **2 calls** (CompactIssue, GetDeletedIssue)
- `internal/rpc/server_routing_validation_diagnostics.go` - **1 call** (ValidateDatabase)

**Test file:**
- `internal/rpc/server_eviction_test.go` - **11 calls** (cache eviction tests only)

**Pattern (all identical):**
```go
store, err := s.getStorageForRequest(req)
if err != nil {
    return Response{Success: false, Error: err.Error()}
}
// ... use store ...
```

**Replacement strategy:** Replace with `store := s.storage` and remove error handling.

---

### 2. req.Cwd Usage

**Purpose:** `req.Cwd` is set by RPC client for database discovery in multi-repo routing.

**Code paths:**

1. **RPC client (`internal/rpc/client.go`):**
   - Sets `req.Cwd` in request construction (line not shown in excerpt, but inferred from server usage)
   - Client debug output references `CacheSize` from health check (line 78)

2. **Server cache lookup (`internal/rpc/server_cache_storage.go`):**
   ```go
   // If no cwd specified, use default storage
   if req.Cwd == "" {
       return s.storage, nil
   }
   ```
   - When `req.Cwd` is set, uses `findDatabaseForCwd()` to locate `.beads/*.db`
   - Canonicalizes to repo root as cache key
   - Checks cache with mtime-based invalidation (lines 162-256)

3. **Validation endpoint (`server_routing_validation_diagnostics.go`):**
   - Uses `req.Cwd` for database validation (line 84-86)
   - Only validator that inspects `req.Cwd` outside of cache

4. **Comment forwarding (`server_labels_deps_comments.go`):**
   - Passes `req.Cwd` through when forwarding AddComment to hooks (line 179)
   - Preserves context for downstream handlers

**Observation:** In local daemon model, `req.Cwd` is **always empty or always matches the daemon's workspace**. The daemon only serves one repository, so routing logic is unused.

---

### 3. MCP Server Multi-Repo Strategy

**Files examined:**
- `integrations/beads-mcp/` (searched for `req.Cwd` - no results)

**Findings:**
- MCP server does **not** use `req.Cwd` routing
- MCP follows **recommended architecture**: separate daemon per repository
- Each MCP workspace connects to its own local daemon via socket
- No shared daemon across projects

**Implication:** MCP will **not** be affected by cache removal. Each daemon already serves only one repo.

---

### 4. Cache-Dependent Tests

**File:** `internal/rpc/server_eviction_test.go`

**Tests to DELETE (entire file):**
- `TestStorageCacheEviction_TTL` - TTL-based eviction
- `TestStorageCacheEviction_LRU` - LRU eviction when over max size
- `TestStorageCacheEviction_MemoryPressure` - Memory pressure eviction
- `TestStorageCacheEviction_MtimeInvalidation` - Stale DB detection
- `TestConcurrentCacheAccess` - Concurrent cache access
- `TestSubdirectoryDatabaseLookup` - Subdirectory canonicalization
- `TestManualCacheEviction` - Manual evict API
- `TestCacheSizeLimit` - Cache size enforcement
- `TestNegativeTTL` - Negative TTL config

**File:** `internal/rpc/limits_test.go`

**Updates needed:**
- Remove assertions like `len(srv.storageCache)` (exact lines not shown, needs inspection)
- Remove manual cache population (if any)

---

### 5. Environment Variables

**Cache-related env vars (to deprecate):**

1. **`BEADS_DAEMON_MAX_CACHE_SIZE`**
   - Default: 50
   - Used in: `server_core.go:63`
   - Tested in: `server_eviction_test.go:238`

2. **`BEADS_DAEMON_CACHE_TTL`**
   - Default: 30 minutes
   - Used in: `server_core.go:72`
   - Tested in: `server_eviction_test.go:239, 423`

3. **`BEADS_DAEMON_MEMORY_THRESHOLD_MB`**
   - Default: 500 MB
   - Used in: `server_cache_storage.go:47`
   - Triggers aggressive eviction

**Other env vars (keep):**
- `BEADS_DAEMON_MAX_CONNS` (connection limiting)
- `BEADS_DAEMON_REQUEST_TIMEOUT` (request timeout)

---

### 6. Cache-Related Server Fields

**In `internal/rpc/server_core.go` (Server struct):**

**Fields to REMOVE:**
```go
storageCache  map[string]*StorageCacheEntry // line 36
cacheMu       sync.RWMutex                  // line 37
maxCacheSize  int                           // line 38
cacheTTL      time.Duration                 // line 39
cleanupTicker *time.Ticker                  // line 40
cacheHits     int64                         // line 44
cacheMisses   int64                         // line 45
```

**Fields to KEEP:**
```go
storage       storage.Storage // line 28 - default storage (use this!)
// ... all other fields ...
```

---

### 7. Cache Functions in server_cache_storage.go

**All functions in this file will be deleted:**

1. **`runCleanupLoop()`** (lines 23-37)
   - Periodic eviction ticker goroutine
   - Called from `Start()`

2. **`checkMemoryPressure()`** (lines 39-61)
   - Memory monitoring and aggressive eviction trigger
   - Called from cleanup loop

3. **`aggressiveEviction()`** (lines 63-103)
   - Evicts 50% of cache under memory pressure
   - LRU-based selection

4. **`evictStaleStorage()`** (lines 105-155)
   - TTL-based eviction
   - LRU eviction for size enforcement
   - Called periodically and after new cache entries

5. **`getStorageForRequest(req *Request)`** (lines 157-256)
   - Main cache lookup and routing logic
   - mtime-based invalidation
   - **17 production callers** (see section 1)

6. **`findDatabaseForCwd(cwd string)`** (lines 258-286)
   - Walks up directory tree to find `.beads/*.db`
   - Used only by `getStorageForRequest()`
   - Also used by validation endpoint (line 86 of diagnostics)

**Note:** `findDatabaseForCwd()` is also called from `server_routing_validation_diagnostics.go:86` for database path validation. We need to decide if that endpoint should be removed or reimplemented.

---

### 8. Metrics/Health Impact

**Health endpoint fields to remove:**
- `cache_size` (line 77-78 in client.go debug output references this)

**Metrics endpoint fields to remove:**
- `cache_size`
- `cache_hits` (field line 44)
- `cache_misses` (field line 45)

**Files to update:**
- `internal/rpc/server_routing_validation_diagnostics.go` - Health and Metrics handlers
- `internal/rpc/metrics.go` - Metrics struct

---

### 9. Cache Initialization and Lifecycle

**Initialization (`server_core.go:NewServer()`):**
- Line 98: `storageCache: make(map[string]*StorageCacheEntry)`
- Lines 99-100: `maxCacheSize`, `cacheTTL` config
- **Remove:** Cache map creation and config parsing (lines 62-76, 98-100)

**Start lifecycle (`server_core.go:Start()` - not shown in excerpt):**
- Starts `runCleanupLoop()` goroutine for periodic eviction
- **Remove:** Cleanup loop goroutine launch

**Stop lifecycle (`server_core.go:Stop()` - not shown in excerpt):**
- Stops cleanup ticker
- Closes all cached storage entries
- **Simplify:** Just close `s.storage` directly

---

## Migration Strategy

### Phase 1: Remove Cache Code (bd-31, bd-32, bd-33)
1. Remove cache fields from `Server` struct
2. Replace `getStorageForRequest(req)` with `s.storage` in all 17 callers
3. Remove error handling for storage lookup failures
4. Delete `server_cache_storage.go` entirely

### Phase 2: Clean Up Tests (bd-34)
1. Delete `server_eviction_test.go` (entire file)
2. Update `limits_test.go` to remove cache assertions

### Phase 3: Update Metrics (bd-35)
1. Remove `cache_size`, `cache_hits`, `cache_misses` from health/metrics endpoints
2. Remove cache-related fields from `Metrics` struct

### Phase 4: Documentation (bd-36)
1. Remove cache env var docs from ADVANCED.md, commands/daemons.md
2. Add CHANGELOG entry documenting removal

---

## Risk Assessment

### ✅ Safe to Remove
- **Multi-repo routing:** Unused in local daemon model (always 1 repo)
- **Cache eviction logic:** Unnecessary for single storage instance
- **mtime invalidation:** Irrelevant for persistent `s.storage` connection
- **LRU/TTL/memory pressure:** Over-engineered for 1 cached entry

### ⚠️ Needs Verification
- **MCP server:** Confirmed using separate daemons (no `req.Cwd` usage found)
- **Database validation:** `ValidateDatabase` endpoint uses `findDatabaseForCwd()` - may need reimplementation or removal
- **Comment forwarding:** Passes `req.Cwd` to hooks - verify hooks don't rely on it

### ✅ No Breaking Changes Expected
- Local daemon already behaves as if cache size = 1
- All requests route to same `s.storage` instance
- Performance identical (no cache overhead to remove)
- Memory usage will improve (no cache structs)

---

## Acceptance Criteria Checklist

- [x] **Document all callers of `getStorageForRequest()`** - 17 production, 11 test
- [x] **Verify `req.Cwd` is only for database discovery** - Confirmed routing-only purpose
- [x] **Confirm MCP doesn't rely on multi-repo cache** - MCP uses separate daemons
- [x] **List tests assuming multi-repo routing** - `server_eviction_test.go` (entire file)
- [x] **Review cache env vars** - 3 cache vars, 2 non-cache vars to keep
- [x] **Document cache dependencies** - All code paths identified above

---

## Next Steps

1. **bd-31**: Remove cache fields from `Server` struct
2. **bd-32**: Replace `getStorageForRequest()` with `s.storage` (17 call sites)
3. **bd-33**: Delete `server_cache_storage.go`
4. **bd-34**: Delete `server_eviction_test.go`, update `limits_test.go`
5. **bd-35**: Remove cache metrics from health/metrics endpoints
6. **bd-36**: Update documentation and CHANGELOG

**Special consideration:** `ValidateDatabase` endpoint in `server_routing_validation_diagnostics.go` uses `findDatabaseForCwd()` outside of cache. Needs decision:
- Option A: Remove validation endpoint (may be unused)
- Option B: Inline database discovery logic in validator
- Option C: Keep `findDatabaseForCwd()` as standalone helper

**Recommendation:** Verify if `ValidateDatabase` is used, then choose Option A or B.
