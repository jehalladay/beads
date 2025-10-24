# Database Re-initialization Bug Investigation

**Date**: 2024-10-24  
**Severity**: P0 Critical  
**Status**: Under Investigation

## Problem Statement

When `.beads/` directory is removed and daemon auto-starts, it creates an **empty database** instead of importing from git-tracked JSONL file. This causes silent data loss.

## What Happened

1. **Initial State**: ~/src/fred/beads had polluted database with 202 issues
2. **Action Taken**: Removed `.beads/` directory to clean pollution: `rm -rf .beads/`
3. **Session Restart**: Amp session restarted, working directory: `/Users/stevey/src/fred/beads`
4. **Auto-Init Triggered**: Daemon auto-started and created fresh database
5. **Result**: Empty database (0 issues) despite `.beads/beads.jsonl` in git with 111 issues

## Root Cause Analysis

### Key Observations

1. **File Naming Confusion**
   - Git history shows rename: `issues.jsonl → beads.jsonl` (commit d1d3fcd)
   - Daemon created new `issues.jsonl` (empty)
   - Auto-import may be looking for wrong filename

2. **Auto-Import Failed**
   - `bd init` ran successfully
   - Auto-import from git did NOT trigger
   - Expected behavior: should import from `.beads/beads.jsonl` in git

3. **Daemon Startup Sequence**
   ```
   [2025-10-24 13:19:42] Daemon started
   [2025-10-24 13:19:42] Using database: /Users/stevey/src/fred/beads/.beads/bd.db
   [2025-10-24 13:19:42] Database opened
   [2025-10-24 13:19:42] Exported to JSONL (exported 0 issues to empty file)
   ```

4. **Multiple Database Problem**
   - Three separate beads databases detected:
     - `~/src/beads/.beads/bd.db` (4.2MB, 112 issues) ✅ CORRECT
     - `~/src/fred/beads/.beads/bd.db` (155KB, 0 issues) ❌ EMPTY
     - `~/src/original/beads/.beads/bd.db` ❌ UNKNOWN STATE

## Expected Behavior

When `.beads/` directory is missing but git has tracked JSONL:

1. `bd init` should detect git-tracked JSONL file
2. Auto-import should trigger immediately
3. Database should be populated from git history
4. User should see: "Imported N issues from git"

## Actual Behavior

1. `bd init` creates empty database
2. Auto-import does NOT trigger
3. Database remains empty (0 issues)
4. Silent data loss - user unaware issues are missing

## Impact

- **Silent Data Loss**: Users lose entire issue database without warning
- **Multi-Workspace Confusion**: Per-project daemons don't handle missing DB correctly
- **Git Sync Broken**: Auto-import from git not working as expected
- **User Trust**: Critical failure mode that breaks core workflow

## Recovery Steps Taken

1. Restored from git: `git restore .beads/beads.jsonl` ❌ File already in git, not in working tree
2. Extracted from git history: `git show HEAD:.beads/beads.jsonl > /tmp/backup.jsonl`
3. Manual import with collision resolution: `bd import -i /tmp/backup.jsonl --resolve-collisions`
4. Final state: 194 issues recovered (had stale backup)

## Correct Recovery (Final)

1. Removed bad database: `rm -f .beads/beads.db`
2. Git pull to get latest: `git pull origin main` (got 111 issues from ~/src/beads)
3. Re-init with correct prefix: `bd init --prefix bd`
4. Import from git-tracked JSONL: `bd import -i .beads/beads.jsonl`
5. ✅ Result: 112 issues (111 + external_ref epic from main database)

## Technical Investigation Needed

### 1. Auto-Import Logic
- Where is auto-import triggered? (`bd init` command? daemon startup?)
- What file does it look for? (`issues.jsonl` vs `beads.jsonl`)
- Why didn't it run when `.beads/` was missing?

### 2. Daemon Initialization
- Should daemon auto-import on first startup?
- Should daemon detect missing database and import from git?
- Per-project daemon handling when DB missing

### 3. File Naming
- When did `issues.jsonl → beads.jsonl` rename happen?
- Are all code paths updated to use correct filename?
- Is auto-import looking for old filename?

### 4. Git Integration
- Should `bd init` check for tracked JSONL in git?
- Should init fail if git has JSONL but DB is empty after init?
- Add warning: "JSONL found in git but not imported"?

## Proposed Fixes (Oracle-Reviewed)

### Fix A: checkGitForIssues() Filename Detection (P0, Simple, <1h)

