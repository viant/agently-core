package sdk

import (
	"testing"

	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

func TestCollectToolChildren_FallsBackToParentWithoutIteration(t *testing.T) {
	iteration := 1
	parentID := "assistant-1"
	turn := &convstore.Turn{
		Message: []*agconv.MessageView{
			{
				Id:        parentID,
				Iteration: &iteration,
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:        "tool-queued",
						Iteration: &iteration,
						ToolCall: &agconv.ToolCallView{
							OpId:     "tool-call-1",
							ToolName: "system/os/getEnv",
							Status:   "queued",
						},
					},
					{
						Id: "tool-completed",
						ToolCall: &agconv.ToolCallView{
							OpId:     "tool-call-1",
							ToolName: "system/os/getEnv",
							Status:   "completed",
						},
					},
				},
			},
		},
	}

	indexed := indexToolMessagesByParentAndIteration(turn)
	toolMessages, toolCalls := collectToolChildren(turn, turn.Message[0], indexed)

	require.Len(t, toolMessages, 2)
	require.Len(t, toolCalls, 2)
	ids := []string{toolMessages[0].Id, toolMessages[1].Id}
	require.Contains(t, ids, "tool-queued")
	require.Contains(t, ids, "tool-completed")
}

func TestBuildExecutionPages_PrefersLatestToolStepStatus(t *testing.T) {
	iteration := 1
	parentID := "assistant-1"
	turn := &convstore.Turn{
		Message: []*agconv.MessageView{
			{
				Id:        parentID,
				Role:      "assistant",
				Iteration: &iteration,
				ModelCall: &agconv.ModelCallView{
					MessageId: parentID,
					Status:    "completed",
					Provider:  "openai",
					Model:     "gpt-5-mini",
				},
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:        "tool-queued",
						Iteration: &iteration,
						ToolCall: &agconv.ToolCallView{
							OpId:     "tool-call-1",
							ToolName: "system/os/getEnv",
							Status:   "queued",
						},
					},
					{
						Id: "tool-completed",
						ToolCall: &agconv.ToolCallView{
							OpId:     "tool-call-1:approved",
							ToolName: "system/os/getEnv",
							Status:   "completed",
						},
					},
				},
			},
		},
	}

	ts := &TurnState{}
	pages := buildExecutionPages(ts, turn)
	require.Len(t, pages, 1)
	require.Len(t, pages[0].ToolSteps, 1)
	require.Equal(t, "completed", pages[0].ToolSteps[0].Status)
}
