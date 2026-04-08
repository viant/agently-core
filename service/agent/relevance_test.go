package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/genai/llm"
)

func TestParseRelevanceSelectorOutput_ToolCall(t *testing.T) {
	resp := &llm.GenerateResponse{
		Choices: []llm.Choice{
			{
				Message: llm.Message{
					ToolCalls: []llm.ToolCall{
						{
							Name: "message:project",
							Arguments: map[string]interface{}{
								"turnIds": []interface{}{"turn-1", "turn-2"},
								"reason":  "irrelevant historical turns",
							},
						},
					},
				},
			},
		},
	}
	got := parseRelevanceSelectorOutput(resp, "")
	require.NotNil(t, got)
	require.Equal(t, []string{"turn-1", "turn-2"}, got.TurnIDs)
	require.Equal(t, "irrelevant historical turns", got.Reason)
}

func TestRunRelevanceSelectors_ChunksAndMerges(t *testing.T) {
	chunk := 2
	concurrency := 2
	svc := &Service{
		defaults: &config.Defaults{
			Projection: config.Projection{
				Relevance: &config.RelevanceProjection{
					ChunkSize:      &chunk,
					MaxConcurrency: &concurrency,
				},
			},
		},
		relevanceSelector: func(_ context.Context, input relevanceSelectorInput) (*relevanceSelectorOutput, error) {
			var ids []string
			for _, candidate := range input.Candidates {
				ids = append(ids, candidate.TurnID)
			}
			return &relevanceSelectorOutput{
				TurnIDs: ids,
				Reason:  "chunk",
			}, nil
		},
	}

	out, err := svc.runRelevanceSelectors(context.Background(), relevanceSelectorInput{
		CurrentTask: "task",
		Candidates: []relevanceTurnCandidate{
			{TurnID: "turn-1"},
			{TurnID: "turn-2"},
			{TurnID: "turn-3"},
			{TurnID: "turn-4"},
			{TurnID: "turn-5"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.ElementsMatch(t, []string{"turn-1", "turn-2", "turn-3", "turn-4", "turn-5"}, out.TurnIDs)
	require.Contains(t, out.Reason, "chunk")
}

func TestRunRelevanceSelectors_RespectsMaxConcurrency(t *testing.T) {
	chunk := 1
	concurrency := 2
	var current atomic.Int32
	var maxSeen atomic.Int32
	svc := &Service{
		defaults: &config.Defaults{
			Projection: config.Projection{
				Relevance: &config.RelevanceProjection{
					ChunkSize:      &chunk,
					MaxConcurrency: &concurrency,
				},
			},
		},
		relevanceSelector: func(_ context.Context, input relevanceSelectorInput) (*relevanceSelectorOutput, error) {
			inFlight := current.Add(1)
			for {
				prev := maxSeen.Load()
				if inFlight <= prev || maxSeen.CompareAndSwap(prev, inFlight) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			current.Add(-1)
			return &relevanceSelectorOutput{TurnIDs: []string{input.Candidates[0].TurnID}}, nil
		},
	}

	_, err := svc.runRelevanceSelectors(context.Background(), relevanceSelectorInput{
		CurrentTask: "task",
		Candidates: []relevanceTurnCandidate{
			{TurnID: "turn-1"},
			{TurnID: "turn-2"},
			{TurnID: "turn-3"},
			{TurnID: "turn-4"},
		},
	})
	require.NoError(t, err)
	require.LessOrEqual(t, maxSeen.Load(), int32(concurrency))
	require.GreaterOrEqual(t, maxSeen.Load(), int32(1))
}
