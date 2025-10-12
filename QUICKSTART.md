# Beads Quickstart

Get up and running with Beads in 2 minutes.

## Installation

```bash
cd ~/src/beads
go build -o beads ./cmd/beads
./beads --help
```

## Your First Issues

```bash
# Create a few issues
./beads create "Set up database" -p 1 -t task
./beads create "Create API" -p 2 -t feature
./beads create "Add authentication" -p 2 -t feature

# List them
./beads list
```

## Add Dependencies

```bash
# API depends on database
./beads dep add bd-2 bd-1

# Auth depends on API
./beads dep add bd-3 bd-2

# View the tree
./beads dep tree bd-3
```

Output:
```
🌲 Dependency tree for bd-3:

→ bd-3: Add authentication [P2] (open)
  → bd-2: Create API [P2] (open)
    → bd-1: Set up database [P1] (open)
```

## Find Ready Work

```bash
./beads ready
```

Output:
```
📋 Ready work (1 issues with no blockers):

1. [P1] bd-1: Set up database
```

Only bd-1 is ready because bd-2 and bd-3 are blocked!

## Work the Queue

```bash
# Start working on bd-1
./beads update bd-1 --status in_progress

# Complete it
./beads close bd-1 --reason "Database setup complete"

# Check ready work again
./beads ready
```

Now bd-2 is ready! 🎉

## Track Progress

```bash
# See blocked issues
./beads blocked

# View statistics
./beads stats
```

## Database Location

By default: `~/.beads/beads.db`

You can use project-specific databases:

```bash
./beads --db ./my-project.db create "Task"
```

## Next Steps

- Add labels: `./beads create "Task" -l "backend,urgent"`
- Filter ready work: `./beads ready --priority 1`
- Search issues: `./beads list --status open`
- Detect cycles: `./beads dep cycles`

See [README.md](README.md) for full documentation.
