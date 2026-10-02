package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestBuildToolExecutions_DecodesCompressedToolResult(t *testing.T) {
	now := time.Now().UTC()
	body := "{\"agents\":[{\"id\":\"chatter\"}]}"
	turnID := "turn-1"

	service := &Service{
		defaults: &config.Defaults{
			PreviewSettings: config.PreviewSettings{Limit: 4096},
		},
	}

	conv := &apiconv.Conversation{
		Transcript: []*conversationmodel.TranscriptView{
			{
				Id: turnID,
				Message: []*conversationmodel.MessageView{
					{
						Id:             "tool-parent-1",
						ConversationId: "conv-1",
						TurnId:         strPtr(turnID),
						Role:           "assistant",
						Type:           "tool_op",
						CreatedAt:      now,
						ToolMessage: []*conversationmodel.ToolMessageView{
							{
								Id:        "tool-msg-1",
								CreatedAt: now,
								ToolCall: &conversationmodel.ToolCallView{
									OpId:            "op-1",
									ToolName:        "llm_agents-list",
									RequestPayload:  &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr("{}")},
									ResponsePayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr(gzipString(t, body)), Compression: "gzip"},
								},
							},
						},
					},
				},
			},
		},
	}

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: turnID})
	result, err := service.buildToolExecutions(ctx, &QueryInput{}, conv, agentmdl.ToolCallExposure("turn"))
	require.NoError(t, err)
	require.Len(t, result.Calls, 1)
	require.Equal(t, body, result.Calls[0].Result)
	require.Equal(t, "llm_agents-list", result.Calls[0].Name)
}
