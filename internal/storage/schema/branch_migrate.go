package schema

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

func MigrateOnNewBranchAndMerge(ctx context.Context, conn *sql.Conn, defaultBranch string) (int, error) {
	if _, err := conn.ExecContext(ctx, "CALL DOLT_CHECKOUT(?)", defaultBranch); err != nil {
		return 0, fmt.Errorf("checkout %q: %w", defaultBranch, err)
	}
	if _, err := conn.ExecContext(ctx, schemaMigrationsBootstrapSQL); err != nil {
		return 0, fmt.Errorf("creating schema_migrations table: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_ADD('-A')"); err != nil {
		return 0, fmt.Errorf("stage schema_migrations on %q: %w", defaultBranch, err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-m', 'schema: bootstrap schema_migrations')"); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "nothing to commit") {
			return 0, fmt.Errorf("commit schema_migrations on %q: %w", defaultBranch, err)
		}
	}

	var current int
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("reading current migration version: %w", err)
	}
	if current >= LatestVersion() {
		return 0, nil
	}

	generated := fmt.Sprintf("bd-schema-init-%d", time.Now().UnixNano())
	if _, err := conn.ExecContext(ctx, "CALL DOLT_BRANCH(?, ?)", generated, defaultBranch); err != nil {
		return 0, fmt.Errorf("create branch %q from %q: %w", generated, defaultBranch, err)
	}

	defer func() {
		if _, err := conn.ExecContext(ctx, "CALL DOLT_CHECKOUT(?)", defaultBranch); err != nil {
			log.Printf("schema: cleanup checkout %q: %v", defaultBranch, err)
		}
		if _, err := conn.ExecContext(ctx, "CALL DOLT_BRANCH('-D', ?)", generated); err != nil {
			log.Printf("schema: cleanup delete %q: %v", generated, err)
		}
	}()

	if _, err := conn.ExecContext(ctx, "CALL DOLT_CHECKOUT(?)", generated); err != nil {
		return 0, fmt.Errorf("checkout %q: %w", generated, err)
	}

	applied, err := MigrateUp(ctx, conn)
	if err != nil {
		return 0, fmt.Errorf("migrate: %w", err)
	}

	if applied > 0 {
		if _, err := conn.ExecContext(ctx, "CALL DOLT_ADD('-A')"); err != nil {
			return 0, fmt.Errorf("stage: %w", err)
		}
		if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-m', 'schema: apply migrations')"); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "nothing to commit") {
				return 0, fmt.Errorf("commit: %w", err)
			}
		}
	}

	if _, err := conn.ExecContext(ctx, "CALL DOLT_CHECKOUT(?)", defaultBranch); err != nil {
		return 0, fmt.Errorf("checkout %q (post-migrate): %w", defaultBranch, err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_MERGE(?)", generated); err != nil {
		return 0, fmt.Errorf("merge %q into %q: %w", generated, defaultBranch, err)
	}

	return applied, nil
}

func MigrateOnDefaultBranch(ctx context.Context, conn *sql.Conn, defaultBranch string) (int, error) {
	if _, err := conn.ExecContext(ctx, "CALL DOLT_CHECKOUT(?)", defaultBranch); err != nil {
		return 0, fmt.Errorf("checkout %q: %w", defaultBranch, err)
	}
	if _, err := conn.ExecContext(ctx, schemaMigrationsBootstrapSQL); err != nil {
		return 0, fmt.Errorf("creating schema_migrations table: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_ADD('-A')"); err != nil {
		return 0, fmt.Errorf("stage schema_migrations on %q: %w", defaultBranch, err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-m', 'schema: bootstrap schema_migrations')"); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "nothing to commit") {
			return 0, fmt.Errorf("commit schema_migrations on %q: %w", defaultBranch, err)
		}
	}

	var current int
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("reading current migration version: %w", err)
	}
	if current >= LatestVersion() {
		return 0, nil
	}

	applied, err := MigrateUp(ctx, conn)
	if err != nil {
		return 0, fmt.Errorf("migrate: %w", err)
	}

	if applied > 0 {
		if _, err := conn.ExecContext(ctx, "CALL DOLT_ADD('-A')"); err != nil {
			return 0, fmt.Errorf("stage: %w", err)
		}
		if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-m', 'schema: apply migrations')"); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "nothing to commit") {
				return 0, fmt.Errorf("commit: %w", err)
			}
		}
	}

	return applied, nil
}
