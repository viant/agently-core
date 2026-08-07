package converse

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestApplyStreamEvent_TextToolArgumentsAndUsage(t *testing.T) {
	state := newStreamState()

	textEvents := applyStreamEvent(&types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{
		ContentBlockIndex: aws.Int32(0), Delta: &types.ContentBlockDeltaMemberText{Value: "hello"},
	}}, state)
	require.Equal(t, llm.StreamEventTextDelta, textEvents[0].Kind)
	require.Equal(t, "hello", textEvents[0].Delta)

	started := applyStreamEvent(&types.ConverseStreamOutputMemberContentBlockStart{Value: types.ContentBlockStartEvent{
		ContentBlockIndex: aws.Int32(1), Start: &types.ContentBlockStartMemberToolUse{Value: types.ToolUseBlockStart{ToolUseId: aws.String("tool-1"), Name: aws.String("find")}},
	}}, state)
	require.Equal(t, llm.StreamEventToolCallStarted, started[0].Kind)

	for _, fragment := range []string{`{"query":`, `"needle"}`} {
		delta := applyStreamEvent(&types.ConverseStreamOutputMemberContentBlockDelta{Value: types.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(1), Delta: &types.ContentBlockDeltaMemberToolUse{Value: types.ToolUseBlockDelta{Input: aws.String(fragment)}},
		}}, state)
		require.Equal(t, llm.StreamEventToolCallDelta, delta[0].Kind)
	}
	completed := applyStreamEvent(&types.ConverseStreamOutputMemberContentBlockStop{Value: types.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(1)}}, state)
	require.Equal(t, llm.StreamEventToolCallCompleted, completed[0].Kind)
	require.Equal(t, "needle", completed[0].Arguments["query"])
	require.Len(t, state.message.ToolCalls, 1)

	usageEvents := applyStreamEvent(&types.ConverseStreamOutputMemberMetadata{Value: types.ConverseStreamMetadataEvent{Usage: &types.TokenUsage{
		InputTokens: aws.Int32(10), OutputTokens: aws.Int32(4), TotalTokens: aws.Int32(14),
	}}}, state)
	require.Equal(t, llm.StreamEventUsage, usageEvents[0].Kind)
	require.Equal(t, 14, usageEvents[0].Usage.TotalTokens)

	stopped := applyStreamEvent(&types.ConverseStreamOutputMemberMessageStop{Value: types.MessageStopEvent{StopReason: types.StopReasonToolUse}}, state)
	require.Equal(t, llm.StreamEventTurnCompleted, stopped[0].Kind)
	require.Equal(t, "tool_use", stopped[0].FinishReason)
}
