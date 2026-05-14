package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/viant/agently-core/internal/logx"
	intakesvc "github.com/viant/agently-core/service/intake"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
)

var allowedDirectActionTools = map[string]bool{
	"ui/view:open":   true,
	"ui/window:show": true,
}

func validateDirectAction(action *intakesvc.DirectActionContext) error {
	if action == nil {
		return fmt.Errorf("direct action is nil")
	}
	toolName := strings.TrimSpace(action.ToolName)
	if toolName == "" {
		return fmt.Errorf("direct action toolName is required")
	}
	if !allowedDirectActionTools[toolName] {
		return fmt.Errorf("direct action tool %q is not allowed", toolName)
	}
	if strings.TrimSpace(action.AssistantText) == "" {
		return fmt.Errorf("direct action assistantText is required")
	}
	if action.Input == nil {
		return fmt.Errorf("direct action input is required")
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
	toolName := strings.TrimSpace(action.ToolName)
	logx.Infof("conversation", "agent.Query directAction start convo=%q turn_id=%q tool=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), toolName)
	if _, _, err := toolexec.ExecuteToolStep(ctx, s.registry, toolexec.StepInfo{
		Name:       toolName,
		Args:       action.Input,
		ResponseID: "intake_direct_action",
	}, s.conversation); err != nil {
		return true, err
	}
	text := strings.TrimSpace(action.AssistantText)
	output.MessageID = input.MessageID
	output.Content = text
	if err := s.publishAssistantMessageWithStatus(ctx, input, text, "intake.direct_action"); err != nil {
		return true, err
	}
	logx.Infof("conversation", "agent.Query directAction ok convo=%q turn_id=%q tool=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), toolName)
	return true, nil
}
