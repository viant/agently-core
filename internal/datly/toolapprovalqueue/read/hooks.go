package read

import (
	"context"
	"fmt"
)

func (input *ApprovalRowsInput) Init(context.Context) error {
	switch input.ReadMode {
	case "rows", "outcome":
		return nil
	}
	return fmt.Errorf("unsupported approval read mode")
}
