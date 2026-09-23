package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/logx"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	agenttool "github.com/viant/agently-core/service/agent/tool"
	intakesvc "github.com/viant/agently-core/service/intake"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
)

const directActionToolResultAssistantText = "$toolResult"

// allowModelDirectAction applies the workspace's explicit model-action policy.
// The intake tool selection and the tool implementation remain the authority
// for tool access and argument validation; this only decides whether the
// model's proposal can skip the main agent loop.
func allowModelDirectAction(tc *intakesvc.Context, cfg *agentmdl.Intake) bool {
	if tc == nil || cfg == nil || cfg.ModelDirectAction == nil ||
		tc.Classification.Confidence < modelDirectActionThreshold(cfg) ||
		strings.EqualFold(strings.TrimSpace(tc.Routing.Mode), intakesvc.ModeClarify) ||
		strings.EqualFold(strings.TrimSpace(tc.Routing.Mode), intakesvc.ModePlanner) ||
		validateDirectAction(&tc.DirectAction) != nil {
		return false
	}
	if required := strings.TrimSpace(cfg.ModelDirectAction.RequiredProfileID); required != "" &&
		!strings.EqualFold(strings.TrimSpace(tc.Prompting.SuggestedProfileID), required) {
		return false
	}
	return modelDirectActionToolAllowed(tc.DirectAction.ToolName, cfg.ModelDirectAction)
}

func modelDirectActionToolAllowed(toolName string, policy *agentmdl.ModelDirectActionPolicy) bool {
	if policy == nil {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(mcpname.Canonical(toolName)))
	for _, allowed := range policy.AllowedTools {
		if name == strings.ToLower(strings.TrimSpace(mcpname.Canonical(allowed))) {
			return true
		}
	}
	return false
}

func modelDirectActionThreshold(cfg *agentmdl.Intake) float64 {
	threshold := cfg.EffectiveConfidenceThreshold()
	if cfg.ModelDirectAction != nil && cfg.ModelDirectAction.MinConfidence > threshold {
		threshold = cfg.ModelDirectAction.MinConfidence
	}
	return threshold
}

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
	switch strings.ToLower(strings.TrimSpace(mcpname.Display(toolName))) {
	case "ui/view/open":
		if strings.TrimSpace(stringValue(action.Input["id"])) == "" {
			items, ok := action.Input["items"].([]interface{})
			if !ok || len(items) == 0 {
				return fmt.Errorf("ui/view:open direct action input.id or input.items is required")
			}
		}
	}
	return nil
}

