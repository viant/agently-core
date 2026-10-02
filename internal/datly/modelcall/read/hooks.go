package read

import (
	"context"
	"fmt"
)

func (input *ModelCallsInput) Init(context.Context) error {
	if input.ReadMode != "rows" && input.ReadMode != "transcript" {
		return fmt.Errorf("unsupported model call read mode")
	}
	return nil
}
