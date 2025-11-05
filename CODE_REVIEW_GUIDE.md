# Code Review Guide: Parent Resurrection Feature (bd-d19a)

**Branch**: `fix/import-missing-parents`  
**Epic**: bd-d19a - Fix import failure on missing parent issues  
**Reviewer Instructions**: Follow this guide to perform a systematic code review, filing issues for any problems found.

---

## Overview

This branch implements a **parent resurrection** feature that allows beads to gracefully handle deleted parent issues during import. When a child issue references a missing parent, the system:

1. Searches JSONL history for the deleted parent
2. Creates a tombstone (status=closed) to preserve referential integrity
3. Allows the import to proceed instead of failing atomically

**Key Files Changed**:
- `internal/storage/sqlite/resurrection.go` (NEW)
- `internal/storage/sqlite/sqlite.go` (CreateIssue modifications)
- `internal/storage/sqlite/ids.go` (EnsureIDs modifications)
- `internal/storage/sqlite/child_id_test.go` (test updates)

---

## Critical Review Areas

### 1. **BACKWARDS COMPATIBILITY** ⚠️ HIGHEST PRIORITY

**Why Critical**: Existing beads installations must continue to work without migration, data loss, or behavior changes.

#### 1.1 Database Schema
- [ ] **Review**: Check if any new columns/tables were added to schema
- [ ] **Test**: Confirm old databases work without migration
- [ ] **Verify**: No schema version bump required for resurrection feature
- [ ] **Edge Case**: What happens if user downgrades bd after using resurrection?
- [ ] **Issue**: File bug if resurrection creates tombstones that old versions can't handle

**Files**: `internal/storage/sqlite/schema.go`, `internal/storage/sqlite/resurrection.go`

#### 1.2 JSONL Format
- [ ] **Review**: Confirm JSONL export/import format unchanged
- [ ] **Test**: Old JSONL files can be imported by new version
- [ ] **Test**: New JSONL files (with resurrected parents) can be read by old versions
- [ ] **Verify**: No new fields added to Issue struct that break old parsers
- [ ] **Issue**: File bug if format is incompatible

**Files**: `internal/types/types.go`, `internal/importer/importer.go`

#### 1.3 API/CLI Behavior Changes
- [ ] **Review**: Check if any existing commands have different behavior
- [ ] **Test**: `bd create` with hierarchical IDs still works as before
- [ ] **Test**: `bd import` still works for normal (non-deleted-parent) cases
- [ ] **Verify**: Error messages for truly invalid cases unchanged
- [ ] **Issue**: File bug if existing workflows break

**Files**: `cmd/bd/*.go`

#### 1.4 Error Message Changes
- [ ] **Review**: Document all error message changes (breaking for scripts/tests)
- [ ] **Check**: `internal/storage/sqlite/child_id_test.go:200` - error message updated, why?
- [ ] **Verify**: All tests updated to match new error messages
- [ ] **Issue**: File bug if error messages are worse/less informative than before

**Key Question**: Did we change `"parent issue X does not exist"` to `"parent issue X does not exist and could not be resurrected from JSONL history"`? Is this acceptable?

---

### 2. **Transaction Safety** ⚠️ HIGH PRIORITY

**Why Critical**: SQLite is sensitive to transaction conflicts. The resurrection feature must participate in existing transactions correctly.

#### 2.1 Connection/Transaction Handling
- [ ] **Review**: `resurrection.go` - verify all DB operations use the provided connection
- [ ] **Check**: `tryResurrectParentWithConn()` uses `conn` parameter, not `s.db`
- [ ] **Verify**: No calls to `s.db.Conn()` inside transaction-aware functions
- [ ] **Test**: Run `TestImportWithDeletedParent` to confirm no "database is locked" errors
- [ ] **Edge Case**: What if resurrection is called outside a transaction?
- [ ] **Issue**: File bug if any transaction conflict scenarios remain

**Files**: `internal/storage/sqlite/resurrection.go:38-104`

