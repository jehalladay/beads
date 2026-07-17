package uow

import (
	"errors"
	"fmt"
	"testing"

	mysql "github.com/go-sql-driver/mysql"
)

func TestIsSerializationError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "nil", err: nil, expected: false},
		// The two retry-safe serialization failures: the tx is guaranteed
		// rolled back, so replaying is safe.
		{name: "deadlock 1213", err: &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}, expected: true},
		{name: "lock wait timeout 1205", err: &mysql.MySQLError{Number: 1205, Message: "Lock wait timeout exceeded"}, expected: true},
		// Real driver errors arrive wrapped; errors.As must see through it.
		{name: "wrapped deadlock", err: fmt.Errorf("uow: commit: %w", &mysql.MySQLError{Number: 1213}), expected: true},
		// Other MySQL error numbers are not serialization failures.
		{name: "syntax error 1064", err: &mysql.MySQLError{Number: 1064, Message: "syntax error"}, expected: false},
		{name: "table not found 1146", err: &mysql.MySQLError{Number: 1146, Message: "Table not found"}, expected: false},
		// Non-MySQL errors never match, even if the text mentions a deadlock.
		{name: "plain error mentioning deadlock", err: errors.New("Error 1213: Deadlock found"), expected: false},
		{name: "connection refused - not serialization", err: errors.New("connection refused"), expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSerializationError(tt.err); got != tt.expected {
				t.Errorf("isSerializationError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}
