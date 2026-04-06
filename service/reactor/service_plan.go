package reactor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/agent/plan"
	core2 "github.com/viant/agently-core/service/core"
	executil "github.com/viant/agently-core/service/shared/executil"
)

func hasRemovalTool(p *plan.Plan) bool {
	if p == nil || len(p.Steps) == 0 {
		return false
	}
	for _, st := range p.Steps {
		name := strings.ToLower(strings.TrimSpace(st.Name))
		if name == "internal/message:remove" || name == "message:remove" || strings.HasSuffix(name, ":remove") {
			return true
		}
	}
	return false
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Service) extendPlanFromResponse(ctx context.Context, genOutput *core2.GenerateOutput, aPlan *plan.Plan) (bool, error) {
	if genOutput.Response == nil || len(genOutput.Response.Choices) == 0 {
		return false, nil
	}
	for j := range genOutput.Response.Choices {
		choice := &genOutput.Response.Choices[j]
		s.extendPlanWithToolCalls(genOutput.Response.ResponseID, choice, aPlan)
	}
	if len(aPlan.Steps) == 0 {
		if err := s.extendPlanFromContent(ctx, genOutput, aPlan); err != nil {
			return false, err
		}
	}
	return !aPlan.IsEmpty(), nil
}

func (s *Service) extendPlanWithToolCalls(responseID string, choice *llm.Choice, aPlan *plan.Plan) {
	if len(choice.Message.ToolCalls) == 0 {
		return
	}
	reason := strings.TrimSpace(choice.Message.Content)
	steps := make(plan.Steps, 0, len(choice.Message.ToolCalls))
	for idx, tc := range choice.Message.ToolCalls {
		name := tc.Name
		args := tc.Arguments
		if name == "" && tc.Function.Name != "" {
			name = tc.Function.Name
		}
		if args == nil && tc.Function.Arguments != "" {
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsed); err == nil {
				args = parsed
			}
		}
		stepID := strings.TrimSpace(tc.ID)
		if stepID == "" {
			stepID = fallbackToolStepID(responseID, idx, name)
		}
		if prev := aPlan.Steps.Find(stepID); prev != nil {
			prev.Name = name
			prev.Args = args
			prev.Reason = reason
			continue
		}
		steps = append(steps, plan.Step{
			ID:         stepID,
			Type:       "tool",
			Name:       name,
			Args:       args,
			Reason:     reason,
			ResponseID: strings.TrimSpace(responseID),
		})
	}
	aPlan.Steps = append(aPlan.Steps, steps...)
}

func fallbackToolStepID(responseID string, idx int, name string) string {
	base := strings.TrimSpace(responseID)
	if base == "" {
		base = "stream"
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	return fmt.Sprintf("%s:%d:%s", base, idx, name)
}

func extractPriorToolResults(genInput *core2.GenerateInput) []llm.ToolCall {
	if genInput == nil || genInput.Binding == nil {
		return nil
	}
	messages := genInput.Binding.History.LLMMessages()
	if len(messages) == 0 {
		return nil
	}
	byID := map[string]llm.ToolCall{}
	order := make([]string, 0)
	for _, msg := range messages {
		if len(msg.ToolCalls) > 0 {
			for _, call := range msg.ToolCalls {
				id := strings.TrimSpace(call.ID)
				if id == "" {
					continue
				}
				if _, ok := byID[id]; !ok {
					order = append(order, id)
				}
				byID[id] = call
			}
			continue
		}
		if strings.ToLower(strings.TrimSpace(msg.Role.String())) != strings.ToLower(strings.TrimSpace(string(llm.RoleTool))) {
			continue
		}
		id := strings.TrimSpace(msg.ToolCallId)
		if id == "" {
			continue
		}
		call := byID[id]
		call.ID = id
		call.Result = strings.TrimSpace(msg.Content)
		if _, ok := byID[id]; !ok {
			order = append(order, id)
		}
		byID[id] = call
	}
	out := make([]llm.ToolCall, 0, len(order))
	for _, id := range order {
		call := byID[id]
		if strings.TrimSpace(call.Name) == "" {
			continue
		}
		out = append(out, call)
	}
	return out
}

func (s *Service) extendPlanFromContent(ctx context.Context, genOutput *core2.GenerateOutput, aPlan *plan.Plan) error {
	content := strings.TrimSpace(genOutput.Content)
	if genOutput != nil && genOutput.Response != nil {
		for _, choice := range genOutput.Response.Choices {
			text := strings.TrimSpace(choice.Message.Content)
			if text == "" {
				text = strings.TrimSpace(llm.MessageText(choice.Message))
			}
			if text != "" {
				content = text
				break
			}
		}
	}
	var err error
	if strings.Contains(content, `"tool"`) {
		err = executil.EnsureJSONResponse(ctx, content, aPlan)
	}
	if strings.Contains(content, `"elicitation"`) {
		aPlan.Elicitation = &plan.Elicitation{}
		_ = executil.EnsureJSONResponse(ctx, content, aPlan.Elicitation)
		if aPlan.Elicitation.IsEmpty() {
			aPlan.Elicitation = nil
		} else if aPlan.Elicitation.ElicitationId == "" {
			aPlan.Elicitation.ElicitationId = uuid.New().String()
		}
	}
	aPlan.Steps.EnsureID()
	if len(aPlan.Steps) > 0 && strings.TrimSpace(aPlan.Steps[0].Reason) == "" {
		prefix := content
		if idx := strings.Index(prefix, "```json"); idx != -1 {
			prefix = prefix[:idx]
		} else if idx := strings.Index(prefix, "{"); idx != -1 {
			prefix = prefix[:idx]
		}
		prefix = strings.TrimSpace(prefix)
		if prefix != "" {
			aPlan.Steps[0].Reason = prefix
		}
	}
	return err
}

func (s *Service) synthesizeFinalResponse(genOutput *core2.GenerateOutput) {
	if strings.TrimSpace(genOutput.Content) == "" || genOutput.Response != nil {
		return
	}
	genOutput.Response = &llm.GenerateResponse{
		Choices: []llm.Choice{{
			Index:        0,
			Message:      llm.Message{Role: llm.RoleAssistant, Content: strings.TrimSpace(genOutput.Content)},
			FinishReason: "stop",
		}},
	}
}
