package cube

import (
	"context"
	"fmt"
)

func (input *TurnReportInput) Init(context.Context) error {
	if !input.Trusted {
		return fmt.Errorf("turn reports require trusted host scope")
	}
	return nil
}
