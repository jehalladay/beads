# bd - Beads Issue Tracker 🔗

**Issues chained together like beads.**

A lightweight, dependency-aware issue tracker designed for AI-supervised coding workflows. Track dependencies, find ready work, and let agents chain together tasks automatically.

## Features

- ✨ **Zero setup** - `bd init` creates project-local database
- 🔗 **Dependency tracking** - Four dependency types (blocks, related, parent-child, discovered-from)
- 📋 **Ready work detection** - Automatically finds issues with no open blockers
- 🤖 **Agent-friendly** - `--json` flags for programmatic integration
- 🏗️ **Extensible** - Add your own tables to the SQLite database
- 🔍 **Project-aware** - Auto-discovers database in `.beads/` directory
- 🌲 **Dependency trees** - Visualize full dependency graphs
- 🎨 **Beautiful CLI** - Colored output for humans, JSON for bots
- 💾 **Full audit trail** - Every change is logged

## Installation

```bash
go install github.com/steveyegge/beads/cmd/bd@latest
```

Or build from source:

```bash
git clone https://github.com/steveyegge/beads
cd beads
go build -o bd ./cmd/bd
```

## Quick Start

```bash
# Initialize bd in your project
bd init

# Or with custom prefix
bd init --prefix myapp

# See the quickstart guide
bd quickstart

# Create your first issue (will be myapp-1)
bd create "Build login page" -d "Need user authentication" -p 1 -t feature

# Create another issue that depends on it
bd create "Add OAuth support" -p 2
bd dep add myapp-2 myapp-1  # myapp-2 depends on myapp-1

# See what's ready to work on
bd ready

# Show dependency tree
bd dep tree myapp-2
```

## Usage

### Creating Issues

```bash
bd create "Fix bug" -d "Description" -p 1 -t bug
bd create "Add feature" --description "Long description" --priority 2 --type feature
bd create "Task" -l "backend,urgent" --assignee alice

# Get JSON output for programmatic use
bd create "Fix bug" -d "Description" --json
```

Options:
- `-d, --description` - Issue description
- `-p, --priority` - Priority (0-4, 0=highest)
- `-t, --type` - Type (bug|feature|task|epic|chore)
- `-a, --assignee` - Assign to user
- `-l, --labels` - Comma-separated labels
- `--json` - Output in JSON format

### Viewing Issues

```bash
bd show bd-1              # Show full details
bd list                   # List all issues
bd list --status open     # Filter by status
bd list --priority 1      # Filter by priority
bd list --assignee alice  # Filter by assignee

# JSON output for agents
bd list --json
bd show bd-1 --json
```

### Updating Issues

```bash
bd update bd-1 --status in_progress
bd update bd-1 --priority 2
bd update bd-1 --assignee bob
bd close bd-1 --reason "Completed"
bd close bd-1 bd-2 bd-3   # Close multiple

# JSON output
bd update bd-1 --status in_progress --json
bd close bd-1 --json
```

### Dependencies

```bash
# Add dependency (bd-2 depends on bd-1)
bd dep add bd-2 bd-1
bd dep add bd-3 bd-1 --type blocks

# Remove dependency
bd dep remove bd-2 bd-1

# Show dependency tree
bd dep tree bd-2

# Detect cycles
bd dep cycles
```

### Finding Work

```bash
# Show ready work (no blockers)
bd ready
bd ready --limit 20
bd ready --priority 1
bd ready --assignee alice

# Show blocked issues
bd blocked

# Statistics
bd stats

# JSON output for agents
bd ready --json
```

## Database Discovery

bd automatically discovers your database in this order:

1. `--db` flag: `bd --db /path/to/db.db create "Issue"`
2. `$BEADS_DB` environment variable: `export BEADS_DB=/path/to/db.db`
3. `.beads/*.db` in current directory or ancestors (walks up like git)
4. `~/.beads/default.db` as fallback

This means you can:
- Initialize per-project databases with `bd init`
- Work from any subdirectory (bd finds the database automatically)
- Override for testing or multiple projects

Example:

```bash
# Initialize in project root
cd ~/myproject
bd init --prefix myapp

# Work from any subdirectory
cd ~/myproject/src/components
bd create "Fix navbar bug"  # Uses ~/myproject/.beads/myapp.db

# Override for a different project
bd --db ~/otherproject/.beads/other.db list
```

## Dependency Model

Beads has four types of dependencies:

1. **blocks** - Hard blocker (affects ready work calculation)
2. **related** - Soft relationship (just for context)
3. **parent-child** - Epic/subtask hierarchy
4. **discovered-from** - Tracks issues discovered while working on another issue

