package read

import (
	"context"
	"fmt"
)

func (input *ToolCallsInput) Init(context.Context) error {
	switch input.ReadMode {
	case "rows", "transcript":
		return nil
	case "byOp", "scopedByOp":
		if input.Has == nil || !input.Has.OpId || input.OpId == "" {
			return fmt.Errorf("by-op lookup requires opId")
		}
		input.SetFields([]string{"message_id", "turn_id", "op_id", "trace_id", "response_payload_id"})
	case "byTurn":
		input.SetFields([]string{"message_id", "turn_id", "op_id", "attempt"})
	default:
		return fmt.Errorf("unsupported tool call read mode")
	}
	return nil
}
