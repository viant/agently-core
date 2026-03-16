package reactor

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	"github.com/viant/agently-core/protocol/agent/plan"
	"github.com/viant/agently-core/runtime/memory"
	core2 "github.com/viant/agently-core/service/core"
	executil "github.com/viant/agently-core/service/shared/executil"
)

// dd test for extendPlanFromContent: parses elicitation JSON embedded in content.
func TestService_extendPlanFromContent_DD(t *testing.T) {
	ctx := context.Background()
	s := &Service{}

	type testCase struct {
		name     string
		content  string
		expected *plan.Elicitation
	}

	elicitationJSON := `{
"type": "elicitation",
"message": "To find out how many tables are in your ci_ads database, I need the connection details for that database so I can access it.\nPlease provide the following information:",
"requestedSchema": {
"type": "object",
"properties": {
"name": { "type": "string", "description": "Connector name you’d like to assign (e.g., ci_ads_conn)" },
"driver": { "type": "string", "enum": ["postgres", "mysql", "bigquery"], "description": "Database type/driver" },
"host": { "type": "string", "description": "Hostname or IP of the database server" },
"port": { "type": integer, "description": "Port number the database listens on" },
"db": { "type": "string", "description": "Database name (ci_ads)" }
},
"required": ["name", "driver", "host", "port", "db"]
}
}`

	expected := &plan.Elicitation{}
	_ = executil.EnsureJSONResponse(ctx, elicitationJSON, expected)
	if expected.IsEmpty() {
		expected = nil
	}

	cases := []testCase{
		{
			name:     "elicitation from content",
			content:  elicitationJSON,
			expected: expected,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := &core2.GenerateOutput{Content: tc.content}
			aPlan := plan.New()
			err := s.extendPlanFromContent(ctx, out, aPlan)
			assert.NoError(t, err)
			assert.EqualValues(t, tc.expected, aPlan.Elicitation)
		})
	}
}

func TestService_extendPlanFromResponse_ElicitationOnlyIsNotEmpty(t *testing.T) {
	ctx := context.Background()
	service := &Service{}
	aPlan := plan.New()
	genOutput := &core2.GenerateOutput{
		Content: `{
  "type": "elicitation",
  "message": "Provide repository path",
  "requestedSchema": {
    "type": "object",
    "properties": {
      "workdir": { "type": "string" }
    },
    "required": ["workdir"]
  }
}`,
		Response: &llm.GenerateResponse{
			Choices: []llm.Choice{{
				Index: 0,
				Message: llm.Message{
					Role:    llm.RoleAssistant,
					Content: `{"type":"elicitation","message":"Provide repository path","requestedSchema":{"type":"object","properties":{"workdir":{"type":"string"}},"required":["workdir"]}}`,
				},
				FinishReason: "stop",
			}},
		},
	}

	ok, err := service.extendPlanFromResponse(ctx, genOutput, aPlan)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, aPlan.Elicitation)
	assert.Equal(t, "Provide repository path", aPlan.Elicitation.Message)
}

func TestService_extendPlanWithToolCalls_SynthesizesReason(t *testing.T) {
	service := &Service{}
	aPlan := plan.New()
	choice := &llm.Choice{
		Message: llm.Message{
			Role:      llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "resources-roots"}},
		},
		FinishReason: "tool_calls",
	}

	service.extendPlanWithToolCalls("resp-1", choice, aPlan)

	require.Len(t, aPlan.Steps, 1)
	assert.EqualValues(t, "", aPlan.Steps[0].Reason)
}

func TestService_extendPlanWithToolCalls_UsesDeterministicFallbackIDForStreamingDeltas(t *testing.T) {
	service := &Service{}
	aPlan := plan.New()
	choice1 := &llm.Choice{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				Name: "system_patch-apply",
				Function: llm.FunctionCall{
					Name:      "system_patch-apply",
					Arguments: "{\"patch\":\"*** Begin Patch",
				},
			}},
		},
		FinishReason: "tool_calls",
	}
	choice2 := &llm.Choice{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				Name: "system_patch-apply",
				Function: llm.FunctionCall{
					Name:      "system_patch-apply",
					Arguments: "{\"patch\":\"*** Begin Patch\\n*** Add File: add_test.go\\n+package main\\n\",\"workdir\":\"/tmp/change-repo2\"}",
				},
			}},
		},
		FinishReason: "tool_calls",
	}

	service.extendPlanWithToolCalls("resp-stream", choice1, aPlan)
	service.extendPlanWithToolCalls("resp-stream", choice2, aPlan)

	require.Len(t, aPlan.Steps, 1)
	assert.Equal(t, "resp-stream:0:system_patch-apply", aPlan.Steps[0].ID)
	require.NotNil(t, aPlan.Steps[0].Args)
	assert.Equal(t, "/tmp/change-repo2", aPlan.Steps[0].Args["workdir"])
}

