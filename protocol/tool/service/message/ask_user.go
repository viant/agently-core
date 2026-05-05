package message

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/protocol/agent/execution"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/mcp-protocol/schema"
)

// Elicitor abstracts the elicitation service to avoid a direct import cycle.
type Elicitor interface {
	Elicit(ctx context.Context, turn *runtimerequestctx.TurnMeta, role string, req *execution.Elicitation) (messageID string, status string, payload map[string]interface{}, err error)
}

// AskUserInput is the tool input for message-askUser.
type AskUserInput struct {
	Message string                 `json:"message" description:"The question or prompt to display to the user."`
	Schema  map[string]interface{} `json:"schema,omitempty" description:"Optional JSON Schema for the expected user response."`
}

// AskUserOutput is the tool result for message-askUser.
type AskUserOutput struct {
	Action  string                 `json:"action"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

func (s *Service) askUser(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*AskUserInput)
	if !ok {
		return fmt.Errorf("invalid input")
	}
	output, ok := out.(*AskUserOutput)
	if !ok {
		return fmt.Errorf("invalid output")
	}
	if s.elicitor == nil {
		return fmt.Errorf("elicitation not configured")
	}
	if strings.TrimSpace(input.Message) == "" {
		return fmt.Errorf("message is required")
	}

	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok {
		return fmt.Errorf("turn context not available")
	}

	req := &execution.Elicitation{}
	req.Message = strings.TrimSpace(input.Message)

	if len(input.Schema) > 0 {
		raw, err := json.Marshal(input.Schema)
		if err != nil {
			return fmt.Errorf("failed to marshal schema: %w", err)
		}
		var rs schema.ElicitRequestParamsRequestedSchema
		if err := json.Unmarshal(raw, &rs); err != nil {
			return fmt.Errorf("failed to parse schema: %w", err)
		}
		req.RequestedSchema = rs
	}

	_, status, payload, err := s.elicitor.Elicit(ctx, &turn, "assistant", req)
	if err != nil {
		return fmt.Errorf("elicitation failed: %w", err)
	}

	output.Action = status
	output.Payload = payload
	return nil
}
