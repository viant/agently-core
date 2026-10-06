package sdk

import (
	"context"
	"fmt"
	"strconv"

	queueread "github.com/viant/agently-core/internal/datly/turnqueue/read"
	queuestore "github.com/viant/agently-core/internal/store/turnqueue"
)

// Current queue storage supplies the additive lossless position. Historical
// admission activities and the legacy numeric queueSeq are not rewritten.
func (c *backendClient) populateCanonicalQueueSequences(ctx context.Context, state *ConversationState) error {
	if c == nil || c.goalInvoker == nil || state == nil {
		return nil
	}
	hasQueued := false
	for _, turn := range state.Turns {
		if turn != nil && turn.Status == TurnStatusQueued {
			hasQueued = true
			break
		}
	}
	if !hasQueued {
		return nil
	}
	input := &queueread.QueueRowsInput{}
	input.SetConversationId(state.ConversationID)
	input.SetQueueStatus("queued")
	input.SetNativeQueuedOnly(true)
	rows, err := (&queuestore.Store{Invoker: c.goalInvoker}).List(ctx, input)
	if err != nil {
		return err
	}
	positions, ambiguous := map[string]string{}, map[string]bool{}
	for _, row := range rows {
		if row == nil || row.ConversationId != state.ConversationID || row.Status != "queued" || row.TurnId == "" {
			return fmt.Errorf("current queue position scope is inconsistent")
		}
		if _, exists := positions[row.TurnId]; exists {
			ambiguous[row.TurnId] = true
		}
		positions[row.TurnId] = strconv.FormatInt(row.QueueSeq, 10)
	}
	for _, turn := range state.Turns {
		if turn != nil && turn.Status == TurnStatusQueued && !ambiguous[turn.TurnID] {
			turn.QueueSequence = positions[turn.TurnID]
		}
	}
	return nil
}
