package db

import (
	"strings"
)

// IsUndefinedColumn reports PostgreSQL undefined_column errors (SQLSTATE 42703)
// surfaced through database/sql without importing driver-specific packages.
func IsUndefinedColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "42703") ||
		strings.Contains(msg, "undefined_column") ||
		strings.Contains(msg, "does not exist") && strings.Contains(msg, "column")
}
