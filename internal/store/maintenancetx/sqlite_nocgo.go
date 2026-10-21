//go:build !cgo

package maintenancetx

// The mattn driver cannot execute without CGO. The modernc driver is handled
// separately, so cleanup remains buildable and usable without CGO.
func cgoSQLiteContention(error) bool { return false }