**Current Code** (autoimport.go:70-76):
```go
relPath, err := filepath.Rel(gitRoot, filepath.Join(beadsDir, "issues.jsonl"))
```

**Fixed Code**:
```go
// Try canonical JSONL filenames in precedence order
relBeads, err := filepath.Rel(gitRoot, beadsDir)
if err != nil {
    return 0, ""
}

candidates := []string{
    filepath.Join(relBeads, "beads.jsonl"),
    filepath.Join(relBeads, "issues.jsonl"),
}

for _, relPath := range candidates {
    cmd := exec.Command("git", "show", fmt.Sprintf("HEAD:%s", relPath))
    output, err := cmd.Output()
    if err == nil && len(output) > 0 {
        lines := bytes.Count(output, []byte("\n"))
        return lines, relPath
    }
}

return 0, ""
```

**Impact**: Auto-import will now detect beads.jsonl in git

---

### Fix B: findJSONLPath() Consults Git HEAD (P0, Simple-Medium, 1-2h)

**Current Code** (main.go:898-912):
```go
func findJSONLPath() string {
    jsonlPath := beads.FindJSONLPath(dbPath)
    // Creates directory but doesn't check git
    return jsonlPath
}
```

**Fixed Code**:
```go
func findJSONLPath() string {
    // First check for existing local JSONL files
    jsonlPath := beads.FindJSONLPath(dbPath)
    
    dbDir := filepath.Dir(dbPath)
    
    // If local file exists, use it
    if _, err := os.Stat(jsonlPath); err == nil {
        return jsonlPath
    }
    
    // No local JSONL - check git HEAD for tracked filename
    if gitJSONL := checkGitForJSONLFilename(); gitJSONL != "" {
        jsonlPath = filepath.Join(dbDir, filepath.Base(gitJSONL))
    }
    
    // Ensure directory exists
    if err := os.MkdirAll(dbDir, 0755); err == nil {
        // Verify we didn't pick the wrong file
        // ...error checking...
    }
    
    return jsonlPath
}
```

**Impact**: Daemon/CLI will export to beads.jsonl (not issues.jsonl) when git tracks beads.jsonl

---

### Fix C: Init Safety Check (P0, Simple, <1h)

**Location**: cmd/bd/init.go after line 150

**Add After Import Attempt**:
```go
// Safety check: verify import succeeded
stats, err := store.GetStatistics(ctx)
if err == nil && stats.TotalIssues == 0 {
    // DB empty after init - check if git has issues we failed to import
    recheck, _ := checkGitForIssues()
    if recheck > 0 {
        fmt.Fprintf(os.Stderr, "\n❌ ERROR: Database empty but git has %d issues!\n", recheck)
        fmt.Fprintf(os.Stderr, "Auto-import failed. Manual recovery:\n")
        fmt.Fprintf(os.Stderr, "  git show HEAD:%s | bd import -i /dev/stdin\n", jsonlPath)
        fmt.Fprintf(os.Stderr, "Or:\n")
        fmt.Fprintf(os.Stderr, "  bd import -i %s\n", jsonlPath)
        os.Exit(1)
    }
}
```

**Impact**: Prevents silent data loss by failing loudly with recovery instructions

---

### Fix D: Daemon Startup Import (P1, Simple, <1h)

**Location**: cmd/bd/daemon.go after DB open (around line 914)

**Add After Database Open**:
```go
// Check for empty DB with issues in git
ctx := context.Background()
stats, err := store.GetStatistics(ctx)
if err == nil && stats.TotalIssues == 0 {
    issueCount, jsonlPath := checkGitForIssues()
    if issueCount > 0 {
        log(fmt.Sprintf("Empty database but git has %d issues, importing...", issueCount))
        if err := importFromGit(ctx, dbPath, store, jsonlPath); err != nil {
            log(fmt.Sprintf("Warning: startup import failed: %v", err))
        } else {
            log(fmt.Sprintf("Successfully imported %d issues from git", issueCount))
        }
    }
}
```

**Impact**: Daemon auto-recovers from empty DB on startup

### Medium Term (P1)
1. **Multiple database warning** (bd-112)
   - Detect multiple `.beads/` in workspace hierarchy
   - Warn user on startup
   - Prevent accidental database pollution

2. **Better error messages**
   - `bd init`: "Warning: found beads.jsonl in git with N issues"
   - `bd stats`: "Warning: database empty but git has tracked JSONL"
   - Guide user to recovery path

### Implementation Refinements (Critical)

