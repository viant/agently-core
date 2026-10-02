package read

import (
	"context"
	"fmt"
)

func (input *TurnRowsInput) Init(context.Context) error {
	switch input.ReadMode {
	case "queued":
		// Queue discovery needs only identity and sequence, as in the legacy API.
		input.SetFields([]string{"id", "queue_seq"})
		return nil
	case "rows", "byId", "active", "nextQueued":
		return nil
	default:
		return fmt.Errorf("unsupported turn read mode")
	}
}
