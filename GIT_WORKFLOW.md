# Git Workflow for bd Databases

> **Note**: This document contains historical analysis of binary SQLite workflows. **The current recommended approach is JSONL-first** (see README.md). This document is kept for reference and understanding the design decisions.

## TL;DR

**Current Recommendation (2025)**: Use JSONL text format as source of truth. See README.md for the current workflow.

**Historical Analysis Below**: This documents the binary SQLite approach and why we moved to JSONL.

---

## The Problem

SQLite databases are **binary files**. Git cannot automatically merge them like text files.

```bash
$ git merge feature-branch
warning: Cannot merge binary files: .beads/myapp.db (HEAD vs. feature-branch)
CONFLICT (content): Merge conflict in .beads/myapp.db
```

When two developers create issues concurrently and try to merge:
- Git detects a conflict
- You must choose "ours" or "theirs" (lose one side's changes)
- OR manually export/import data (tedious)

---

## Solution 1: Binary in Git with Protocol (Recommended for Small Teams)

**Works for**: 2-10 developers, <500 issues, low-medium velocity

### The Protocol

1. **One person owns the database per branch**
2. **Pull before creating issues**
3. **Push immediately after creating issues**
4. **Use short-lived feature branches**

### Workflow

```bash
# Developer A
git pull origin main
bd create "Fix navbar bug" -p 1
git add .beads/myapp.db
git commit -m "Add issue: Fix navbar bug"
git push origin main

# Developer B (same time)
git pull origin main  # Gets A's changes first
bd create "Add dark mode" -p 2
git add .beads/myapp.db
git commit -m "Add issue: Add dark mode"
git push origin main  # No conflict!
```

### Handling Conflicts

If you DO get a conflict:

```bash
# Option 1: Take remote (lose your local changes)
git checkout --theirs .beads/myapp.db
bd list  # Verify what you got
git commit

# Option 2: Export your changes, take theirs, reimport
bd list --json > my-issues.json
git checkout --theirs .beads/myapp.db
# Manually recreate your issues
bd create "My issue that got lost"
git add .beads/myapp.db && git commit

# Option 3: Union merge with custom script (see below)
```

### Pros
- ✅ Simple: No infrastructure needed
- ✅ Fast: SQLite is incredibly fast
- ✅ Offline-first: Works without network
- ✅ Atomic: Database transactions guarantee consistency
- ✅ Rich queries: Full SQL power

### Cons
- ❌ Binary conflicts require manual resolution
- ❌ Diffs are opaque (can't see changes in git diff)
- ❌ Database size grows over time (but SQLite VACUUM helps)
- ❌ Git LFS might be needed for large projects (>100MB)

### Size Analysis

Empty database: **80KB**
100 issues: **~120KB** (adds ~400 bytes per issue)
1000 issues: **~500KB**
10,000 issues: **~5MB**

**Recommendation**: Use binary in git up to ~500 issues or 5MB.

---

## Solution 2: Text Export Format (Recommended for Medium Teams)

**Works for**: 5-50 developers, any number of issues

### Implementation

Create `bd export` and `bd import` commands:

```bash
# Export to text format (JSON Lines or SQL)
bd export > .beads/myapp.jsonl

# Import from text
bd import < .beads/myapp.jsonl
```

### Workflow

```bash
# Before committing
bd export > .beads/myapp.jsonl
git add .beads/myapp.jsonl
git commit -m "Add issues"

# After pulling
bd import < .beads/myapp.jsonl
```

### Advanced: Keep Both

```
.beads/
├── myapp.db          # Binary database (in .gitignore)
├── myapp.jsonl       # Text export (in git)
└── sync.sh           # Script to sync between formats
```

### Pros
- ✅ Git can merge text files
- ✅ Diffs are readable
- ✅ Conflicts are easier to resolve
- ✅ Scales to any team size

### Cons
- ❌ Requires discipline (must export before commit)
- ❌ Slower (export/import overhead)
- ❌ Two sources of truth (can get out of sync)
- ❌ Merge conflicts still happen (but mergeable)

---

## Solution 3: Shared Database Server (Enterprise)

**Works for**: 10+ developers, high velocity, need real-time sync

### Options

1. **PostgreSQL Backend** (future bd feature)
   ```bash
   export BD_DATABASE=postgresql://host/db
   bd create "Issue"  # Goes to shared Postgres
   ```

2. **SQLite on Shared Filesystem**
   ```bash
   export BD_DATABASE=/mnt/shared/myapp.db
   bd create "Issue"  # Multiple writers work fine with WAL
   ```

3. **bd Server Mode** (future feature)
   ```bash
   bd serve --port 8080  # Run bd as HTTP API
   bd --remote=http://localhost:8080 create "Issue"
   ```

### Pros
- ✅ True concurrent access
- ✅ No merge conflicts
- ✅ Real-time updates
- ✅ Centralized audit trail

### Cons
- ❌ Requires infrastructure
- ❌ Not offline-first
- ❌ More complex
- ❌ Needs authentication/authorization

---

## Solution 4: Hybrid - Short-Lived Branches

**Works for**: Any team size, best of both worlds

### Strategy

1. **main branch**: Contains source of truth database
2. **Feature branches**: Don't commit database changes
3. **Issue creation**: Only on main branch

```bash
# Working on feature
git checkout -b feature-dark-mode
# ... make code changes ...
git commit -m "Implement dark mode"

# Need to create issue? Switch to main first
git checkout main
git pull
bd create "Bug found in dark mode"
git add .beads/myapp.db
git commit -m "Add issue"
git push

git checkout feature-dark-mode
# Continue working
```

### Pros
- ✅ No database merge conflicts (database only on main)
- ✅ Simple mental model
- ✅ Works with existing git workflows

### Cons
- ❌ Issues not tied to feature branches
- ❌ Requires discipline

---

## Recommended Approach by Team Size

### Solo Developer
**Binary in git** - Just commit it. No conflicts possible.

### 2-5 Developers (Startup)
**Binary in git with protocol** - Pull before creating issues, push immediately.

### 5-20 Developers (Growing Team)
**Text export format** - Export to JSON Lines, commit that. Binary in .gitignore.

### 20+ Developers (Enterprise)
**Shared database** - PostgreSQL backend or bd server mode.

---

## Scaling Analysis

How far can binary-in-git scale?

**Experiment**: Simulate concurrent developers

```bash
# 10 developers each creating 10 issues
# If they all pull at same time, create issues, push sequentially:
# - Developer 1: pushes successfully
# - Developer 2: pulls, gets conflict, resolves, pushes
# - Developer 3: pulls, gets conflict, resolves, pushes
# ...
# Result: 9/10 developers hit conflicts

# If they coordinate (pull, create, push immediately):
# - Success rate: ~80-90% (depends on timing)
# - Failed pushes just retry after pull

# Conclusion: Works up to ~10 concurrent developers with retry logic
```

**Rule of Thumb**:
- **1-5 devs**: 95% conflict-free with protocol
- **5-10 devs**: 80% conflict-free, need retry automation
- **10+ devs**: <50% conflict-free, text export recommended

---

## Git LFS

For very large projects (>1000 issues, >5MB database):

```bash
# .gitattributes
*.db filter=lfs diff=lfs merge=lfs -text

git lfs track "*.db"
git add .gitattributes
git commit -m "Track SQLite with LFS"
```

### Pros
- ✅ Keeps git repo small
- ✅ Handles large binaries efficiently

### Cons
- ❌ Requires Git LFS setup
- ❌ Still can't merge binaries
- ❌ LFS storage costs money (GitHub/GitLab)

---

## Custom Merge Driver

For advanced users, create a custom git merge driver:

```bash
# .gitattributes
*.db merge=bd-merge

# .git/config
[merge "bd-merge"]
    name = bd database merger
    driver = bd-merge-tool %O %A %B %P
```

Where `bd-merge-tool` is a script that:
1. Exports both databases to JSON
2. Merges JSON (using git's text merge)
3. Imports merged JSON to database
4. Handles conflicts intelligently (e.g., keep both issues if IDs differ)

This could be a future bd feature:

```bash
bd merge-databases base.db ours.db theirs.db > merged.db
```

---

## For the beads Project Itself

**Recommendation**: Binary in git with protocol

Rationale:
- Small team (1-2 primary developers)
- Low-medium velocity (~10-50 issues total)
- Want dogfooding (eat our own food)
- Want simplicity (no export/import overhead)
- Database will stay small (<1MB)

### Protocol for beads Contributors

1. **Pull before creating issues**
   ```bash
   git pull origin main
   ```

2. **Create issue**
   ```bash
   bd create "Add PostgreSQL backend" -p 2 -t feature
   ```

3. **Commit and push immediately**
   ```bash
   git add .beads/bd.db
   git commit -m "Add issue: PostgreSQL backend"
   git push origin main
   ```

4. **If push fails (someone beat you)**
   ```bash
   git pull --rebase origin main
   # Resolve conflict by taking theirs
   git checkout --theirs .beads/bd.db
   # Recreate your issue
   bd create "Add PostgreSQL backend" -p 2 -t feature
   git add .beads/bd.db
   git rebase --continue
   git push origin main
   ```

5. **For feature branches**
   - Don't commit database changes
   - Create issues on main branch only
   - Reference issue IDs in commits: `git commit -m "Implement bd-42"`

---

## Future Enhancements

### bd export/import (Priority: Medium)

```bash
# JSON Lines format (one issue per line)
bd export --format=jsonl > issues.jsonl
bd import < issues.jsonl

# SQL format (full dump)
bd export --format=sql > issues.sql
bd import < issues.sql

# Delta export (only changes since last)
bd export --since=2025-10-01 > delta.jsonl
```

### bd sync (Priority: High)

Automatic export before git commit:

```bash
# .git/hooks/pre-commit
#!/bin/bash
if [ -f .beads/*.db ]; then
    bd export > .beads/issues.jsonl
    git add .beads/issues.jsonl
fi
```

### bd merge-databases (Priority: Low)

```bash
bd merge-databases --ours=.beads/bd.db --theirs=/tmp/bd.db --output=merged.db
# Intelligently merges:
# - Same issue ID, different fields: prompt user
# - Different issue IDs: keep both
# - Conflicting dependencies: resolve automatically
```

---

## Conclusion

**For beads itself**: Binary in git works great. Just commit `.beads/bd.db`.

**For bd users**:
- Small teams: Binary in git with simple protocol
- Medium teams: Text export format
- Large teams: Shared database server

The key insight: **SQLite is amazing for local storage**, but git wasn't designed for binary merges. Accept this tradeoff and use the right solution for your team size.

**Document in README**: Add a "Git Workflow" section explaining binary vs text approaches and when to use each.
