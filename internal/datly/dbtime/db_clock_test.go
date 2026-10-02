package dbtime

import (
	"testing"
	"time"
)

func TestParseDatabaseUTC(t *testing.T) {
	for _, input := range []string{"2026-09-30 02:15:45", "2026-09-30 02:15:45.123456", "2026-09-30T02:15:45Z"} {
		got, err := ParseDatabaseUTC(input)
		if err != nil || got.Location() != time.UTC || got.Year() != 2026 || got.Hour() != 2 {
			t.Fatalf("input=%q time=%v err=%v", input, got, err)
		}
	}
	if _, err := ParseDatabaseUTC("not a database clock"); err == nil {
		t.Fatal("malformed database clock was accepted")
	}
}
