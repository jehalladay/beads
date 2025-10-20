---
description: Manage dependencies between issues
argument-hint: [command] [from-id] [to-id]
---

Manage dependencies between beads issues.

## Available Commands

- **add**: Add a dependency between issues
  - $1: "add"
  - $2: From issue ID
  - $3: To issue ID
  - $4: Dependency type (blocks, related, parent-child, discovered-from)

- **remove**: Remove a dependency
  - $1: "remove"
  - $2: From issue ID
  - $3: To issue ID

- **tree**: Show dependency tree for an issue
  - $1: "tree"
  - $2: Issue ID

- **cycles**: Detect dependency cycles

## Dependency Types

- **blocks**: Hard blocker (from blocks to) - affects ready queue
- **related**: Soft relationship - for context only
- **parent-child**: Epic/subtask relationship
- **discovered-from**: Track issues found during work

## Examples

- `bd dep add bd-10 bd-20 --type blocks`: bd-10 blocks bd-20
- `bd dep tree bd-20`: Show what blocks bd-20 and what bd-20 blocks
- `bd dep cycles`: Check for circular dependencies
