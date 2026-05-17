package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/internal/logx"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	agenttool "github.com/viant/agently-core/service/agent/tool"
	intakesvc "github.com/viant/agently-core/service/intake"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
)

func validateDirectAction(action *intakesvc.DirectActionContext) error {
	if action == nil {
		return fmt.Errorf("direct action is nil")
	}
	toolName := strings.TrimSpace(action.ToolName)
	if toolName == "" {
		return fmt.Errorf("direct action toolName is required")
	}
	if strings.TrimSpace(action.AssistantText) == "" {
		return fmt.Errorf("direct action assistantText is required")
	}
	if action.Input == nil {
		return fmt.Errorf("direct action input is required")
	}
	return nil
}

func directActionSelectionFromIntake(cfg *agentmdl.Intake) agenttool.Selection {
	if cfg == nil {
		return agenttool.Selection{}
	}
	selection := agenttool.Selection{
		Bundles: append([]string(nil), cfg.Tool.Bundles...),
	}
	for _, item := range cfg.Tool.Items {
		if item == nil {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = strings.TrimSpace(item.Definition.Name)
		}
		if name == "" {
			continue
		}
		selection.Tools = append(selection.Tools, name)
	}
	return selection
}

func (s *Service) directActionAllowedToolNames(ctx context.Context, cfg *agentmdl.Intake) (map[string]struct{}, error) {
	control := directActionSelectionFromIntake(cfg)
	if len(control.Tools) == 0 && len(control.Bundles) == 0 {
		return nil, nil
	}
	defs, err := s.resolveStructuredToolDefinitions(ctx, control)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(mcpname.Canonical(def.Name))
		if name == "" {
			continue
		}
		allowed[strings.ToLower(name)] = struct{}{}
	}
	return allowed, nil
}

func (s *Service) authorizeDirectAction(ctx context.Context, input *QueryInput, action *intakesvc.DirectActionContext) error {
	if input == nil || input.Agent == nil {
		return fmt.Errorf("direct action requires an agent context")
	}
	allowed, err := s.directActionAllowedToolNames(ctx, &input.Agent.Intake)
	if err != nil {
		return err
	}
	toolName := strings.ToLower(strings.TrimSpace(mcpname.Canonical(action.ToolName)))
	if toolName == "" {
		return fmt.Errorf("direct action toolName is required")
	}
	if len(allowed) == 0 {
		return fmt.Errorf("direct action tool %q is not allowed by intake.tool policy", strings.TrimSpace(action.ToolName))
	}
	if _, ok := allowed[toolName]; !ok {
		return fmt.Errorf("direct action tool %q is not allowed by intake.tool policy", strings.TrimSpace(action.ToolName))
	}
	return nil
}

func (s *Service) maybeRunDirectAction(ctx context.Context, input *QueryInput, output *QueryOutput) (bool, error) {
	action := directActionFromContext(input.Context)
	if action == nil {
		return false, nil
	}
	if err := validateDirectAction(action); err != nil {
		return true, err
	}
	if err := s.authorizeDirectAction(ctx, input, action); err != nil {
		return true, err
	}
	toolName := strings.TrimSpace(action.ToolName)
	logx.Infof("conversation", "agent.Query directAction start convo=%q turn_id=%q tool=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), toolName)
	_, _, err := toolexec.ExecuteToolStep(ctx, s.registry, toolexec.StepInfo{
		Name:       toolName,
		Args:       action.Input,
		ResponseID: "intake_direct_action",
	}, s.conversation)
	if err != nil {
		return true, err
	}
	text := strings.TrimSpace(action.AssistantText)
	output.TurnID = input.MessageID
	output.MessageID = input.MessageID
	output.Content = text
	if err := s.publishAssistantMessageWithStatus(ctx, input, text, "intake.direct_action"); err != nil {
		return true, err
	}
	logx.Infof("conversation", "agent.Query directAction ok convo=%q turn_id=%q tool=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), toolName)
	return true, nil
}

func stringValue(v interface{}) string {
	if v == nil {
		return ""
	}
	switch actual := v.(type) {
	case string:
		return actual
	default:
		return fmt.Sprintf("%v", v)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeInterfaceMap(value interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]interface{}); ok {
		return mapped
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	result := map[string]interface{}{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	return result
}
