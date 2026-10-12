package toolexec

import (
	"context"
	"testing"
	"time"
)

func TestClientToolDeadlineUsesOriginalTrustedBudget(t *testing.T) {
	t.Setenv("AGENTLY_TOOLCALL_TIMEOUT", "45s")
	start := time.Now().UTC()
	if got := ClientToolDeadline(context.Background(), start); !got.Equal(start.Add(45 * time.Second)) {
		t.Fatal("environment budget changed")
	}
	ctx := WithToolTimeout(context.Background(), 12*time.Second)
	if got := ClientToolDeadline(ctx, start); !got.Equal(start.Add(12 * time.Second)) {
		t.Fatal("configured budget changed")
	}
	bounded, cancel := context.WithDeadline(ctx, start.Add(2*time.Second))
	defer cancel()
	if got := ClientToolDeadline(bounded, start); !got.Equal(start.Add(2 * time.Second)) {
		t.Fatal("parent deadline extended")
	}
	if !ClientToolDeadline(ctx, time.Time{}).IsZero() {
		t.Fatal("unknown start manufactured")
	}
	t.Setenv("AGENTLY_TOOLCALL_TIMEOUT", "")
	if got := ClientToolDeadline(context.Background(), start); !got.Equal(start.Add(3 * time.Minute)) {
		t.Fatal("existing default changed")
	}
}
