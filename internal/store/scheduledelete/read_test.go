package scheduledelete

import (
	"testing"
	"time"
)

func TestScheduleLeaseRawTimeSemantics(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 30, 15, 123456789, time.UTC)
	for _, tc := range []struct {
		name, raw      string
		nilValue, want bool
	}{
		{name: "null", nilValue: true}, {name: "empty"}, {name: "whitespace", raw: " "},
		{name: "equal RFC timestamp", raw: now.Format(time.RFC3339Nano)},
		{name: "one nanosecond later", raw: now.Add(time.Nanosecond).Format(time.RFC3339Nano), want: true},
		{name: "one nanosecond earlier", raw: now.Add(-time.Nanosecond).Format(time.RFC3339Nano)},
		{name: "equal SQL timestamp", raw: "2026-09-30 12:30:15.123456789"},
		{name: "equal offset timestamp", raw: "2026-09-30 05:30:15.123456789-07:00"},
		{name: "malformed", raw: "corrupt", want: true}, {name: "zero mysql timestamp", raw: "0000-00-00 00:00:00", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := &tc.raw
			if tc.nilValue {
				value = nil
			}
			if got := scheduleLeaseActive(value, now); got != tc.want {
				t.Fatalf("lease active=%v want %v raw=%q", got, tc.want, tc.raw)
			}
		})
	}
}
