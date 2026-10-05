package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/runtime/recovery"
)

type tokenCountingModel struct {
	counted, generated []byte
	counts, calls      int
}

func (m *tokenCountingModel) CountInputTokens(_ context.Context, r *llm.GenerateRequest) (int, error) {
	m.counts++
	m.counted, _ = json.Marshal(r)
	return 123, nil
}
func (m *tokenCountingModel) Generate(_ context.Context, r *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.calls++
	m.generated, _ = json.Marshal(r)
	return textGenerateResponse("ok"), nil
}
func (m *tokenCountingModel) Implements(string) bool { return false }

func TestCountInputTokens_PreparedRequestReusedWithoutDoubleInit(t *testing.T) {
	model := &tokenCountingModel{}
	svc := &Service{llmFinder: &generateFixedFinder{model: model}}
	input := newGenerateInput()
	input.Options = &llm.Options{}
	input.SystemPrompt = &binding.Prompt{Text: "system"}
	input.Binding.Tools.Signatures = []*llm.ToolDefinition{{Name: "lookup"}}
	tokens, err := svc.CountInputTokens(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, 123, tokens)
	size, tools := len(input.Message), len(input.Options.Tools)
	_, err = svc.CountInputTokens(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, size, len(input.Message))
	require.Equal(t, tools, len(input.Options.Tools))
	require.NoError(t, svc.Generate(context.Background(), input, &GenerateOutput{}))
	require.JSONEq(t, string(model.counted), string(model.generated))
	require.Equal(t, 1, model.calls)
}
func TestCountInputTokens_UnsupportedIsActionable(t *testing.T) {
	svc := &Service{llmFinder: &generateFixedFinder{model: &generateSequenceModel{}}}
	input := newGenerateInput()
	input.Options = &llm.Options{}
	_, err := svc.CountInputTokens(context.Background(), input)
	require.ErrorContains(t, err, "reliable input token counting")
}
func TestCompactionForcesFullHistoryForBothContinuationPaths(t *testing.T) {
	svc := &Service{}
	ctx := recovery.WithFullHistory(context.Background(), true)
	request := &llm.GenerateRequest{PreviousResponseID: "stale"}
	require.Nil(t, svc.BuildContinuationRequest(ctx, request, &binding.History{}))
	_, handled, err := svc.tryGenerateContinuationByAnchor(ctx, &tokenCountingModel{}, request)
	require.NoError(t, err)
	require.False(t, handled)
}

func TestPreparedTokenCountRequestPreservesRetryReminderWithoutReinit(t *testing.T) {
	model := &tokenCountingModel{}
	svc := &Service{llmFinder: &generateFixedFinder{model: model}}
	input := newGenerateInput()
	input.Options = &llm.Options{}
	input.Instructions = "keep these instructions"
	input.Binding.Tools.Signatures = []*llm.ToolDefinition{{Name: "lookup"}}
	_, err := svc.CountInputTokens(context.Background(), input)
	require.NoError(t, err)
	messages, tools := len(input.Message), len(input.Options.Tools)
	input.Message = append([]llm.Message{llm.NewSystemMessage("pending operation reminder")}, input.Message...)
	input.preparedRequest.PreviousResponseID = "stale-response"
	request, _, err := svc.prepareGenerateRequest(recovery.WithFullHistory(context.Background(), true), input)
	require.NoError(t, err)
	require.Equal(t, messages+1, len(input.Message))
	require.Equal(t, tools, len(input.Options.Tools))
	require.Contains(t, request.Messages[0].Content, "pending operation reminder")
	require.Empty(t, request.PreviousResponseID)
}