#### 2.2 Rollback Behavior
- [ ] **Review**: If resurrection fails mid-chain (bd-abc.1.2 → bd-abc fails), does it rollback?
- [ ] **Check**: `tryResurrectParentChainWithConn()` error handling
- [ ] **Verify**: Failed resurrection doesn't leave partial tombstones in DB
- [ ] **Test**: Create test case for partial resurrection failure
- [ ] **Issue**: File bug if rollback behavior is incorrect

**Files**: `internal/storage/sqlite/resurrection.go:189-206`

---

### 3. **Resurrection Logic Correctness**

#### 3.1 Parent Chain Resurrection
- [ ] **Review**: `extractParentChain()` correctly extracts all parent IDs
- [ ] **Test**: Multi-level hierarchy (bd-abc.1.2.3) resurrects bd-abc, bd-abc.1, bd-abc.1.2 in order
- [ ] **Edge Case**: What if JSONL has bd-abc.1.2 but not bd-abc.1? Does it fail gracefully?
- [ ] **Verify**: Root-to-leaf ordering (depth 0 → 1 → 2 → 3)
- [ ] **Issue**: File bug if chain resurrection can fail silently

**Files**: `internal/storage/sqlite/resurrection.go:189-206`, `resurrection.go:207-221`

#### 3.2 JSONL Search
- [ ] **Review**: `findIssueInJSONL()` - can it handle large JSONL files (>10MB)?
- [ ] **Performance**: Line-by-line scanning is O(n) - acceptable?
- [ ] **Edge Case**: What if JSONL is malformed/corrupted?
- [ ] **Edge Case**: What if issue appears multiple times in JSONL (updates)?
- [ ] **Verify**: Uses latest version if issue appears multiple times
- [ ] **Issue**: File bug if JSONL search has correctness or performance issues

**Files**: `internal/storage/sqlite/resurrection.go:116-179`

**Key Question**: Does it pick the FIRST or LAST occurrence of an issue in JSONL? (JSONL may have updates)

#### 3.3 Tombstone Creation
- [ ] **Review**: Tombstone fields - are they correct?
  - Status: `closed` ✓
  - Priority: `4` (lowest) ✓
  - Description: `[RESURRECTED]` prefix ✓
  - Timestamps: CreatedAt from original, UpdatedAt/ClosedAt = now ✓
- [ ] **Edge Case**: What if original issue had dependencies? Are they resurrected?
- [ ] **Check**: Lines 79-95 - dependency resurrection is "best effort", acceptable?
- [ ] **Verify**: Tombstone doesn't trigger export/sync loops
- [ ] **Issue**: File bug if tombstone creation causes side effects

**Files**: `internal/storage/sqlite/resurrection.go:48-95`

---

### 4. **Integration Points**

#### 4.1 CreateIssue Integration
- [ ] **Review**: `sqlite.go:182-196` - resurrection called before `insertIssue()`
- [ ] **Verify**: Resurrection only happens for hierarchical IDs (contains ".")
- [ ] **Edge Case**: What if user manually creates issue with ID "bd-abc.1" but parent exists?
- [ ] **Performance**: Does resurrection check happen on EVERY CreateIssue call?
- [ ] **Issue**: File bug if resurrection adds unnecessary overhead

**Files**: `internal/storage/sqlite/sqlite.go:182-196`

#### 4.2 EnsureIDs Integration
- [ ] **Review**: `ids.go:189-202` - resurrection in batch ID validation
- [ ] **Verify**: Works correctly during import (batch operations)
- [ ] **Edge Case**: What if 100 issues all reference same missing parent?
- [ ] **Performance**: Is parent resurrected once or 100 times?
- [ ] **Issue**: File bug if resurrection is inefficient in batch scenarios

**Files**: `internal/storage/sqlite/ids.go:189-202`

#### 4.3 Import Flow
- [ ] **Review**: Does topological sort + resurrection work together correctly?
- [ ] **Check**: `importer.go:540-558` - depth sorting happens BEFORE EnsureIDs
- [ ] **Verify**: Resurrection is a fallback, not the primary mechanism
- [ ] **Test**: Import batch with mix of new hierarchical issues + deleted parent refs
- [ ] **Issue**: File bug if import flow has race conditions or ordering issues

**Files**: `internal/importer/importer.go:540-558`

---

