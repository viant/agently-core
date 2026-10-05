package sdk

import (
	"context"
	"fmt"
	"strings"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
)

// A deferred interrupt event can be observed just before its original native
// Query finishes recording the wait boundary. The receipt must not start a
// second execution while that first call is still returning.
func (c *backendClient) aguiAwaitDeferredBoundary(ctx context.Context, record *aguistore.Run) error {
	if c == nil || c.data == nil || record == nil {
		return fmt.Errorf("native continuation boundary inspection unavailable")
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		native, err := c.data.GetRun(ctx, record.TurnID, nil)
		if err != nil {
			return err
		}
		if native == nil || native.ConversationId == nil || *native.ConversationId != record.ConversationID {
			return fmt.Errorf("native continuation identity mismatch")
		}
		if native.EffectiveUserId != nil && *native.EffectiveUserId != "" && *native.EffectiveUserId != record.Principal {
			return fmt.Errorf("native continuation owner mismatch")
		}
		switch strings.ToLower(native.Status) {
		case "completed", "failed", "error", "canceled", "cancelled":
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
