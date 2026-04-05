package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/agent/plan"
	"github.com/viant/agently-core/runtime/memory"
	elicitation "github.com/viant/agently-core/service/elicitation"
	elicaction "github.com/viant/agently-core/service/elicitation/action"
	executil "github.com/viant/agently-core/service/shared/executil"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

// agentToolApprovalElicitor implements executil.ToolApprovalElicitor by wiring
// into the agent's elicitation service. It builds a tool_approval elicitation
// from the approval config and blocks until the user resolves it.
type agentToolApprovalElicitor struct {
	elicService *elicitation.Service
}

var _ executil.ToolApprovalElicitor = (*agentToolApprovalElicitor)(nil)

// ElicitToolApproval builds a plan.Elicitation for the tool approval request,
// records it via the elicitation service, and waits for the user's decision.
// Returns the normalized action: "accept", "decline", or "cancel".
func (e *agentToolApprovalElicitor) ElicitToolApproval(
	ctx context.Context,
	turn *memory.TurnMeta,
	toolName string,
	cfg *llm.ApprovalConfig,
	args map[string]interface{},
) (string, map[string]interface{}, error) {
	view := executil.BuildApprovalView(toolName, args, cfg)

	acceptLabel := "Accept"
	rejectLabel := "Reject"
	cancelLabel := "Cancel"
	if cfg != nil && cfg.Prompt != nil {
		if strings.TrimSpace(cfg.Prompt.AcceptLabel) != "" {
			acceptLabel = cfg.Prompt.AcceptLabel
		}
		if strings.TrimSpace(cfg.Prompt.RejectLabel) != "" {
			rejectLabel = cfg.Prompt.RejectLabel
		}
		if strings.TrimSpace(cfg.Prompt.CancelLabel) != "" {
			cancelLabel = cfg.Prompt.CancelLabel
		}
	}

	// Embed approval metadata as schema properties so the UI can detect
	// this is a tool_approval elicitation and render accordingly.
	properties := map[string]interface{}{
		"_type":        map[string]interface{}{"type": "string", "const": "tool_approval"},
		"_toolName":    map[string]interface{}{"type": "string", "const": toolName},
		"_title":       map[string]interface{}{"type": "string", "const": view.Title},
		"_acceptLabel": map[string]interface{}{"type": "string", "const": acceptLabel},
		"_rejectLabel": map[string]interface{}{"type": "string", "const": rejectLabel},
		"_cancelLabel": map[string]interface{}{"type": "string", "const": cancelLabel},
	}
	if meta, err := json.Marshal(map[string]interface{}{
		"type":        "tool_approval",
		"toolName":    toolName,
		"title":       view.Title,
		"message":     view.Message,
		"acceptLabel": acceptLabel,
		"rejectLabel": rejectLabel,
		"cancelLabel": cancelLabel,
		"editors":     view.Editors,
	}); err == nil && len(meta) > 0 {
		properties["_approvalMeta"] = map[string]interface{}{"type": "string", "const": string(meta)}
	}

	message := view.Message
	if message == "" {
		message = fmt.Sprintf("Approve execution of %s?", view.Title)
	}

	req := &plan.Elicitation{
		ElicitRequestParams: mcpschema.ElicitRequestParams{
			Message: message,
			RequestedSchema: mcpschema.ElicitRequestParamsRequestedSchema{
				Type:       "object",
				Properties: properties,
			},
		},
	}

	_, action, payload, err := e.elicService.Elicit(ctx, turn, "assistant", req)
	if err != nil {
		return elicaction.Decline, nil, err
	}
	return elicaction.Normalize(action), payload, nil
}
