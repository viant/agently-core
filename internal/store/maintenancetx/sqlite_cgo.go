//go:build cgo

package maintenancetx

import "github.com/mattn/go-sqlite3"

func cgoSQLiteContention(err error) bool {
	switch failure := err.(type) {
	case sqlite3.Error:
		return failure.Code == sqlite3.ErrBusy || failure.Code == sqlite3.ErrLocked
	case *sqlite3.Error:
		return failure != nil && (failure.Code == sqlite3.ErrBusy || failure.Code == sqlite3.ErrLocked)
	}
	return false
}
