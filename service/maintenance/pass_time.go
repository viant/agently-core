package maintenance

import (
	"context"
	"time"
)

type passTimeKey struct{}

func cleanupPassTime(ctx context.Context, fallback func() time.Time) time.Time {
	if value, ok := ctx.Value(passTimeKey{}).(time.Time); ok {
		return value
	}
	return fallback().UTC()
}
