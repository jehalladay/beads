# Collision Resolution Failure Analysis

**Date:** 2025-10-29
**Incident:** Import with `--resolve-collisions` created duplicate issues after routine `git pull`

## The Incident

1. User performed routine `git pull` in `~/src/fred/beads`
2. JSONL file updated from remote (canonical database: `~/src/beads`)
3. Import failed with collision detection for: bd-106, bd-108, bd-172, bd-175
4. Used `--resolve-collisions` which remapped them to bd-187, bd-188, bd-189, bd-190
5. **Result:** Database now has BOTH versions - 6 duplicate issues created

## The Evidence

### Canonical Database (~/src/beads)
- Total issues: 509
- JSONL lines: 165
- bd-106: status=closed
- bd-108: status=closed
- bd-172: status=open
- bd-175: status=open

### Polluted Database (~/src/fred/beads)
- Total issues: 515 (6 extra)
- JSONL lines: 171 (6 extra - includes the remapped duplicates)
- **Original issues:**
  - bd-106: status=**open** (should be closed)
  - bd-108: status=closed ✓
  - bd-172: status=open ✓
  - bd-175: status=open ✓
- **Duplicate issues created:**
  - bd-187: "Import validation falsely reports data loss" (originally bd-106)
  - bd-188: "Fix multi-round convergence" (duplicate of bd-108)
  - bd-189: "Delete collision resolution code" (duplicate of bd-172)
  - bd-190: "Test: N-clone scenario" (duplicate of bd-175)

### The JSONL State
The JSONL file now contains BOTH versions:
- bd-106 (closed) - from remote
- bd-187 (open) - remapped local version
- bd-108, bd-188 - duplicates
- bd-172, bd-189 - duplicates
- bd-175, bd-190 - duplicates

## Root Cause Analysis

### What Should Have Happened
1. Import detects bd-106 in JSONL has same ID as database
2. Compares content: JSONL version is newer/different
3. **Updates the existing bd-106 in database** (status: open → closed)
4. No duplicates created

### What Actually Happened
1. Import detected bd-106 as a "collision"
2. Collision resolution assumed: "Two different issues both want ID bd-106"
3. Remapped local bd-106 → bd-187
4. Imported JSONL bd-106 as new issue
5. **Result:** Both versions now exist

### The Fundamental Design Flaw

**Collision resolution conflates two completely different scenarios:**

#### Scenario A: Normal Update (NOT a collision)
- JSONL has bd-106 (status=closed)
- Database has bd-106 (status=open)
- **This is a normal update** - JSONL is source of truth after git pull
- **Should:** Update database bd-106 to match JSONL
- **Should NOT:** Treat as collision

#### Scenario B: Actual Collision (IS a collision)
- Branch A creates bd-106: "Fix authentication bug"
- Branch B creates bd-106: "Add new feature"
- Both branches merge into main
- **This is a true collision** - two different issues want same ID
- **Should:** Remap one to new ID, keep both

### The Broken Logic

The current collision detection logic appears to be:
```
if (JSONL.id exists in database && JSONL.content != database.content) {
    collision = true
}
```

This is **catastrophically wrong** because it treats every update as a collision.

### What It Should Be

**Idempotent import logic:**
```
if (JSONL.id exists in database) {
    if (JSONL.content_hash == database.content_hash) {
        // Exact match - skip
    } else {
        // UPDATE the existing issue
        database.update(JSONL.id, JSONL.content)
    }
}
```

**Collision detection (only for branch merges):**
```
Collisions only occur when:
1. Git merge conflict in JSONL file (<<<<<<< markers present)
2. Two JSONL entries have same ID but different content
3. User explicitly wants to keep both versions
```

## Why This Keeps Happening

1. **Conceptual confusion:** "Collision" is being used for two different things:
   - Content difference (normal update)
   - ID conflict (actual collision)

2. **Wrong default:** Import should default to "update on ID match"
   - Current: Default to "collision on content difference"

3. **Tool misuse:** `--resolve-collisions` is being used for normal imports
   - Should only be needed for branch merge scenarios

4. **No distinction:** Code doesn't distinguish between:
   - Import after `git pull` (JSONL is authoritative)
   - Import after branch merge (need conflict resolution)

## The Correct Mental Model

### Import Modes

1. **Normal Import (default):**
   - Purpose: Sync database with JSONL (source of truth)
   - Behavior: Update issues on ID match, create new ones
   - Use case: After `git pull`, switching branches, fresh clone
   - Should NEVER create duplicates