**Fix B Missing Helper Function**:
The oracle's Fix B pseudocode calls `checkGitForJSONLFilename()` which doesn't exist. Need to add:
```go
// checkGitForJSONLFilename returns just the filename from git HEAD check
func checkGitForJSONLFilename() string {
    _, relPath := checkGitForIssues()
    if relPath == "" {
        return ""
    }
    return filepath.Base(relPath)
}
```

**Alternative Simpler Approach for Fix B**:
Instead of making `findJSONLPath()` git-aware, ensure import immediately exports to local file:
```go
// In cmd/bd/init.go after successful importFromGit (line 148):
if err := importFromGit(ctx, initDBPath, store, jsonlPath); err != nil {
    // ...error handling...
} else {
    // CRITICAL: Immediately export to local to prevent daemon race
    localPath := filepath.Join(".beads", filepath.Base(jsonlPath))
    if err := exportToJSONL(ctx, store, localPath); err != nil {
        fmt.Fprintf(os.Stderr, "Warning: failed to export after import: %v\n", err)
    }
    fmt.Fprintf(os.Stderr, "✓ Successfully imported %d issues from git.\n\n", issueCount)
}
```

**Race Condition Warning**:
After `rm -rf .beads/`, there's a timing window:
1. `bd init` runs, imports from git's `beads.jsonl`
2. Import schedules auto-flush (5-second debounce)
3. Daemon auto-starts before flush completes
4. Daemon calls `findJSONLPath()` → no local file yet → creates wrong `issues.jsonl`

**Solution**: Import must **immediately create local JSONL** (no debounce) to win the race.

**Revised Priority**:
- Fix A: P0 - Blocks everything, enables git detection
- Fix C: P0 - Prevents silent failures, critical safety net
- Fix B: P0 - Prevents wrong file creation (OR immediate export)
- Fix D: P1 - Nice recovery but redundant if A+B+C work

### Precedence Rules (All Fixes)

**When checking git HEAD**:
1. First try `.beads/beads.jsonl`
2. Then try `.beads/issues.jsonl`
3. Ignore non-canonical names (archive.jsonl, backup.jsonl, etc.)

**When multiple local JSONL files exist**:
- Use existing `beads.FindJSONLPath()` glob behavior (first match)
- This preserves backward compatibility

### Long Term (P2)
1. **Unified JSONL naming**
   - Standardize on one filename (recommend `beads.jsonl`)
   - Migration path for old `issues.jsonl`
   - Update all code paths consistently
   - Optional: Store chosen JSONL filename in DB metadata

2. **Git-aware init** ✅ PARTIALLY DONE
   - `bd init` should be git-aware ✅ EXISTS (commit 7f82708)
   - Detect tracked JSONL and import automatically ❌ BROKEN (wrong filename)
   - Make this the default happy path ✅ WILL BE FIXED by Fix A

## Implementation Plan (Epic Structure)

**Epic**: Fix database reinitialization data loss bug

**Child Issues** (in dependency order):
1. **Fix A**: checkGitForIssues() filename detection (P0, <1h)
   - Update autoimport.go:70-96 to try beads.jsonl then issues.jsonl
   - Test: verify detects both filenames in git
   - Blocks: Fix C (needs working detection)

2. **Fix B-Alt**: Immediate export after import (P0, <1h)
   - In init.go after importFromGit(), immediately call exportToJSONL()
   - Prevents daemon race condition
   - Simpler than making findJSONLPath() git-aware
   - Test: verify local JSONL created with correct filename

3. **Fix C**: Init safety check (P0, <1h)
   - Add post-init verification in init.go
   - Error and exit if DB empty but git has issues
   - Depends: Fix A (uses checkGitForIssues)
   - Test: verify fails loudly when import fails

4. **Fix D**: Daemon startup import (P1, <1h)
   - Add empty-DB check on daemon startup
   - Auto-import if git has issues
   - Depends: Fix A (uses checkGitForIssues)
   - Test: verify daemon recovers from empty DB

5. **Integration tests** (P0, 1-2h)
   - Test fresh clone scenario
   - Test `rm -rf .beads/` scenario
   - Test daemon race condition (start daemon immediately after init)
   - Test both beads.jsonl and issues.jsonl in git

**Estimated Total**: 5-7 hours

## Related Issues

- **bd-112**: Warn when multiple beads databases detected (filed in ~/src/beads)
- **GH #142**: External_ref import feature (not directly related but shows import complexity)
- Commit d1d3fcd: Renamed `issues.jsonl → beads.jsonl`
- Commit 7f82708: "Fix bd init to auto-import issues from git on fresh clone"

## Test Cases Needed

