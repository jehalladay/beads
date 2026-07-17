package dberrors

import (
	"errors"
	"regexp"
	"strings"

	mysql "github.com/go-sql-driver/mysql"
)

var (
	quotedTableMissingPattern   = regexp.MustCompile(`(?i)\btable\s+'[^']+'\s+(doesn't exist|does not exist)\b`)
	unquotedTableMissingPattern = regexp.MustCompile("(?i)^table\\s+`?[^\\s'`]+`?\\s+(doesn't exist|does not exist)\\b")
	// error1146Pattern matches the stringified driver code on a digit boundary
	// so "error 11460"/"error 11461" (unrelated numbers that share the "1146"
	// prefix) are not misclassified as a missing table. A trailing non-digit
	// (or end of string) after 1146 is required.
	error1146Pattern = regexp.MustCompile(`(?i)\berror 1146(\D|$)`)
)

// IsTableNotExist reports whether err is specifically a MySQL/Dolt
// table-not-found error. It intentionally does not classify missing columns,
// schemas, or other objects as optional-table absence.
func IsTableNotExist(err error) bool {
	if err == nil {
		return false
	}

	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1146
	}

	s := strings.ToLower(err.Error())
	return error1146Pattern.MatchString(s) ||
		quotedTableMissingPattern.MatchString(s) ||
		unquotedTableMissingPattern.MatchString(s)
}
