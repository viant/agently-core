package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/pkg/agently/tool/resolver"
	"github.com/viant/agently-core/protocol/agent/execution"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	elicitation "github.com/viant/agently-core/service/elicitation"
	elicaction "github.com/viant/agently-core/service/elicitation/action"
	toolapproval "github.com/viant/agently-core/service/shared/toolapproval"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

// agentToolApprovalElicitor implements toolapproval.Elicitor by wiring
// into the agent's elicitation service. It builds a tool_approval elicitation
// from the approval config and blocks until the user resolves it.
type agentToolApprovalElicitor struct {
	elicService *elicitation.Service
}

var _ toolapproval.Elicitor = (*agentToolApprovalElicitor)(nil)

// ElicitToolApproval builds a execution.Elicitation for the tool approval request,
// records it via the elicitation service, and waits for the user's decision.
// Returns the normalized action: "accept", "decline", or "cancel".
func (e *agentToolApprovalElicitor) ElicitToolApproval(
	ctx context.Context,
	turn *runtimerequestctx.TurnMeta,
	toolName string,
	cfg *llm.ApprovalConfig,
	args map[string]interface{},
) (string, map[string]interface{}, error) {
	view := toolapproval.BuildView(toolName, args, cfg)

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

	message := view.Message
	if message == "" {
		message = fmt.Sprintf("Approve execution of %s?", view.Title)
	}

	reqSchema := buildApprovalRequestedSchema(toolName, view, cfg, args, acceptLabel, rejectLabel, cancelLabel)
	req := &execution.Elicitation{
		ElicitRequestParams: mcpschema.ElicitRequestParams{
			Message:         message,
			RequestedSchema: reqSchema,
		},
	}

	_, action, payload, err := e.elicService.Elicit(ctx, turn, "assistant", req)
	if err != nil {
		return elicaction.Decline, nil, err
	}
	return elicaction.Normalize(action), payload, nil
}

func buildApprovalRequestedSchema(toolName string, view toolapproval.View, cfg *llm.ApprovalConfig, args map[string]interface{}, acceptLabel, rejectLabel, cancelLabel string) mcpschema.ElicitRequestParamsRequestedSchema {
	if cfg != nil && cfg.Review != nil && len(cfg.Review.RequestedSchema) > 0 {
		schema := cloneApprovalSchemaMap(cfg.Review.RequestedSchema)
		applyApprovalReviewSeeds(schema, cfg.Review.Seeds, args)
		properties := ensureApprovalSchemaProperties(schema)
		injectApprovalMeta(properties, map[string]interface{}{
			"type":        "tool_approval",
			"toolName":    toolName,
			"title":       view.Title,
			"message":     view.Message,
			"acceptLabel": acceptLabel,
			"rejectLabel": rejectLabel,
			"cancelLabel": cancelLabel,
			"review":      cfg.Review,
		}, toolName, view.Title, acceptLabel, rejectLabel, cancelLabel, "tool_approval")
		raw, _ := json.Marshal(schema)
		var reqSchema mcpschema.ElicitRequestParamsRequestedSchema
		if err := json.Unmarshal(raw, &reqSchema); err == nil {
			if strings.TrimSpace(reqSchema.Type) == "" {
				reqSchema.Type = "object"
			}
			if reqSchema.Properties == nil {
				reqSchema.Properties = map[string]interface{}{}
			}
			return reqSchema
		}
	}

	properties := map[string]interface{}{}
	injectApprovalMeta(properties, map[string]interface{}{
		"type":        "tool_approval",
		"toolName":    toolName,
		"title":       view.Title,
		"message":     view.Message,
		"acceptLabel": acceptLabel,
		"rejectLabel": rejectLabel,
		"cancelLabel": cancelLabel,
		"editors":     view.Editors,
	}, toolName, view.Title, acceptLabel, rejectLabel, cancelLabel, "tool_approval")
	return mcpschema.ElicitRequestParamsRequestedSchema{
		Type:       "object",
		Properties: properties,
	}
}

func injectApprovalMeta(properties map[string]interface{}, meta map[string]interface{}, toolName, title, acceptLabel, rejectLabel, cancelLabel, kind string) {
	properties["_type"] = map[string]interface{}{"type": "string", "const": kind}
	properties["_toolName"] = map[string]interface{}{"type": "string", "const": toolName}
	properties["_title"] = map[string]interface{}{"type": "string", "const": title}
	properties["_acceptLabel"] = map[string]interface{}{"type": "string", "const": acceptLabel}
	properties["_rejectLabel"] = map[string]interface{}{"type": "string", "const": rejectLabel}
	properties["_cancelLabel"] = map[string]interface{}{"type": "string", "const": cancelLabel}
	if raw, err := json.Marshal(meta); err == nil && len(raw) > 0 {
		properties["_approvalMeta"] = map[string]interface{}{"type": "string", "const": string(raw)}
	}
}

func cloneApprovalSchemaMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return map[string]interface{}{}
	}
	raw, err := json.Marshal(src)
	if err != nil {
		out := make(map[string]interface{}, len(src))
		for key, value := range src {
			out[key] = value
		}
		return out
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

func ensureApprovalSchemaProperties(schema map[string]interface{}) map[string]interface{} {
	if len(schema) == 0 {
		return map[string]interface{}{}
	}
	if existing, ok := schema["properties"].(map[string]interface{}); ok && existing != nil {
		return existing
	}
	properties := map[string]interface{}{}
	schema["properties"] = properties
	return properties
}

func applyApprovalReviewSeeds(schema map[string]interface{}, seeds []*llm.ApprovalReviewSeed, args map[string]interface{}) {
	for _, seed := range seeds {
		if seed == nil {
			continue
		}
		path := strings.TrimSpace(seed.SchemaPath)
		selector := strings.TrimSpace(seed.Selector)
		if path == "" || selector == "" {
			continue
		}
		if !strings.HasPrefix(selector, "input.") && !strings.HasPrefix(selector, "output.") && selector != "input" && selector != "output" {
			selector = "input." + selector
		}
		value := resolver.Select(selector, args, nil)
		if value == nil {
			continue
		}
		assignApprovalSchemaPath(schema, path, value)
	}
}

func assignApprovalSchemaPath(target map[string]interface{}, path string, value interface{}) {
	if len(target) == 0 || strings.TrimSpace(path) == "" {
		return
	}
	parts := strings.Split(path, ".")
	current := target
	for index, part := range parts {
		key := strings.TrimSpace(part)
		if key == "" {
			return
		}
		if index == len(parts)-1 {
			current[key] = value
			return
		}
		next, ok := current[key].(map[string]interface{})
		if !ok || next == nil {
			next = map[string]interface{}{}
			current[key] = next
		}
		current = next
	}
}
