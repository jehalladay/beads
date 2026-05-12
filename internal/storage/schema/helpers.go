package schema

import (
	"context"
	"fmt"
)

func TableExists(ctx context.Context, db DBConn, table string) (bool, error) {
	rows, err := db.QueryContext(ctx, "SHOW TABLES LIKE '"+table+"'") //nolint:gosec // G202: table name is an internal constant
	if err != nil {
		return false, fmt.Errorf("check table %s: %w", table, err)
	}
	defer rows.Close()
	return rows.Next(), nil
}
