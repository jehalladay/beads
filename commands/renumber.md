---
description: Renumber all issues sequentially
argument-hint: [--dry-run] [--force]
---

Renumber all issues sequentially to eliminate gaps in the ID space.

## What It Does

- Renumber all issues starting from 1 (keeps chronological order)
- Update all dependency links (all types)
- Update all text references in descriptions, notes, acceptance criteria
- Show mapping report: old ID -> new ID
- Export updated database to JSONL

## Usage

- **Preview**: `bd renumber --dry-run`
- **Apply**: `bd renumber --force`

## Risks

⚠️ **Warning**: This operation cannot be undone!

- May break external references in GitHub issues, docs, commits
- Git history may become confusing
- Backup recommended before running

Use sparingly - only when ID gaps are problematic.