Only `blocks` dependencies affect the ready work queue.

### Dependency Type Usage

- **blocks**: Use when issue X cannot start until issue Y is completed
  ```bash
  bd dep add bd-5 bd-3 --type blocks  # bd-5 blocked by bd-3
  ```

- **related**: Use for issues that are connected but don't block each other
  ```bash
  bd dep add bd-10 bd-8 --type related  # bd-10 related to bd-8
  ```

- **parent-child**: Use for epic/subtask hierarchies
  ```bash
  bd dep add bd-15 bd-12 --type parent-child  # bd-15 is child of epic bd-12
  ```

- **discovered-from**: Use when you discover new work while working on an issue
  ```bash
  # While working on bd-20, you discover a bug
  bd create "Fix edge case bug" -t bug -p 1
  bd dep add bd-21 bd-20 --type discovered-from  # bd-21 discovered from bd-20
  ```

The `discovered-from` type is particularly useful for AI-supervised workflows, where the AI can automatically create issues for discovered work and link them back to the parent task.

## AI Agent Integration

bd is designed to work seamlessly with AI coding agents:

```bash
# Agent discovers ready work
WORK=$(bd ready --limit 1 --json)
ISSUE_ID=$(echo $WORK | jq -r '.[0].id')

# Agent claims and starts work
bd update $ISSUE_ID --status in_progress --json

# Agent discovers new work while executing
bd create "Fix bug found in testing" -t bug -p 0 --json > new_issue.json
NEW_ID=$(cat new_issue.json | jq -r '.id')
bd dep add $NEW_ID $ISSUE_ID --type discovered-from

# Agent completes work
bd close $ISSUE_ID --reason "Implemented and tested" --json
```

The `--json` flag on every command makes bd perfect for programmatic workflows.

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
├── cmd/bd/              # CLI entry point
│   ├── main.go          # Core commands (create, list, show, update, close)
│   ├── init.go          # Project initialization
│   ├── quickstart.go    # Interactive guide
│   └── ...
├── internal/
│   ├── types/           # Core data types (Issue, Dependency, etc.)
│   └── storage/         # Storage interface
│       └── sqlite/      # SQLite implementation
└── EXTENDING.md         # Database extension guide
```

## Extending bd

Applications can extend bd's SQLite database with their own tables. See [EXTENDING.md](EXTENDING.md) for the full guide.

Quick example:

```sql
-- Add your own tables to .beads/myapp.db
CREATE TABLE myapp_executions (
    id INTEGER PRIMARY KEY,
    issue_id TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at DATETIME,
    FOREIGN KEY (issue_id) REFERENCES issues(id)
);

