package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/service/shared/executil"
)

const forcedInitialDelegationKey = "ForcedInitialDelegationDone"

type forcedDelegationResult struct {
	Answer         string   `json:"answer"`
	Status         string   `json:"status,omitempty"`
	ConversationID string   `json:"conversationId,omitempty"`
	MessageID      string   `json:"messageId,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
}

func (s *Service) maybeForceInitialRepoAnalysisDelegation(ctx context.Context, input *QueryInput, output *QueryOutput) (bool, error) {
	if !shouldForceInitialRepoAnalysisDelegation(input) {
		return false, nil
	}
	if s == nil || s.registry == nil || s.conversation == nil {
		return false, nil
	}
	if input.Context == nil {
		input.Context = map[string]interface{}{}
	}
	input.Context[forcedInitialDelegationKey] = true

	workdir := resolveDelegationWorkdir(input)
	childContext := cloneContextMap(input.Context)
	delete(childContext, forcedInitialDelegationKey)
	childContext["workdir"] = workdir
	childContext["resolvedWorkdir"] = workdir

	step := executil.StepInfo{
		ID:   "forced-delegation-" + uuid.NewString(),
		Name: "llm/agents:run",
		Args: map[string]interface{}{
			"agentId":   strings.TrimSpace(input.Agent.ID),
			"objective": strings.TrimSpace(input.Query),
			"context":   childContext,
		},
	}
	toolCall, _, err := executil.ExecuteToolStep(ctx, s.registry, step, s.conversation)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return true, err
		}
		return true, err
	}
	result := decodeForcedDelegationResult(toolCall.Result)
	answer := strings.TrimSpace(result.Answer)
	if answer == "" {
		answer = strings.TrimSpace(toolCall.Result)
	}
	if output != nil {
		output.Content = answer
		if strings.TrimSpace(result.MessageID) != "" {
			output.MessageID = strings.TrimSpace(result.MessageID)
		}
		if len(result.Warnings) > 0 {
			output.Warnings = append(output.Warnings, result.Warnings...)
		}
	}
	if strings.TrimSpace(answer) != "" {
		turn, ok := memoryTurnMeta(ctx)
		if ok && !shouldSkipFinalAssistantPersist(ctx, s.conversation, &turn, answer) {
			if _, addErr := s.addMessage(ctx, &turn, "assistant", input.Actor(), answer, nil, "plan", ""); addErr != nil {
				return true, addErr
			}
		}
	}
	return true, nil
}

func shouldForceInitialRepoAnalysisDelegation(input *QueryInput) bool {
	if input == nil || input.Agent == nil {
		return false
	}
	if marker, ok := input.Context[forcedInitialDelegationKey].(bool); ok && marker {
		return false
	}
	agentID := strings.TrimSpace(input.Agent.ID)
	if agentID != "coder" {
		return false
	}
	if input.Agent.Delegation == nil || !input.Agent.Delegation.Enabled {
		return false
	}
	maxDepth := input.Agent.Delegation.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}
	currentDepth := delegationDepthFromContextMap(input.Context, agentID)
	if currentDepth >= maxDepth || currentDepth > 0 {
		return false
	}
	if !looksLikeRepoAnalysisRequest(strings.TrimSpace(input.Query)) {
		return false
	}
	return resolveDelegationWorkdir(input) != ""
}

func decodeForcedDelegationResult(raw string) forcedDelegationResult {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return forcedDelegationResult{}
	}
	var result forcedDelegationResult
	if err := json.Unmarshal([]byte(trimmed), &result); err == nil {
		return result
	}
	return forcedDelegationResult{Answer: trimmed}
}

func memoryTurnMeta(ctx context.Context) (memory.TurnMeta, bool) {
	return memory.TurnMetaFromContext(ctx)
}