// patchCountingClient wraps a conversation client and counts PatchMessage calls.
type patchCountingClient struct {
	apiconv.Client
	patchCount int32
}

func (c *patchCountingClient) PatchMessage(ctx context.Context, msg *apiconv.MutableMessage) error {
	atomic.AddInt32(&c.patchCount, 1)
	return c.Client.PatchMessage(ctx, msg)
}

func (c *patchCountingClient) PatchCount() int32 {
	return atomic.LoadInt32(&c.patchCount)
}

// TestService_patchStreamingToolPreamble_SkipsDuplicatePatch verifies that
// calling patchStreamingToolPreamble with the same preamble text multiple times
// only issues one PatchMessage call (deduplication).
func TestService_patchStreamingToolPreamble_SkipsDuplicatePatch(t *testing.T) {
	inner := convmem.New()
	client := &patchCountingClient{Client: inner}

	base := memory.WithConversationID(context.Background(), "conv-dedup")
	seed := &convw.Conversation{Has: &convw.ConversationHas{}}
	seed.SetId("conv-dedup")
	seed.SetStatus("")
	require.NoError(t, inner.PatchConversations(base, seed))

	ctx := memory.WithTurnMeta(base, memory.TurnMeta{ConversationID: "conv-dedup", TurnID: "turn-1"})
	ctx = context.WithValue(ctx, memory.ModelMessageIDKey, "msg-dedup")

	seedMsg := apiconv.NewMessage()
	seedMsg.SetId("msg-dedup")
	seedMsg.SetConversationID("conv-dedup")
	seedMsg.SetTurnID("turn-1")
	seedMsg.SetInterim(1)
	require.NoError(t, inner.PatchMessage(ctx, seedMsg))

	service := &Service{convClient: client}
	choice := llm.Choice{
		Message: llm.Message{
			Content:   "I will use system_os-getEnv.",
			Role:      llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "system/os:getEnv"}},
		},
		FinishReason: "tool_calls",
	}

	// First call should patch
	service.patchStreamingToolPreamble(ctx, choice)
	assert.EqualValues(t, 1, client.PatchCount(), "first preamble patch should go through")

	// Second call with same preamble should be skipped
	service.patchStreamingToolPreamble(ctx, choice)
	assert.EqualValues(t, 1, client.PatchCount(), "duplicate preamble should be skipped")

	// Third call with different preamble should patch
	choice2 := llm.Choice{
		Message: llm.Message{
			Content: "I will use system_exec-execute instead.",
			Role:    llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "system/os:getEnv"},
				{ID: "call_2", Name: "system/exec:execute"},
			},
		},
		FinishReason: "tool_calls",
	}
	service.patchStreamingToolPreamble(ctx, choice2)
	assert.EqualValues(t, 2, client.PatchCount(), "different preamble should patch")
}

func TestService_patchStreamingToolPreamble_PatchesAssistantMessage(t *testing.T) {
	client := convmem.New()
	base := memory.WithConversationID(context.Background(), "conv-1")
	seed := &convw.Conversation{Has: &convw.ConversationHas{}}
	seed.SetId("conv-1")
	seed.SetStatus("")
	require.NoError(t, client.PatchConversations(base, seed))

	ctx := memory.WithTurnMeta(base, memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	ctx = context.WithValue(ctx, memory.ModelMessageIDKey, "msg-1")

	seedMsg := apiconv.NewMessage()
	seedMsg.SetId("msg-1")
	seedMsg.SetConversationID("conv-1")
	seedMsg.SetTurnID("turn-1")
	seedMsg.SetInterim(1)
	require.NoError(t, client.PatchMessage(ctx, seedMsg))

	service := &Service{convClient: client}
	service.patchStreamingToolPreamble(ctx, llm.Choice{
		Message: llm.Message{
			Content:   "I will use system_os-getEnv.",
			Role:      llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "system/os:getEnv"}},
		},
		FinishReason: "tool_calls",
	})

	msg, err := client.GetMessage(context.Background(), "msg-1")
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.NotNil(t, msg.Content)
	require.NotNil(t, msg.Preamble)
	require.NotNil(t, msg.RawContent)
	assert.EqualValues(t, "I will use system_os-getEnv.", *msg.Content)
	assert.EqualValues(t, "I will use system_os-getEnv.", *msg.Preamble)
	assert.EqualValues(t, "I will use system_os-getEnv.", *msg.RawContent)
	assert.EqualValues(t, 1, msg.Interim)
}
