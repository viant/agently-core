package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/tool"
	runtimeprojection "github.com/viant/agently-core/runtime/projection"
	"github.com/viant/agently-core/service/agent/prompts"
	"github.com/viant/agently-core/service/core"
)

type relevanceSelectorInput struct {
	CurrentTask       string                   `json:"currentTask"`
	ProtectedTurnIDs  []string                 `json:"protectedTurnIds"`
	Candidates        []relevanceTurnCandidate `json:"candidates"`
	ProjectionScope   string                   `json:"projectionScope,omitempty"`
	ConversationID    string                   `json:"conversationId,omitempty"`
	CurrentTurnID     string                   `json:"currentTurnId,omitempty"`
	ApproxTokenBudget int                      `json:"approxTokenBudget,omitempty"`
}

type relevanceTurnCandidate struct {
	TurnID          string `json:"turnId"`
	UserText        string `json:"userText,omitempty"`
	AssistantText   string `json:"assistantText,omitempty"`
	EstimatedTokens int    `json:"estimatedTokens,omitempty"`
}

type relevanceSelectorOutput struct {
	TurnIDs []string `json:"turnIds"`
	Reason  string   `json:"reason,omitempty"`
}

func (s *Service) applyRelevanceProjection(
	ctx context.Context,
	transcript apiconv.Transcript,
	input *QueryInput,
	currentTurnID string,
	scope string,
) {
	if s == nil || s.defaults == nil || input == nil {
		return
	}
	relevance := s.defaults.Projection.Relevance
	if !relevance.IsEnabled() {
		return
	}
	if strings.TrimSpace(input.Query) == "" {
		return
	}
	state, ok := runtimeprojection.StateFromContext(ctx)
	if !ok {
		return
	}

	candidates, protected, totalTokens := buildRelevanceSelectorInput(transcript, currentTurnID, relevance.ProtectedTurns())
	if len(candidates) == 0 {
		return
	}
	if threshold := relevance.Threshold(); threshold > 0 && totalTokens < threshold {
		return
	}

	selectorInput := relevanceSelectorInput{
		CurrentTask:       strings.TrimSpace(input.Query),
		ProtectedTurnIDs:  protected,
		Candidates:        candidates,
		ProjectionScope:   strings.TrimSpace(scope),
		ConversationID:    strings.TrimSpace(input.ConversationID),
		CurrentTurnID:     strings.TrimSpace(currentTurnID),
		ApproxTokenBudget: totalTokens,
	}
	result, err := s.runRelevanceSelectors(ctx, selectorInput)
	if err != nil || result == nil || len(result.TurnIDs) == 0 {
		return
	}
	state.HideTurns(result.TurnIDs...)
	if reason := strings.TrimSpace(result.Reason); reason != "" {
		state.AddReason(reason)
	} else {
		state.AddReason("relevance projection")
	}
	state.AddTokensFreed(estimateRelevanceHiddenTokens(candidates, result.TurnIDs))
}

func (s *Service) runRelevanceSelectors(ctx context.Context, input relevanceSelectorInput) (*relevanceSelectorOutput, error) {
	if s == nil || s.defaults == nil || s.defaults.Projection.Relevance == nil {
		return s.runRelevanceSelector(ctx, input)
	}
	chunkSize := s.defaults.Projection.Relevance.Chunk()
	if chunkSize <= 0 || len(input.Candidates) <= chunkSize {
		return s.runRelevanceSelector(ctx, input)
	}
	maxConcurrency := s.defaults.Projection.Relevance.Concurrency()
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	chunks := chunkRelevanceCandidates(input.Candidates, chunkSize)
	type chunkResult struct {
		output *relevanceSelectorOutput
		err    error
	}
	results := make(chan chunkResult, len(chunks))
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for _, chunk := range chunks {
		chunkInput := input
		chunkInput.Candidates = chunk
		wg.Add(1)
		go func(in relevanceSelectorInput) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, err := s.runRelevanceSelector(ctx, in)
			results <- chunkResult{output: out, err: err}
		}(chunkInput)
	}
	wg.Wait()
	close(results)

	merged := &relevanceSelectorOutput{}
	for result := range results {
		if result.err != nil {
			return nil, result.err
		}
		if result.output == nil {
			continue
		}
		merged.TurnIDs = appendUniqueSelectorStrings(merged.TurnIDs, result.output.TurnIDs...)
		if reason := strings.TrimSpace(result.output.Reason); reason != "" && !strings.Contains(merged.Reason, reason) {
			if merged.Reason == "" {
				merged.Reason = reason
			} else {
				merged.Reason += "; " + reason
			}
		}
	}
	if len(merged.TurnIDs) == 0 && strings.TrimSpace(merged.Reason) == "" {
		return nil, nil
	}
	return merged, nil
}