### 5. **Testing Coverage**

#### 5.1 Existing Tests Updated
- [ ] **Review**: `child_id_test.go:200` - why was error message changed?
- [ ] **Verify**: All tests pass with `go test ./internal/storage/sqlite/...`
- [ ] **Check**: Integration test `TestImportWithDeletedParent` passes
- [ ] **Issue**: File bug for any test failures or inadequate test updates

#### 5.2 Missing Test Cases (File issues for these!)
- [ ] **Test Case**: Multi-level resurrection (bd-abc.1.2 with missing bd-abc and bd-abc.1)
- [ ] **Test Case**: Resurrection when JSONL doesn't exist at all
- [ ] **Test Case**: Resurrection when JSONL exists but parent never existed
- [ ] **Test Case**: Resurrection with corrupted JSONL file
- [ ] **Test Case**: Resurrection during concurrent operations (race conditions)
- [ ] **Test Case**: Backwards compat - old DB with new code, new DB with old code
- [ ] **Test Case**: Performance test - resurrect parent with 1000 children
- [ ] **Test Case**: Dependency resurrection - parent had dependencies, are they restored?

**Files**: `beads_integration_test.go`, `internal/storage/sqlite/*_test.go`

---

### 6. **Edge Cases & Error Handling**

#### 6.1 Error Handling
- [ ] **Review**: All error paths return meaningful messages
- [ ] **Check**: JSONL file doesn't exist - returns `nil, nil` (acceptable?)
- [ ] **Check**: Parent not found in JSONL - returns `false, nil` (acceptable?)
- [ ] **Check**: Database locked - returns error (handled by transaction refactor)
- [ ] **Verify**: No silent failures that leave DB in inconsistent state
- [ ] **Issue**: File bug for any unclear or missing error messages

#### 6.2 Pathological Cases
- [ ] **Edge Case**: Circular references (bd-abc.1 parent is bd-abc.1.2)?
- [ ] **Edge Case**: ID with 10+ levels of nesting (bd-abc.1.2.3.4.5.6.7.8.9.10)?
- [ ] **Edge Case**: Issue ID contains multiple dots in hash (bd-abc.def.1)?
- [ ] **Edge Case**: JSONL has 1 million issues - how long does resurrection take?
- [ ] **Edge Case**: Resurrection triggered during daemon auto-sync - conflicts?
- [ ] **Issue**: File bug for any unhandled pathological cases

---

### 7. **Documentation & User Impact**

#### 7.1 User-Facing Documentation
- [ ] **Review**: Is resurrection behavior documented in README.md?
- [ ] **Review**: Is it documented in AGENTS.md (for AI users)?
- [ ] **Review**: Should there be a tombstone cleanup command (bd compact-tombstones)?
- [ ] **Verify**: Error messages guide users to solutions
- [ ] **Issue**: File doc bug if feature is undocumented

#### 7.2 Migration Path
- [ ] **Review**: Do users need to do anything when upgrading?
- [ ] **Review**: What happens to existing orphaned children in old DBs?
- [ ] **Verify**: Feature is opt-in or backwards compatible by default?
- [ ] **Issue**: File bug if migration path is unclear

---

## Review Workflow

### Step 1: Read the Code
```bash
# Review core resurrection logic
cat internal/storage/sqlite/resurrection.go

# Review integration points
cat internal/storage/sqlite/sqlite.go
cat internal/storage/sqlite/ids.go

# Review tests
cat internal/storage/sqlite/child_id_test.go
cat beads_integration_test.go | grep -A 30 "TestImportWithDeletedParent"
```

### Step 2: Run Tests
```bash
# Unit tests
go test ./internal/storage/sqlite/... -v

# Integration tests
go test -v ./beads_integration_test.go -run TestImportWithDeletedParent

# Full test suite
go test ./...
```

### Step 3: Test Backwards Compatibility Manually
```bash
# Create old-style database
git checkout main
./bd init --prefix test
./bd create "Parent" --id test-parent
./bd create "Child" --id test-parent.1

# Switch to new branch and verify it works
git checkout fix/import-missing-parents
./bd show test-parent test-parent.1  # Should work

# Test resurrection
./bd delete test-parent
./bd sync
# Edit JSONL to add back child reference, then import
./bd import -i .beads/issues.jsonl
./bd show test-parent  # Should show tombstone
```