func clearDirectActionInContext(ctx map[string]any) {
	tc := intakesvc.FromContext(ctx)
	if tc == nil {
		return
	}
	tc.DirectAction = intakesvc.DirectActionContext{}
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
		logx.Warnf("conversation", "agent.Query directAction ignored convo=%q turn_id=%q reason=%v", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), err)
		clearDirectActionInContext(input.Context)
		return false, nil
	}
	modelProposed := false
	if tc := intakesvc.FromContext(input.Context); tc != nil {
		modelProposed = strings.EqualFold(strings.TrimSpace(tc.Routing.Source), intakesvc.SourceAgent)
	}
	if input.Agent != nil && modelDirectActionToolAllowed(action.ToolName, input.Agent.Intake.ModelDirectAction) {
		policy := input.Agent.Intake.ModelDirectAction
		if policy.RequireLiveClient && !s.hasRequestedUIClient(ctx, input.ConversationID) {
			message := strings.TrimSpace(policy.UnavailableText)
			if message == "" {
				message = "This action needs an active client. Open the appropriate app and try again."
			}
			output.TurnID = input.MessageID
			output.MessageID = input.MessageID
			output.Content = message
			return true, s.publishDirectActionAssistantMessage(ctx, input, message)
		}
	}
	if err := s.authorizeDirectAction(ctx, input, action); err != nil {
		logx.Warnf("conversation", "agent.Query directAction unauthorized convo=%q turn_id=%q tool=%q reason=%v", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), strings.TrimSpace(action.ToolName), err)
		clearDirectActionInContext(input.Context)
		return false, nil
	}
	toolName := strings.TrimSpace(action.ToolName)
	publicationCtx := ctx
	if modelProposed && input.Agent != nil && input.Agent.Intake.ModelDirectAction != nil {
		if seconds := input.Agent.Intake.ModelDirectAction.TimeoutSec; seconds > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
			defer cancel()
		}
	}
	logx.Infof("conversation", "agent.Query directAction start convo=%q turn_id=%q tool=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), toolName)
	toolCall, _, err := toolexec.ExecuteToolStep(ctx, s.registry, toolexec.StepInfo{
		Name:       toolName,
		Args:       action.Input,
		ResponseID: "intake_direct_action",
	}, s.conversation)
	if err != nil {
		if modelProposed {
			var policy *agentmdl.ModelDirectActionPolicy
			if input.Agent != nil {
				policy = input.Agent.Intake.ModelDirectAction
			}
			if policy != nil && strings.TrimSpace(policy.FailureText) != "" {
				message := strings.TrimSpace(policy.FailureText)
				logx.Warnf("conversation", "agent.Query model directAction failed convo=%q turn_id=%q tool=%q reason=%v", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), toolName, err)
				output.TurnID = input.MessageID
				output.MessageID = input.MessageID
				output.Content = message
				return true, s.publishDirectActionAssistantMessage(publicationCtx, input, message)
			}
			logx.Warnf("conversation", "agent.Query model directAction fell through convo=%q turn_id=%q tool=%q reason=%v", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), toolName, err)
			clearDirectActionInContext(input.Context)
			return false, nil
		}
		return true, err
	}
	s.annotateDirectActionExecution(input, action, &toolCall.Result)
	text := directActionAssistantText(action, toolCall.Result)
	output.TurnID = input.MessageID
	output.MessageID = input.MessageID
	output.Content = text
	if err := s.publishDirectActionAssistantMessage(ctx, input, text); err != nil {
		return true, err
	}
	logx.Infof("conversation", "agent.Query directAction ok convo=%q turn_id=%q tool=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), toolName)
	return true, nil
}

func directActionClientKind(inputContext map[string]any) string {
	if inputContext == nil {
		return ""
	}
	switch client := inputContext["client"].(type) {
	case map[string]any:
		return strings.TrimSpace(stringValue(client["kind"]))
	case map[string]string:
		return strings.TrimSpace(client["kind"])
	default:
		return ""
	}
}

func (s *Service) hasRequestedUIClient(ctx context.Context, conversationID string) bool {
	clientID := strings.TrimSpace(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	if clientID == "" || s == nil || s.uiRegistry == nil {
		return false
	}
	clients, err := s.uiRegistry.ListAttachedByConversation(ctx, conversationID)
	if err != nil {
		return false
	}
	for _, client := range clients {
		if strings.TrimSpace(client.ClientID) == clientID {
			return true
		}
	}
	return false
}

func directActionAssistantText(action *intakesvc.DirectActionContext, result string) string {
	if action == nil {
		return ""
	}
	configured := strings.TrimSpace(action.AssistantText)
	if configured != directActionToolResultAssistantText {
		return configured
	}
	return strings.TrimSpace(result)
}

func (s *Service) publishDirectActionAssistantMessage(ctx context.Context, input *QueryInput, text string) error {
	return s.publishAssistantMessageWithStatus(ctx, input, text, "completed")
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

func normalizeToolResult(result string) interface{} {
	result = strings.TrimSpace(result)
	if result == "" {
		return nil
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(result), &decoded); err == nil {
		return decoded
	}
	return nil
}

func (s *Service) annotateDirectActionExecution(input *QueryInput, action *intakesvc.DirectActionContext, result *string) {
	if input == nil || action == nil {
		return
	}
	tc := intakesvc.FromContext(input.Context)
	if tc == nil {
		return
	}
	resultText := ""
	var normalized interface{}
	if result != nil {
		resultText = strings.TrimSpace(*result)
		normalized = normalizeToolResult(resultText)
	}
	tc.DirectActionExecution = intakesvc.DirectActionExecutionContext{
		Executed:   true,
		ToolName:   strings.TrimSpace(action.ToolName),
		Result:     normalized,
		ResultText: resultText,
	}
	input.Context["intake.directActionExecuted"] = true
	input.Context["intake.directActionTool"] = strings.TrimSpace(action.ToolName)
	if normalized != nil {
		input.Context["intake.directActionResult"] = normalized
	}
	if resultText != "" {
		input.Context["intake.directActionResultText"] = resultText
	}
}
