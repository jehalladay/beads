# Beads 🔗

**Issues chained together like beads.**

A lightweight, dependency-aware issue tracker with first-class support for tracking blockers and finding ready work.

## Features

- ✨ **Zero setup** - Single binary + SQLite database file
- 🔗 **Dependency tracking** - First-class support for issue dependencies
- 📋 **Ready work detection** - Automatically finds issues with no open blockers
- 🌲 **Dependency trees** - Visualize full dependency graphs
- 🚫 **Blocker analysis** - See what's blocking your issues
- 📊 **Statistics** - Track progress and lead times
- 🎨 **Colored CLI** - Beautiful terminal output
- 💾 **Full audit trail** - Every change is logged

## Installation

```bash
go install github.com/steveyackey/beads/cmd/beads@latest
```

Or build from source:

```bash
git clone https://github.com/steveyackey/beads
cd beads
go build -o beads ./cmd/beads
```

## Quick Start

```bash
# Create your first issue
beads create "Build login page" -d "Need user authentication" -p 1 -t feature

# Create another issue that depends on it
beads create "Add OAuth support" -p 2
beads dep add bd-2 bd-1  # bd-2 depends on bd-1

# See what's ready to work on
beads ready

# Show dependency tree
beads dep tree bd-2
```

## Usage

### Creating Issues

```bash
beads create "Fix bug" -d "Description" -p 1 -t bug
beads create "Add feature" --description "Long description" --priority 2 --type feature
beads create "Task" -l "backend,urgent" --assignee alice
```

Options:
- `-d, --description` - Issue description
- `-p, --priority` - Priority (0-4, 0=highest)
- `-t, --type` - Type (bug|feature|task|epic|chore)
- `-a, --assignee` - Assign to user
- `-l, --labels` - Comma-separated labels

### Viewing Issues

```bash
beads show bd-1              # Show full details
beads list                   # List all issues
beads list --status open     # Filter by status
beads list --priority 1      # Filter by priority
beads list --assignee alice  # Filter by assignee
```

### Updating Issues

```bash
beads update bd-1 --status in_progress
beads update bd-1 --priority 2
beads update bd-1 --assignee bob
beads close bd-1 --reason "Completed"
beads close bd-1 bd-2 bd-3   # Close multiple
```

### Dependencies

```bash
# Add dependency (bd-2 depends on bd-1)
beads dep add bd-2 bd-1
beads dep add bd-3 bd-1 --type blocks

# Remove dependency
beads dep remove bd-2 bd-1

# Show dependency tree
beads dep tree bd-2

# Detect cycles
beads dep cycles
```

### Finding Work

```bash
# Show ready work (no blockers)
beads ready
beads ready --limit 20
beads ready --priority 1
beads ready --assignee alice

# Show blocked issues
beads blocked

# Statistics
beads stats
```

## Database

By default, Beads stores data in `~/.beads/beads.db` using SQLite.

You can use a different database:

```bash
beads --db ./project.db create "Issue"
```

Or set it via environment:

```bash
export BEADS_DB=/path/to/db
beads create "Issue"
```

## Dependency Model

Beads has three types of dependencies:

1. **blocks** - Hard blocker (affects ready work calculation)
2. **related** - Soft relationship (just for context)
3. **parent-child** - Epic/subtask hierarchy

Only `blocks` dependencies affect the ready work queue.

## Ready Work Algorithm

An issue is "ready" if:
- Status is `open`
- It has NO open `blocks` dependencies
- All blockers are either closed or non-existent

Example:
```
bd-1 [open] ← blocks ← bd-2 [open] ← blocks ← bd-3 [open]
```

Ready work: `[bd-1]`
Blocked: `[bd-2, bd-3]`

## Issue Lifecycle

```
open → in_progress → closed
       ↓
     blocked (manually set, or has open blockers)
```

## Architecture

```
beads/
├── cmd/beads/           # CLI entry point
├── internal/
│   ├── types/           # Core data types
│   ├── storage/         # Storage interface
│   │   └── sqlite/      # SQLite implementation
│   └── ...
└── DESIGN.md            # Full design doc
```

## Comparison to Other Tools

| Feature | Beads | GitHub Issues | Jira | Linear |
|---------|-------|---------------|------|--------|
| Zero setup | ✅ | ❌ | ❌ | ❌ |
| Dependency tracking | ✅ | ⚠️ | ✅ | ✅ |
| Ready work detection | ✅ | ❌ | ❌ | ❌ |
| Offline first | ✅ | ❌ | ❌ | ❌ |
| Git-friendly | ✅ | ❌ | ❌ | ❌ |
| Self-hosted | ✅ | ⚠️ | ⚠️ | ❌ |

## Future Plans

- [ ] PostgreSQL backend for teams
- [ ] Config file support
- [ ] Export/import (JSON, CSV)
- [ ] GitHub/Jira migration tools
- [ ] TUI with bubble tea
- [ ] Web UI (optional)
- [ ] API server mode

## Why Beads?

We built Beads after getting frustrated with heavyweight issue trackers that:
- Required complex setup
- Didn't treat dependencies as first-class citizens
- Couldn't easily show "what's ready to work on"
- Required internet connectivity
- Weren't git-friendly for small teams

Beads is designed for developers who want:
- **Zero setup** - Just run a binary
- **Dependency awareness** - Built-in from day one
- **Offline first** - Local SQLite database
- **Git-friendly** - Check in your database with your code
- **Simple** - No complicated workflows or ceremony

## Documentation

- **[README.md](README.md)** - You are here! Quick reference
- **[QUICKSTART.md](QUICKSTART.md)** - 2-minute tutorial
- **[WORKFLOW.md](WORKFLOW.md)** - Complete workflow guide (vibe coding, database structure, git workflow)
- **[DESIGN.md](DESIGN.md)** - Full technical design document

## Development

```bash
# Run tests
go test ./...

# Build
go build -o beads ./cmd/beads

# Run
./beads create "Test issue"
```

## License

MIT

## Credits

Built with ❤️ by developers who love tracking dependencies and finding ready work.

Inspired by the need for a simpler, dependency-aware issue tracker.
