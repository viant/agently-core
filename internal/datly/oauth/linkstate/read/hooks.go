package read

import (
	"context"
	"fmt"
	"strings"
)

func (input *LinkStateInput) Init(context.Context) error {
	input.FlowHash = strings.TrimSpace(input.FlowHash)
	input.StateHash = strings.TrimSpace(input.StateHash)
	if input.Mode == "expired" {
		if strings.TrimSpace(input.Before) == "" {
			return fmt.Errorf("cleanup horizon is required")
		}
		return nil
	}
	if input.Mode != "" && input.Mode != "lookup" {
		return fmt.Errorf("unsupported link state read mode")
	}
	if (input.FlowHash == "") == (input.StateHash == "") {
		return fmt.Errorf("exactly one state or flow hash is required")
	}
	return nil
}
