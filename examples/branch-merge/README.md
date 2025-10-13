# Branch Merge Workflow with Collision Resolution

This example demonstrates how to handle ID collisions when merging branches that have diverged and created issues with the same IDs.

## The Problem

When two branches work independently and both create issues, they'll often generate overlapping IDs:

```
main:    bd-1, bd-2, bd-3, bd-4, bd-5
feature: bd-1, bd-2, bd-3, bd-6, bd-7  (diverged from main earlier)
```

When you try to merge `feature` into `main`, you'll have ID collisions for bd-1 through bd-3 (if the content differs).

## The Solution

bd provides automatic collision resolution that:
1. Detects collisions (same ID, different content)
2. Renumbers the incoming colliding issues
3. Updates ALL text references and dependencies automatically

## Demo Workflow

### 1. Setup - Two Diverged Branches

```bash
# Start on main branch
git checkout main
bd create "Feature A" -t feature -p 1
bd create "Bug fix B" -t bug -p 0
bd create "Task C" -t task -p 2
bd export -o .beads/issues.jsonl
git add .beads/issues.jsonl
git commit -m "Add main branch issues"

# Create feature branch from an earlier commit
git checkout -b feature-branch HEAD~5

# On feature branch, create overlapping issues
bd create "Different feature A" -t feature -p 2
bd create "Different bug B" -t bug -p 1
bd create "Feature D" -t feature -p 1
bd export -o .beads/issues.jsonl
git add .beads/issues.jsonl
git commit -m "Add feature branch issues"
```

At this point:
- `main` has: bd-1 (Feature A), bd-2 (Bug fix B), bd-3 (Task C)
- `feature-branch` has: bd-1 (Different feature A), bd-2 (Different bug B), bd-3 (Feature D)

The bd-1 and bd-2 on each branch have different content = collisions!

### 2. Merge and Detect Collisions

```bash
# Merge feature branch into main
git checkout main
git merge feature-branch

# Git will show merge conflict in .beads/issues.jsonl
# Manually resolve the conflict by keeping both versions
# (or use a merge tool)

# After resolving the git conflict, check for ID collisions
bd import -i .beads/issues.jsonl --dry-run
```

Output shows:
```
=== Collision Detection Report ===
Exact matches (idempotent): 0
New issues: 1
COLLISIONS DETECTED: 2

Colliding issues:
  bd-1: Different feature A
    Conflicting fields: [title, priority]
  bd-2: Different bug B
    Conflicting fields: [title, priority]
```

### 3. Resolve Collisions Automatically

```bash
# Let bd resolve the collisions
bd import -i .beads/issues.jsonl --resolve-collisions
```

Output shows:
```
Resolving collisions...

=== Remapping Report ===
Issues remapped: 2

Remappings (sorted by reference count):
  bd-1 → bd-4 (refs: 0)
  bd-2 → bd-5 (refs: 0)

All text and dependency references have been updated.

Import complete: 2 created, 0 updated, 1 dependencies added, 2 issues remapped
```

Result:
- `main` keeps: bd-1 (Feature A), bd-2 (Bug fix B), bd-3 (Task C)
- `feature-branch` issues become: bd-4 (Different feature A), bd-5 (Different bug B), bd-3 (Feature D)

### 4. Export and Commit

```bash
# Export the resolved state back to JSONL
bd export -o .beads/issues.jsonl

# Commit the merge
git add .beads/issues.jsonl
git commit -m "Merge feature-branch with collision resolution"
```

## Advanced: Cross-References

If your issues reference each other in text or dependencies, bd updates those automatically:

```bash
# On feature branch, create issues with references
bd create "Feature X" -d "Implements the core logic" -t feature -p 1
# Assume this becomes bd-10

bd create "Test for X" -d "Tests bd-10 functionality" -t task -p 2
# This references bd-10 in the description

bd dep add bd-11 bd-10 --type blocks
# Dependencies are created

# After merge with collision resolution
bd import -i .beads/issues.jsonl --resolve-collisions

# If bd-10 collided and was remapped to bd-15:
# - bd-11's description becomes: "Tests bd-15 functionality"
# - Dependency becomes: bd-11 → bd-15
```

## When to Use This

1. **Feature branches** - Long-lived branches that create issues independently
2. **Parallel development** - Multiple developers working on separate branches
3. **Stale branches** - Old branches that need to be merged but have ID conflicts
4. **Distributed teams** - Teams that work offline and sync via git

## Safety Notes

- `--resolve-collisions` preserves your existing database (current branch's issues never change IDs)
- Only the incoming colliding issues get new IDs
- Use `--dry-run` first to preview what will happen
- All text references use word-boundary matching (bd-10 won't match bd-100)
- The collision resolution is deterministic (same input = same output)

## Alternative: Manual Resolution

If you prefer manual control:

1. Don't use `--resolve-collisions`
2. Manually edit the JSONL file before import
3. Rename colliding IDs to unique values
4. Manually update any cross-references
5. Import normally

This gives you complete control but is more error-prone and time-consuming.

## See Also

- [Git Hooks Example](../git-hooks/) - Automate export/import with git hooks
- [README.md](../../README.md) - Full collision resolution documentation
- [TEXT_FORMATS.md](../../TEXT_FORMATS.md) - JSONL merge strategies
