package count

import (
	"context"
	"fmt"
	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
	"time"
)

func (i *Input) Init(context.Context) error {
	if !i.Allowed {
		return fmt.Errorf("trusted audit scope required")
	}
	if i.ConversationID == "" {
		from, fromErr := time.Parse(projection.DatabaseTimeFormat, i.From)
		to, toErr := time.Parse(projection.DatabaseTimeFormat, i.To)
		if fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > 31*24*time.Hour {
			return fmt.Errorf("bounded conversation selection required")
		}
	}
	return nil
}
