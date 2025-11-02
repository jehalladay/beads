# Next Session: Agent-Supervised Migration Safety

## Context
We identified that database migrations can lose user data through edge cases (e.g., GH #201 where `bd migrate` failed to set `issue_prefix`, breaking commands). Since beads is designed for AI agents, we should leverage **agent supervision** to make migrations safer.

## Key Architectural Decision
**Beads provides observability primitives; agents supervise using their own reasoning.**

Beads does NOT:
- ❌ Make AI API calls
- ❌ Invoke external models
- ❌ Call agents

Beads DOES:
- ✅ Provide deterministic invariant checks
- ✅ Expose migration state via `--dry-run --json`
- ✅ Roll back on validation failures
- ✅ Give agents structured data to analyze

## The Work (bd-627d)

### Phase 1: Migration Invariants (Start here!)
Create `internal/storage/sqlite/migration_invariants.go` with:

```go
type MigrationInvariant struct {
    Name        string
    Description string
    Check       func(*sql.DB, *Snapshot) error
}

type Snapshot struct {
    IssueCount      int
    ConfigKeys      []string
    DependencyCount int
    LabelCount      int
}
```

Implement these invariants:
1. **required_config_present** - Would have caught GH #201!
2. **foreign_keys_valid** - Detect orphaned dependencies
3. **issue_count_stable** - Catch unexpected data loss

### Phase 2: Inspection Tools
Add CLI commands for agents to inspect migrations:

1. `bd migrate --dry-run --json` - Shows what will change
2. `bd info --schema --json` - Current schema + detected prefix
3. Update `RunMigrations()` to check invariants and rollback on failure

### Phase 3 & 4: MCP Tools + Agent Workflows
Add MCP tools so agents can:
- Inspect migration plans before running
- Detect missing config (like `issue_prefix`)
- Auto-fix issues before migration
- Validate post-migration state

## Starting Prompt for Next Session

```
Let's implement Phase 1 of bd-627d (agent-supervised migration safety).

We need to create migration invariants that check for common data loss scenarios:
1. Missing required config keys (would have caught GH #201)
2. Foreign key integrity (no orphaned dependencies)
3. Issue count stability (detect unexpected deletions)

Start by creating internal/storage/sqlite/migration_invariants.go with the Snapshot type and invariant infrastructure. Then integrate it into RunMigrations() in migrations.go.

The goal: migrations should automatically roll back if invariants fail, preventing data loss.
```

## Related Issues
- bd-627d: Main epic for agent-supervised migrations
- GH #201: Real-world example of migration data loss (missing issue_prefix)
- bd-d355a07d: False positive data loss warnings
- bd-b245: Migration registry (just completed - makes migrations introspectable!)

## Success Criteria
After Phase 1, migrations should:
- ✅ Check invariants before committing
- ✅ Roll back on any invariant failure
- ✅ Provide clear error messages
- ✅ Have unit tests for each invariant

This prevents silent data loss like GH #201 where users discovered breakage only after migration completed.