2. **Collision Resolution Import:**
   - Purpose: Merge two independent databases that both created same IDs
   - Behavior: Remap conflicting IDs to preserve both versions
   - Use case: Branch merge where two devs independently created bd-42
   - Creates duplicates BY DESIGN (but with different content)

### The Missing Piece: Import Intent

The import command needs to know:
- "Trust the JSONL, update my database" (normal mode)
- "JSONL and database are both valid, resolve conflicts" (collision mode)

Current implementation assumes EVERY import is a collision scenario.

## Immediate Impact

User now has:
- 6 duplicate issues polluting the database
- Incorrect JSONL synced to remote (if pushed)
- Canonical database potentially corrupted
- Zero trust in the import system

## The Solution Architecture

### Phase 1: Fix Import Default Behavior
```go
// Default import: JSONL is source of truth
func Import(jsonlPath string) error {
    for each issue in JSONL {
        if issue.ID exists in DB {
            if issue.content_hash == DB.content_hash {
                skip // identical
            } else {
                UPDATE DB issue // JSONL wins
            }
        } else {
            CREATE new issue
        }
    }
}
```

### Phase 2: Collision Resolution (Separate Mode)
```bash
# Only when you KNOW you have a collision (branch merge)
bd import --resolve-collisions

# Should ONLY be used when:
# 1. Git shows merge conflict in JSONL
# 2. You want to preserve both versions
# 3. You understand duplicates will be created
```

### Phase 3: Collision Detection (During Merge)
```bash
# Helper for detecting actual collisions in JSONL
bd detect-collisions

# Shows:
# - Issues with same ID, different content in JSONL conflict markers
# - Suggests resolution strategies
# - DOES NOT modify database
```

## Testing Strategy

### Test 1: Normal Update (Currently Broken)
```bash
# Setup: Database has bd-42 (status=open)
# JSONL has bd-42 (status=closed)
bd import -i issues.jsonl

# Expected: bd-42 updated to status=closed
# Actual: Collision detected, bd-42 remapped to bd-XXX
# FAILURE ❌
```

### Test 2: Actual Collision
```bash
# Setup: Branch merge creates duplicate bd-42 in JSONL
# bd-42 (title="Fix bug") in HEAD
# bd-42 (title="Add feature") in BASE
bd import -i issues.jsonl --resolve-collisions

# Expected: Remap one to bd-XXX, keep both
# Actual: TBD
```

### Test 3: Idempotent Import
```bash
# Import same JSONL twice
bd import -i issues.jsonl
bd import -i issues.jsonl

# Expected: Second import is no-op
# Actual: TBD
```

## Recommended Actions

### Immediate (User Recovery)
1. Identify all duplicate pairs (bd-106/bd-187, etc.)
2. Manually merge duplicates using `bd merge`
3. Export clean database to JSONL
4. Force push to reset remote

### Short-term (Fix Import)
1. **Create bd-XXX:** Rewrite import logic to default to UPDATE, not collision
2. **Create bd-XXX:** Add `--merge-mode` flag for actual collisions
3. **Create bd-XXX:** Write comprehensive import tests
4. **Create bd-XXX:** Document when to use collision resolution

### Long-term (Prevent Recurrence)
1. **Create bd-XXX:** Add import validation (detect duplicates before committing)
2. **Create bd-XXX:** Add `bd validate` command to check database health
3. **Create bd-XXX:** Remove collision resolution entirely (use merge tools instead)
4. **Create bd-XXX:** Implement content-addressable IDs to prevent collisions

## Historical Context

This is NOT the first time this has happened:
- Multiple prior incidents of duplicate issues
- Repeated attempts to fix collision resolution
- bd-94: Epic to fix N-way collision convergence
- bd-109: Transaction + retry logic for collisions
- **Pattern:** We keep treating symptoms, not root cause

## The Deeper Problem

**beads is trying to solve distributed consensus without a consensus algorithm.**

We're essentially trying to do what Git does (merge distributed changes) but:
- Git uses content-addressable storage (SHA hashes)
- Git has explicit merge semantics
- Git forces users to resolve conflicts manually
- **beads assumes automatic resolution is possible**

The N-way collision problem, the import pollution, the duplicate issues - they're all symptoms of trying to sync mutable IDs across independent actors.

## Conclusion

The collision resolution feature is fundamentally broken because:
1. It treats normal updates as collisions
2. It has no concept of "source of truth"
3. It creates duplicates when it should update
4. It's being used for the wrong use case

**The fix is not to improve collision resolution.**
**The fix is to make normal import work correctly first.**
**Then, collision resolution becomes a rare edge case for branch merges.**

Until then, every `git pull` is a potential database corruption event.
