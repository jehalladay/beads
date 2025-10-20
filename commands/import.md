---
description: Import issues from JSONL format
argument-hint: [-i input-file]
---

Import issues from JSON Lines format (one JSON object per line).

## Usage

- **From stdin**: `bd import` (reads from stdin)
- **From file**: `bd import -i issues.jsonl`
- **Preview**: `bd import -i issues.jsonl --dry-run`
- **Resolve collisions**: `bd import -i issues.jsonl --resolve-collisions`

## Behavior

- **Existing issues** (same ID): Updated with new data
- **New issues**: Created
- **Collisions** (same ID, different content): Detected and reported

## Collision Handling

When merging branches or pulling changes, ID collisions can occur:

- **--dry-run**: Preview collisions without making changes
- **--resolve-collisions**: Automatically remap colliding issues to new IDs
- All text references and dependencies are automatically updated

## Automatic Import

The daemon automatically imports from `.beads/issues.jsonl` when it's newer than the database (e.g., after `git pull`). Manual import is rarely needed.

## Options

- **--skip-existing**: Skip updates to existing issues
- **--strict**: Fail on dependency errors instead of warnings
