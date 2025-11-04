# Next Session: Complete bd-d19a (Fix Import Failure on Missing Parents)

## Current Status

**Branch**: `fix/import-missing-parents`  
**Epic**: bd-d19a (P0 - Critical)  
**Progress**: Phase 1 & 2 Complete ✅

### Completed Work

#### Phase 1: Topological Sorting ✅
- **Commit**: `f2cb91d`
- **What**: Added depth-based sorting to `importer.go` to ensure parents are created before children
- **Files**: `internal/importer/importer.go`
- **Result**: Fixes latent ordering bug where parent-child pairs in same batch could fail

#### Phase 2: Parent Resurrection ✅
- **Commit**: `b41d65d`
- **Implemented Issues**:
  - bd-cc4f: `TryResurrectParent` function
  - bd-d76d: Modified `EnsureIDs` to call resurrection
  - bd-02a4: Modified `CreateIssue` to call resurrection
- **Files Created**: `internal/storage/sqlite/resurrection.go`
- **Files Modified**:
  - `internal/storage/sqlite/ids.go`
  - `internal/storage/sqlite/sqlite.go`
  - `internal/storage/sqlite/batch_ops.go`
  - `internal/storage/sqlite/batch_ops_test.go`

**How Resurrection Works**:
1. When child issue has missing parent, search `.beads/issues.jsonl` for parent in git history
2. If found, create tombstone issue (status=closed, priority=4)
3. Tombstone preserves original title, type, created_at
4. Description marked with `[RESURRECTED]` prefix + original description
5. Dependencies copied if targets exist
6. Recursively handles entire parent chains (e.g., `bd-abc.1.2` → resurrects both `bd-abc` and `bd-abc.1`)

---

## Next Steps: Phase 3 - Testing & Documentation

### 1. Add Comprehensive Tests

**Create**: `internal/storage/sqlite/resurrection_test.go`

**Test Cases Needed**:
- ✅ Parent exists → no resurrection needed
- ✅ Parent found in JSONL → successful resurrection
- ✅ Parent not in JSONL → proper error message
- ✅ Multi-level chain (`bd-abc.1.2`) → resurrects entire chain
- ✅ JSONL file missing → graceful failure
- ✅ Malformed JSONL lines → skip with warning
- ✅ Dependencies preserved → only if targets exist
- ✅ Tombstone properties → correct status, priority, description format
- ✅ Concurrent resurrection → idempotent behavior

**Integration Test**:
Add to `beads_integration_test.go`:
```go
TestImportWithDeletedParent
- Create parent and child
- Delete parent
- Export to JSONL (preserves parent in git)
- Clear DB
- Import from JSONL
- Verify: parent resurrected as tombstone, child imported successfully
```

### 2. Update Documentation

**Files to Update**:
1. `README.md` - Add resurrection behavior to import section
2. `QUICKSTART.md` - Mention parent resurrection for multi-repo workflows
3. `docs/import-bug-analysis-bd-3xq.md` - Add "Implementation Complete" section
4. `AGENTS.md` - Document resurrection for AI agents

**Example Addition to README.md**:
```markdown
## Parent Resurrection

When importing issues with hierarchical IDs (e.g., `bd-abc.1`), bd automatically
resurrects deleted parent issues from git history to maintain referential integrity.

Resurrected parents are created as tombstones:
- Status: `closed`
- Priority: 4 (lowest)
- Description: `[RESURRECTED]` prefix + original description

This enables multi-repo workflows where different clones may delete different issues.
```

### 3. Manual Testing Workflow

```bash
# Terminal 1: Create test scenario
cd /tmp/bd-test
git init
bd init --prefix test --quiet
bd create "Parent epic" -t epic -p 1 --json  # Returns test-abc123
bd create "Child task" -p 1 --json            # Auto-creates test-abc123.1

# Verify hierarchy
bd dep tree test-abc123

# Delete parent (simulating normal database hygiene)
bd delete test-abc123 --force

# Export state (child exists, parent deleted)
bd export -o backup.jsonl

# Simulate fresh clone
rm -rf .beads/beads.db
bd init --prefix test --quiet

# Import - should resurrect parent as tombstone
bd import -i backup.jsonl

# Verify resurrection
bd show test-abc123 --json | grep -i resurrected
bd show test-abc123.1 --json  # Should exist
bd dep tree test-abc123  # Should show full tree
```