func buildRelevanceSelectorInput(transcript apiconv.Transcript, currentTurnID string, protectedRecentTurns int) ([]relevanceTurnCandidate, []string, int) {
	if len(transcript) == 0 {
		return nil, nil, 0
	}
	if protectedRecentTurns <= 0 {
		protectedRecentTurns = 1
	}
	var priorTurns []*apiconv.Turn
	for _, turn := range transcript {
		if turn == nil {
			continue
		}
		if strings.TrimSpace(turn.Id) == strings.TrimSpace(currentTurnID) {
			continue
		}
		priorTurns = append(priorTurns, turn)
	}
	if len(priorTurns) == 0 {
		return nil, nil, 0
	}
	protectedStart := len(priorTurns) - protectedRecentTurns
	if protectedStart < 0 {
		protectedStart = 0
	}
	protected := make([]string, 0, len(priorTurns)-protectedStart)
	for i := protectedStart; i < len(priorTurns); i++ {
		protected = append(protected, strings.TrimSpace(priorTurns[i].Id))
	}
	protectedSet := map[string]struct{}{}
	for _, id := range protected {
		protectedSet[id] = struct{}{}
	}
	var candidates []relevanceTurnCandidate
	totalTokens := 0
	for _, turn := range priorTurns {
		if turn == nil {
			continue
		}
		turnID := strings.TrimSpace(turn.Id)
		if turnID == "" {
			continue
		}
		if _, ok := protectedSet[turnID]; ok {
			continue
		}
		var userText, assistantText string
		estimated := 0
		for _, msg := range turn.GetMessages() {
			if msg == nil || msg.IsArchived() || msg.IsInterim() {
				continue
			}
			content := strings.TrimSpace(msg.GetContent())
			if content == "" {
				continue
			}
			role := strings.ToLower(strings.TrimSpace(msg.Role))
			switch role {
			case "user":
				if userText == "" {
					userText = content
				}
			case "assistant":
				if assistantText == "" {
					assistantText = content
				}
			}
			estimated += estimateSimpleTokens(content)
		}
		if userText == "" && assistantText == "" {
			continue
		}
		totalTokens += estimated
		candidates = append(candidates, relevanceTurnCandidate{
			TurnID:          turnID,
			UserText:        userText,
			AssistantText:   assistantText,
			EstimatedTokens: estimated,
		})
	}
	return candidates, protected, totalTokens
}

func (s *Service) runRelevanceSelector(ctx context.Context, input relevanceSelectorInput) (*relevanceSelectorOutput, error) {
	if s.relevanceSelector != nil {
		return s.relevanceSelector(ctx, input)
	}
	if s == nil || s.llm == nil || s.defaults == nil {
		return nil, nil
	}
	relevance := s.defaults.Projection.Relevance
	if relevance == nil {
		return nil, nil
	}
	modelName := ""
	if relevance.Model != nil {
		modelName = strings.TrimSpace(*relevance.Model)
	}
	if modelName == "" {
		modelName = strings.TrimSpace(s.defaults.Model)
	}
	if modelName == "" {
		return nil, nil
	}
	if !s.llm.ModelImplements(ctx, modelName, base.CanUseTools) {
		return nil, nil
	}
	systemPrompt := &binding.Prompt{Text: prompts.RelevanceProjection}
	if relevance.Prompt != nil {
		systemPrompt = relevance.Prompt
	}
	binding := &binding.Binding{
		Context: map[string]interface{}{
			"projection": input,
		},
	}
	systemText, err := systemPrompt.Generate(ctx, binding)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	genInput := &core.GenerateInput{
		UserID: "system",
		ModelSelection: llm.ModelSelection{
			Model: modelName,
			Options: &llm.Options{
				Temperature: 0.0000001,
				MaxTokens:   256,
				Mode:        "router",
			},
		},
		Message: []llm.Message{
			llm.NewSystemMessage(systemText),
			llm.NewUserMessage(string(payload)),
		},
	}
	def := relevanceProjectToolDefinition(s.registry)
	if def == nil {
		return nil, nil
	}
	genInput.ModelSelection.Options.Tools = []llm.Tool{llm.NewFunctionTool(*def)}
	genInput.ModelSelection.Options.ToolChoice = llm.NewFunctionToolChoice(strings.TrimSpace(def.Name))
	output := &core.GenerateOutput{}
	if err = s.llm.Generate(ctx, genInput, output); err != nil {
		return nil, err
	}
	return parseRelevanceSelectorOutput(output.Response, output.Content), nil
}

