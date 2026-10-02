package dbtime

import (
	"fmt"
	"strings"
	"time"
)

// ParseDatabaseUTC decodes the server clock projected by the token reader.
// MySQL UTC_TIMESTAMP and SQLite DATETIME('now') are both UTC without an offset.
func ParseDatabaseUTC(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if result, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return result.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported database UTC time %q", value)
}