-- Query across layers
SELECT i.*, e.status as execution_status
FROM issues i
LEFT JOIN myapp_executions e ON i.id = e.issue_id
WHERE i.status = 'in_progress';
```

This pattern enables powerful integrations while keeping bd simple and focused.

## Comparison to Other Tools

| Feature | bd | GitHub Issues | Jira | Linear |
|---------|-------|---------------|------|--------|
| Zero setup | ✅ | ❌ | ❌ | ❌ |
| Dependency tracking | ✅ | ⚠️ | ✅ | ✅ |
| Ready work detection | ✅ | ❌ | ❌ | ❌ |
| Agent-friendly (JSON) | ✅ | ⚠️ | ⚠️ | ⚠️ |
| Git-native storage | ✅ (JSONL) | ❌ | ❌ | ❌ |
| AI-resolvable conflicts | ✅ | ❌ | ❌ | ❌ |
| Extensible database | ✅ | ❌ | ❌ | ❌ |
| Offline first | ✅ | ❌ | ❌ | ❌ |
| Self-hosted | ✅ | ⚠️ | ⚠️ | ❌ |

## Why bd?

bd is built for AI-supervised coding workflows where:
- **Agents need to discover work** - `bd ready --json` gives agents unblocked tasks
- **Dependencies matter** - Agents shouldn't duplicate effort or work on blocked tasks
- **Discovery happens during execution** - Use `discovered-from` to track new work found during implementation
- **Git-native storage** - JSONL format enables AI-powered conflict resolution
- **Integration is easy** - Extend the SQLite database with your own orchestration tables
- **Setup is instant** - `bd init` and you're tracking issues

Traditional issue trackers were built for human project managers. bd is built for agent colonies.

## Architecture: JSONL + SQLite

bd uses a dual-storage approach:

- **JSONL files** (`.beads/issues.jsonl`) - Source of truth, committed to git
- **SQLite database** (`.beads/*.db`) - Ephemeral cache for fast queries, gitignored

This gives you:
- ✅ **Git-friendly storage** - Text diffs, AI-resolvable conflicts
- ✅ **Fast queries** - SQLite indexes for dependency graphs
- ✅ **Simple workflow** - Export before commit, import after pull
- ✅ **No daemon required** - In-process SQLite, ~10-100ms per command

When you run `bd create`, it writes to SQLite. Before committing to git, run `bd export` to sync to JSONL. After pulling, run `bd import` to sync back to SQLite. Git hooks can automate this.

## Export/Import (JSONL Format)

bd can export and import issues as JSON Lines (one JSON object per line). This is perfect for git workflows and data portability.

### Export Issues

```bash
# Export all issues to stdout
bd export --format=jsonl

# Export to file
bd export --format=jsonl -o issues.jsonl

# Export filtered issues
bd export --format=jsonl --status=open -o open-issues.jsonl
```

Issues are exported sorted by ID for consistent git diffs.

### Import Issues

```bash
# Import from stdin
cat issues.jsonl | bd import

# Import from file
bd import -i issues.jsonl

# Skip existing issues (only create new ones)
bd import -i issues.jsonl --skip-existing
```

Import behavior:
- Existing issues (same ID) are **updated** with new values
- New issues are **created**
- All imports are atomic (all or nothing)

### JSONL Format

Each line is a complete JSON issue object:

```jsonl
{"id":"bd-1","title":"Fix login bug","status":"open","priority":1,"issue_type":"bug","created_at":"2025-10-12T10:00:00Z","updated_at":"2025-10-12T10:00:00Z"}
{"id":"bd-2","title":"Add dark mode","status":"in_progress","priority":2,"issue_type":"feature","created_at":"2025-10-12T11:00:00Z","updated_at":"2025-10-12T12:00:00Z"}
```

## Git Workflow

**Recommended approach**: Use JSONL export as source of truth, SQLite database as ephemeral cache (not committed to git).

### Setup

Add to `.gitignore`:
```
.beads/*.db
.beads/*.db-*
```

Add to git:
```
.beads/issues.jsonl
```

### Workflow

```bash
# Export before committing
bd export -o .beads/issues.jsonl
git add .beads/issues.jsonl
git commit -m "Update issues"
git push

# Import after pulling
git pull
bd import -i .beads/issues.jsonl
```

### Automated with Git Hooks

Create `.git/hooks/pre-commit`:
```bash
#!/bin/bash
bd export -o .beads/issues.jsonl
git add .beads/issues.jsonl
```

Create `.git/hooks/post-merge`:
```bash
#!/bin/bash
bd import -i .beads/issues.jsonl
```

Make hooks executable:
```bash
chmod +x .git/hooks/pre-commit .git/hooks/post-merge
```

### Why JSONL?

- ✅ **Git-friendly**: One line per issue = clean diffs
- ✅ **Mergeable**: Concurrent appends rarely conflict
- ✅ **Human-readable**: Easy to review changes
- ✅ **Scriptable**: Use `jq`, `grep`, or any text tools
- ✅ **Portable**: Export/import between databases

### Handling Conflicts

When two developers create new issues:
```diff
 {"id":"bd-1","title":"First issue",...}
 {"id":"bd-2","title":"Second issue",...}
+{"id":"bd-3","title":"From branch A",...}
+{"id":"bd-4","title":"From branch B",...}
```

Git may show a conflict, but resolution is simple: **keep both lines** (both changes are compatible).

See **[TEXT_FORMATS.md](TEXT_FORMATS.md)** for detailed analysis of JSONL merge strategies and conflict resolution.

## Documentation

- **[README.md](README.md)** - You are here! Complete guide
- **[TEXT_FORMATS.md](TEXT_FORMATS.md)** - JSONL format analysis and merge strategies
- **[GIT_WORKFLOW.md](GIT_WORKFLOW.md)** - Historical analysis of binary vs text approaches
- **[EXTENDING.md](EXTENDING.md)** - Database extension patterns
- Run `bd quickstart` for interactive tutorial

## Development

```bash
# Run tests
go test ./...

# Build
go build -o bd ./cmd/bd

# Run
./bd create "Test issue"
```

## License

MIT

## Credits

Built with ❤️ by developers who love tracking dependencies and finding ready work.

Inspired by the need for a simpler, dependency-aware issue tracker.
