package database

import (
	"database/sql"
	"reflect"
	"strings"
)

// IsSQLite reports whether db was opened with the SQLite driver.
func IsSQLite(db *sql.DB) bool {
	driverType := reflect.TypeOf(db.Driver())
	if driverType == nil {
		return false
	}
	return strings.Contains(driverType.String(), "sqlite")
}
