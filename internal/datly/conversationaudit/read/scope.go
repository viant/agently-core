package read

import (
	"context"
	"fmt"
	"time"
)

func (i *Input) Init(context.Context) error {
	if !i.Allowed {
		return fmt.Errorf("trusted audit scope required")
	}
	if i.Limit < 1 || i.Limit > 201 {
		return fmt.Errorf("invalid audit limit")
	}
	if len(i.TurnIDs) > 200 || (i.Detail && i.TurnID == "" && len(i.TurnIDs) == 0) {
		return fmt.Errorf("bounded audit detail selection required")
	}
	for _, id := range i.TurnIDs {
		if id == "" || len(id) > 256 {
			return fmt.Errorf("invalid audit turn ID")
		}
	}
	if i.ConversationID == "" {
		from, fromErr := time.Parse(DatabaseTimeFormat, i.From)
		to, toErr := time.Parse(DatabaseTimeFormat, i.To)
		if fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > 31*24*time.Hour {
			return fmt.Errorf("bounded conversation selection required")
		}
	}
	return nil
}

// Use UTC SQL timestamps without driver-specific timezone suffixes. This also
// compares correctly with SQLite's CURRENT_TIMESTAMP values at exact bounds.
const DatabaseTimeFormat = "2006-01-02 15:04:05.999999999"