1. **Fresh Clone Scenario**
   ```bash
   git clone repo
   cd repo
   bd init
   # Should auto-import from .beads/beads.jsonl
   # Should create local .beads/beads.jsonl immediately
   bd stats --json | jq '.total_issues'  # Should match git count
   ```

2. **Database Removal Scenario (Primary Bug)**
   ```bash
   rm -rf .beads/
   bd init
   # Should detect git-tracked JSONL and import
   bd stats --json | jq '.total_issues'  # Should be >0, not 0
   ls .beads/*.jsonl  # Should be beads.jsonl, NOT issues.jsonl
   ```

3. **Race Condition Scenario (Daemon Startup)**
   ```bash
   rm -rf .beads/
   bd init &  # Start init in background
   sleep 0.1
   bd ready   # Triggers daemon auto-start
   wait
   # Daemon should NOT create issues.jsonl
   # Should use beads.jsonl from git
   ls .beads/*.jsonl
   ```

4. **Legacy Filename Support (issues.jsonl)**
   ```bash
   # Git has .beads/issues.jsonl (not beads.jsonl)
   rm -rf .beads/
   bd init
   # Should still import correctly
   ls .beads/*.jsonl  # Should be issues.jsonl (matches git)
   ```

5. **Multiple Workspace Scenario**
   ```bash
   # Two separate clones
   ~/src/beads/        # database 1
   ~/src/fred/beads/   # database 2
   # Each should maintain separate state correctly
   # Each should use correct JSONL filename from its own git
   ```

6. **Daemon Restart Scenario**
   ```bash
   bd daemon --stop
   rm .beads/bd.db
   bd daemon  # auto-start
   # Should import from git on startup
   bd stats --json | jq '.total_issues'  # Should be >0
   ```

7. **Init Safety Check Scenario**
   ```bash
   # Simulate import failure
   rm -rf .beads/
   chmod 000 .beads  # Prevent creation
   bd init 2>&1 | grep ERROR
   # Should fail with clear error, not silent success
   ```

## Root Cause Analysis - CONFIRMED

### Primary Bug: Hardcoded Filename in checkGitForIssues()

**File**: `cmd/bd/autoimport.go:76`
**Problem**: Hardcoded to `"issues.jsonl"` but git tracks `"beads.jsonl"`

```go
// Line 76 - HARDCODED FILENAME
relPath, err := filepath.Rel(gitRoot, filepath.Join(beadsDir, "issues.jsonl"))
```

### Secondary Bug: Daemon Creates Wrong JSONL File

**File**: `cmd/bd/main.go:findJSONLPath()`, `beads.go:FindJSONLPath()`
**Problem**: When no local JSONL exists, defaults to `"issues.jsonl"` without checking git HEAD

**Code Flow**:
1. `FindJSONLPath()` globs for `*.jsonl` in `.beads/` (line 137)
2. If none found, defaults to `"issues.jsonl"` (line 144)
3. Daemon exports to empty `issues.jsonl`, ignoring `beads.jsonl` in git

### Why Auto-Import Failed

1. **bd init** called `checkGitForIssues()` → looked for `HEAD:.beads/issues.jsonl`
2. Git only has `HEAD:.beads/beads.jsonl` → check returned 0 issues
3. No import triggered, DB stayed empty
4. Daemon started, called `findJSONLPath()` → found no local JSONL
5. Defaulted to `issues.jsonl`, exported 0 issues to empty file
6. **Silent data loss complete**

## Questions for Investigation

1. ✅ Why did auto-import not trigger after `bd init`?
   - **ANSWERED**: checkGitForIssues() hardcoded to issues.jsonl, git has beads.jsonl
2. ✅ Is there auto-import code that's not being called?
   - **ANSWERED**: Auto-import code ran but found 0 issues due to wrong filename
3. ✅ When should daemon vs CLI handle import?
   - **ANSWERED**: Both should handle; daemon on startup if DB empty + git has JSONL
4. ✅ Should we enforce single JSONL filename across codebase?
   - **ANSWERED**: Support both with precedence: beads.jsonl > issues.jsonl
5. ✅ How do we prevent this silent data loss in future?
   - **ANSWERED**: See proposed fixes below

## Severity Justification: P0

This is a **critical data loss bug**:
- ✅ Silent failure (no error, no warning)
- ✅ Complete data loss (0 issues after 202)
- ✅ Core workflow broken (init + auto-import)
- ✅ Multi-workspace scenarios broken
- ✅ User cannot recover without manual intervention
- ✅ Breaks trust in beads reliability

**Recommendation**: Investigate and fix immediately before 1.0 release.