func parseRelevanceSelectorOutput(resp *llm.GenerateResponse, content string) *relevanceSelectorOutput {
	if resp != nil {
		for _, choice := range resp.Choices {
			for _, call := range choice.Message.ToolCalls {
				if strings.TrimSpace(call.Name) == "" {
					continue
				}
				if strings.EqualFold(strings.TrimSpace(call.Name), "message:project") || strings.EqualFold(strings.TrimSpace(call.Name), "message/project") {
					out := &relevanceSelectorOutput{}
					if v, ok := call.Arguments["turnIds"]; ok {
						out.TurnIDs = appendJSONStringSlice(nil, v)
					}
					if v, ok := call.Arguments["reason"].(string); ok {
						out.Reason = strings.TrimSpace(v)
					}
					return out
				}
			}
		}
	}
	return nil
}

func relevanceProjectToolDefinition(reg tool.Registry) *llm.ToolDefinition {
	if reg == nil {
		return nil
	}
	for _, name := range []string{"message:project", "message/project", "message-project"} {
		if def, ok := reg.GetDefinition(name); ok && def != nil {
			cp := *def
			return &cp
		}
	}
	return nil
}

func appendJSONStringSlice(dst []string, value interface{}) []string {
	switch actual := value.(type) {
	case []interface{}:
		for _, item := range actual {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				dst = append(dst, strings.TrimSpace(text))
			}
		}
	case []string:
		for _, item := range actual {
			if text := strings.TrimSpace(item); text != "" {
				dst = append(dst, text)
			}
		}
	}
	return dst
}

func chunkRelevanceCandidates(candidates []relevanceTurnCandidate, chunkSize int) [][]relevanceTurnCandidate {
	if len(candidates) == 0 || chunkSize <= 0 || len(candidates) <= chunkSize {
		if len(candidates) == 0 {
			return nil
		}
		return [][]relevanceTurnCandidate{candidates}
	}
	result := make([][]relevanceTurnCandidate, 0, (len(candidates)+chunkSize-1)/chunkSize)
	for start := 0; start < len(candidates); start += chunkSize {
		end := start + chunkSize
		if end > len(candidates) {
			end = len(candidates)
		}
		result = append(result, candidates[start:end])
	}
	return result
}

func appendUniqueSelectorStrings(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, existing := range dst {
		key := strings.TrimSpace(existing)
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
	}
	for _, raw := range values {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, key)
	}
	return dst
}

func estimateRelevanceHiddenTokens(candidates []relevanceTurnCandidate, hiddenTurnIDs []string) int {
	if len(candidates) == 0 || len(hiddenTurnIDs) == 0 {
		return 0
	}
	hidden := map[string]struct{}{}
	for _, id := range hiddenTurnIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		hidden[id] = struct{}{}
	}
	total := 0
	for _, candidate := range candidates {
		if _, ok := hidden[strings.TrimSpace(candidate.TurnID)]; ok {
			total += candidate.EstimatedTokens
		}
	}
	return total
}

func estimateSimpleTokens(content string) int {
	content = strings.TrimSpace(content)
	if content == "" {
		return 0
	}
	if len(content) < 8 {
		return 1
	}
	return (len(content) + 3) / 4
}
