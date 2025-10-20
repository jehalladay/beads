---
description: Manage work across multiple repositories
argument-hint: [command]
---

Manage work across multiple repositories when using a global daemon.

**Requires**: Running global daemon (`bd daemon --global`)

## Available Commands

- **list**: List all cached repositories
- **ready**: Show ready work across all repositories
  - `--group`: Group results by repository
- **stats**: Show combined statistics across all repositories
- **clear-cache**: Clear all cached repository connections

## Usage

- `bd repos list` - See all repositories connected to global daemon
- `bd repos ready` - View all ready work across projects
- `bd repos ready --group` - Group ready work by repository
- `bd repos stats` - Combined statistics from all repos

Useful for managing multiple beads projects from a single global daemon.
