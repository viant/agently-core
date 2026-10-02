package data

import (
	"context"
	"fmt"
	"strings"

	"github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/sqlitewrite"
	"github.com/viant/agently-core/internal/store/scheduledelete"
)

// DeleteScheduledRun deletes one persisted scheduler run and the complete
// conversation graph created by it. The schedule definition is retained.
func (s *datlyService) DeleteScheduledRun(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: empty id", ErrScheduledRunNotFound)
	}
	_, err := sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) {
		store := &scheduledelete.Store{Invoker: s.native, OwnerID: auth.EffectiveUserID}
		return struct{}{}, mapScheduleDeleteError(store.DeleteRun(ctx, id))
	})
	return err
}