### Step 4: File Issues
For each problem found, create an issue:
```bash
./bd create "BUG: [description]" -t bug -p 0 --deps discovered-from:bd-d19a
```

---

## Specific Code Snippets to Review

### Critical: Transaction Handling
**File**: `internal/storage/sqlite/resurrection.go:67-76` (BEFORE fix)
```go
// Get a connection for the transaction
conn, err := s.db.Conn(ctx)
if err != nil {
    return false, fmt.Errorf("failed to get database connection: %w", err)
}
defer conn.Close()

// Insert tombstone into database
if err := insertIssue(ctx, conn, tombstone); err != nil {
    return false, fmt.Errorf("failed to create tombstone for parent %s: %w", parentID, err)
}
```

**Question**: Does this create a NEW connection inside an existing transaction? (YES - this was bd-58c0 bug, fixed by refactor)

### Critical: Error Message Change
**File**: `internal/storage/sqlite/child_id_test.go:200`
```go
// OLD:
if err != nil && err.Error() != "parent issue bd-nonexistent does not exist" {

// NEW:
expectedErr := "parent issue bd-nonexistent does not exist and could not be resurrected from JSONL history"
if err != nil && err.Error() != expectedErr {
```

**Question**: Is this error message change acceptable for backwards compatibility? (Scripts parsing errors may break)

### Critical: JSONL Lookup Logic
**File**: `internal/storage/sqlite/resurrection.go:138-156`
```go
for scanner.Scan() {
    lineNum++
    line := scanner.Text()
    
    // Quick check: does this line contain our issue ID?
    if !strings.Contains(line, `"`+issueID+`"`) {
        continue
    }
    
    // Parse JSON
    var issue types.Issue
    if err := json.Unmarshal([]byte(line), &issue); err != nil {
        fmt.Fprintf(os.Stderr, "Warning: skipping malformed JSONL line %d: %v\n", lineNum, err)
        continue
    }
    
    if issue.ID == issueID {
        return &issue, nil  // Returns FIRST match
    }
}
```

**Question**: Should this return the LAST match instead (latest version of issue)? JSONL may have updates.

---

## Sign-Off Checklist

Before approving this branch for merge, confirm:

- [ ] All backwards compatibility concerns addressed
- [ ] No schema migration required
- [ ] No JSONL format changes
- [ ] Transaction safety verified (no "database is locked" errors)
- [ ] Error messages are informative and backwards-compatible
- [ ] Integration tests pass (`TestImportWithDeletedParent`)
- [ ] Unit tests pass (`go test ./internal/storage/sqlite/...`)
- [ ] Documentation updated (README.md, AGENTS.md)
- [ ] Edge cases have test coverage or filed issues
- [ ] Performance acceptable for common cases (resurrect 1 parent with 100 children)

---

## Expected Issues to File

Based on this review, you should file issues for:

1. **JSONL lookup returns first match, not last** - should return latest version
2. **No test for multi-level resurrection** (bd-abc.1.2 with missing bd-abc and bd-abc.1)
3. **No test for resurrection when JSONL doesn't exist**
4. **No test for backwards compat** (old DB → new code, new DB → old code)
5. **Error message change may break user scripts** - document or add deprecation warning
6. **Performance concern**: Resurrection in EnsureIDs may resurrect same parent N times for N children
7. **Missing documentation** - README.md doesn't mention resurrection feature
8. **No cleanup mechanism** for tombstones (consider `bd compact-tombstones` command)

---

## Questions for Original Implementer

1. Why does `findIssueInJSONL()` return the FIRST match instead of LAST?
2. Is the error message change acceptable for backwards compatibility?
3. Should resurrection be opt-in via config flag?
4. What's the performance impact of resurrection on large JSONL files (1M+ issues)?
5. Should tombstones be marked with a special flag (`is_tombstone=true`) in the database?

---

**Good luck with the review!** Be thorough, file issues for everything you find, and don't hesitate to ask questions.
