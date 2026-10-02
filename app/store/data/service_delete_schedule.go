package data

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/sqlitewrite"
	"github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/agently-core/internal/store/scheduledelete"
)

var ErrScheduleNotFound = errors.New("schedule not found")

func (s *datlyService) DeleteScheduleCascade(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	_, err := sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) {
		store := &scheduledelete.Store{Invoker: s.native, OwnerID: auth.EffectiveUserID}
		return struct{}{}, mapScheduleDeleteError(store.DeleteSchedule(ctx, id))
	})
	return err
}

func mapScheduleDeleteError(err error) error {
	switch {
	case errors.Is(err, scheduledelete.ErrScheduleNotFound):
		return fmt.Errorf("%w%s", ErrScheduleNotFound, strings.TrimPrefix(err.Error(), scheduledelete.ErrScheduleNotFound.Error()))
	case errors.Is(err, scheduledelete.ErrScheduledRunNotFound):
		return fmt.Errorf("%w%s", ErrScheduledRunNotFound, strings.TrimPrefix(err.Error(), scheduledelete.ErrScheduledRunNotFound.Error()))
	case errors.Is(err, conversationtree.ErrPermissionDenied):
		return ErrPermissionDenied
	case errors.Is(err, conversationtree.ErrNotFound):
		return ErrConversationNotFound
	case errors.Is(err, conversationtree.ErrConversationActive):
		return ErrConversationActive
	case errors.Is(err, conversationtree.ErrNonTerminal):
		return ErrConversationNonTerminal
	case errors.Is(err, conversationtree.ErrTooLarge):
		return ErrConversationGraphTooLarge
	case errors.Is(err, conversationtree.ErrGraphReferenced):
		return ErrConversationGraphReferenced
	case errors.Is(err, conversationtree.ErrScheduleReferenced):
		return ErrConversationScheduleReferenced
	}
	return err
}
