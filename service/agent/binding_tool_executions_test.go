package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/runtime/memory"
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
		Transcript: []*agconv.TranscriptView{
			{
				Id: turnID,
				Message: []*agconv.MessageView{
					{
						Id:             "tool-parent-1",
						ConversationId: "conv-1",
						TurnId:         strPtr(turnID),
						Role:           "assistant",
						Type:           "tool_op",
						CreatedAt:      now,
						ToolMessage: []*agconv.ToolMessageView{
							{
								Id:        "tool-msg-1",
								CreatedAt: now,
								ToolCall: &agconv.ToolCallView{
									OpId:            "op-1",
									ToolName:        "llm_agents-list",
									RequestPayload:  &agconv.ModelCallStreamPayloadView{InlineBody: strPtr("{}")},
									ResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(gzipString(t, body)), Compression: "gzip"},
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
