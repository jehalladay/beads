# Schema migrations

This directory holds the beads schema migrations. Each version has a paired
`NNNN_name.up.sql` and `NNNN_name.down.sql`, but **only the `.up.sql` files are
live**. The pairing is a naming convention, not evidence of a rollback path.

## There is no rollback path

`schema.go` embeds up migrations only:

```go
//go:embed migrations/*.up.sql
//go:embed migrations/ignored/*.up.sql
```

The `.down.sql` files are **not embedded and there is no `MigrateDown`
consumer** anywhere in the source — nothing compiles them into the binary or
executes them. `bd` migrates strictly forward (version-gated, cursor-recorded
in `schema_migrations`). To undo a migration, **restore from a prior `dolt`
commit**; there is no in-tool downgrade.

The `.down.sql` files are kept for documentation and as a record of the
inverse intent of each migration. Do not assume they run.

## Notes on specific down files

- **`0035_migrate_infra_to_wisps.down.sql`** is a maintained test fixture:
  `schema_test.go` (`TestMigration0035HandlesLegacyWispDependenciesShape`)
  reads it from disk and asserts its content. Keep it in sync with the up
  migration.
- **`0041` and `0042` down files are intentional no-ops** — those migrations
  are irreversible by design (see `CHANGELOG.md`, PR #3952). The down files
  document *why* rather than providing a reversal.
- The remaining down files are best-effort inverse SQL that has never been
  executed by the tool; treat them as documentation, not tested code.

## Adding a migration

Add `NNNN_name.up.sql` (next version number). A matching `.down.sql` is
optional and, if written, is documentation only — it will not run. If a
migration is irreversible, say so in the `.down.sql` as a comment (see
`0041`/`0042` for the pattern).
