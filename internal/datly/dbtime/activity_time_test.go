package dbtime

import (
	"testing"
	"time"
)

func TestMaintenanceActivityDatabaseText(t *testing.T) {
	want := time.Date(2026, 9, 30, 12, 0, 0, 123456000, time.UTC)
	for _, raw := range []string{"2026-09-30T12:00:00.123456Z", "2026-09-30 12:00:00.123456", "2026-09-30 05:00:00.123456-07:00", " 2026-09-30 12:00:00.123456 +0000 UTC "} {
		got, ok := ParseActivity(raw)
		if !ok || !got.Equal(want) || got.Location() != time.UTC {
			t.Fatalf("activity %q=%s known=%v", raw, got, ok)
		}
	}
	for _, raw := range []string{"", "bad", "0000-00-00 00:00:00", "2026-99-99 00:00:00"} {
		if got, ok := ParseActivity(raw); ok || !got.IsZero() {
			t.Fatalf("invalid activity %q=%s known=%v", raw, got, ok)
		}
	}
}