### 4. Edge Cases to Handle

**Potential Issues**:
1. **JSONL path detection**: Currently assumes `.beads/issues.jsonl` - verify works with symlinks, worktrees
2. **Performance**: Large JSONL files (10k+ issues) - may need optimization (indexing?)
3. **Memory**: Scanner buffer is 1MB - test with very large issue descriptions
4. **Concurrent access**: Multiple processes resurrecting same parent simultaneously

**Optimizations to Consider** (Future work):
- Build in-memory index of JSONL on first resurrection call (cache for session)
- Use `grep` or `ripgrep` for fast ID lookup before JSON parsing
- Add resurrection stats to import summary (`Resurrected: 3 parents`)

### 5. Create Pull Request

Once testing complete:

```bash
# Update CHANGELOG.md
# Add entry under "Unreleased"

# Create PR
gh pr create \
  --title "Fix import failure on missing parent issues (bd-d19a)" \
  --body "Implements topological sorting + parent resurrection.

Fixes #XXX (if there's a GitHub issue)

## Changes
- Phase 1: Topological sorting for import ordering
- Phase 2: Parent resurrection from JSONL history
- Creates tombstones for deleted parents to preserve hierarchical structure

## Testing
- [x] Unit tests for resurrection logic
- [x] Integration test for deleted parent scenario
- [x] Manual testing with multi-level hierarchies

See docs/import-bug-analysis-bd-3xq.md for full design rationale."
```

---

## Commands for Next Session

```bash
# Resume work
cd /Users/stevey/src/dave/beads
git checkout fix/import-missing-parents

# Run existing tests
go test ./internal/storage/sqlite -v -run Resurrection

# Create new test file
# (See test template above)

# Run integration tests
go test -v -run TestImport

# Manual testing
# (See workflow above)

# When ready to merge
git checkout main
git merge fix/import-missing-parents
git push origin main
```

---

## Issues Tracking

**Epic**: bd-d19a (Fix import failure on missing parent issues) - **OPEN**  
**Subtasks**:
- bd-cc4f: Implement TryResurrectParent - **DONE** ✅
- bd-d76d: Modify EnsureIDs - **DONE** ✅
- bd-02a4: Modify CreateIssue - **DONE** ✅
- **TODO**: Create test issue for Phase 3
- **TODO**: Create docs issue for Phase 3

**Files Modified**:
- ✅ `internal/importer/importer.go` (topological sorting)
- ✅ `internal/storage/sqlite/resurrection.go` (new file)
- ✅ `internal/storage/sqlite/ids.go`
- ✅ `internal/storage/sqlite/sqlite.go`
- ✅ `internal/storage/sqlite/batch_ops.go`
- ✅ `internal/storage/sqlite/batch_ops_test.go`
- ⏳ `internal/storage/sqlite/resurrection_test.go` (TODO)
- ⏳ `beads_integration_test.go` (TODO - add import test)
- ⏳ `README.md` (TODO - document resurrection)
- ⏳ `AGENTS.md` (TODO - document for AI agents)

---

## Key Design Decisions

1. **Tombstone Status**: Using `closed` (not a new "deleted" status) to avoid schema changes
2. **Search Strategy**: Linear scan of JSONL (acceptable for <10k issues, can optimize later)
3. **Idempotency**: `TryResurrectParent` checks existence first, safe to call multiple times
4. **Recursion**: `TryResurrectParentChain` handles multi-level hierarchies automatically
5. **Dependencies**: Best-effort resurrection (logs warnings, doesn't fail if targets missing)

---

## Reference Documents

- **Design Doc**: `docs/import-bug-analysis-bd-3xq.md` (comprehensive analysis)
- **Current Branch**: `fix/import-missing-parents`
- **GitHub PR URL**: (To be created)
- **Related Issues**: bd-4ms (multi-repo support), bd-a101 (separate branch workflow)

---

**Status**: Ready for Phase 3 (Testing & Documentation)  
**Estimate**: 2-3 hours for comprehensive tests + 1 hour for docs  
**Risk**: Low - core logic implemented and builds successfully
