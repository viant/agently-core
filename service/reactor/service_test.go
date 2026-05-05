package reactor

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/jsonutil"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	"github.com/viant/agently-core/protocol/agent/plan"
	"github.com/viant/agently-core/protocol/binding"
	memory "github.com/viant/agently-core/runtime/requestctx"
	core2 "github.com/viant/agently-core/service/core"
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
	_ = jsonutil.EnsureJSONResponse(ctx, elicitationJSON, expected)
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

	ok, err := service.extendPlanFromResponse(ctx, nil, genOutput, aPlan)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, aPlan.Elicitation)
	assert.Equal(t, "Provide repository path", aPlan.Elicitation.Message)
}

func TestService_extendPlanFromContent_PrefersResponseContentForElicitation(t *testing.T) {
	ctx := context.Background()
	service := &Service{}
	aPlan := plan.New()
	genOutput := &core2.GenerateOutput{
		Content: `{
 "type": "elicitation",
 "message": "Please provide your favorite color so I can describe it in3 sentences.",
 "requestedSchema": "type": "object",
 "properties": "favoriteColor": "type": "string" }
 },
 "required": ["favoriteColor"]
 }
}`,
		Response: &llm.GenerateResponse{
			Choices: []llm.Choice{{
				Index: 0,
				Message: llm.Message{
					Role:    llm.RoleAssistant,
					Content: `{"type":"elicitation","message":"Please provide your favorite color so I can describe it in 3 sentences.","requestedSchema":{"type":"object","properties":{"favoriteColor":{"type":"string"}},"required":["favoriteColor"]}}`,
				},
				FinishReason: "stop",
			}},
		},
	}

	err := service.extendPlanFromContent(ctx, genOutput, aPlan)
	require.NoError(t, err)
	require.NotNil(t, aPlan.Elicitation)
	assert.Equal(t, "Please provide your favorite color so I can describe it in 3 sentences.", aPlan.Elicitation.Message)
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

func TestService_extendPlanFromResponse_RejectsUnresolvedMessageShowContinuation(t *testing.T) {
	ctx := context.Background()
	service := &Service{}
	aPlan := plan.New()
	genInput := &core2.GenerateInput{
		Binding: &binding.Binding{
			History: binding.History{
				Current: &binding.Turn{
					Messages: []*binding.Message{
						{
							Kind:     binding.MessageKindToolResult,
							Role:     string(llm.RoleAssistant),
							ToolName: "message-show",
							ToolOpID: "call-1",
							ToolArgs: map[string]interface{}{
								"messageId": "source-msg",
								"byteRange": map[string]int{"from": 57600, "to": 65912},
							},
							Content: `overflow: true
messageId: source-msg
nextArgs:
  messageId: source-msg
  byteRange:
    from: 57600
    to: 65912
nextRange:
  bytes:
    offset: 57600
    length: 8312
hasMore: true
useToolToSeeMore: message-show
content: |
  partial body`,
						},
					},
				},
			},
		},
	}
	genOutput := &core2.GenerateOutput{
		Content: "Here is my answer without the required continuation.",
		Response: &llm.GenerateResponse{
			Choices: []llm.Choice{{
				Index: 0,
				Message: llm.Message{
					Role:    llm.RoleAssistant,
					Content: "Here is my answer without the required continuation.",
				},
				FinishReason: "stop",
			}},
		},
	}

	ok, err := service.extendPlanFromResponse(ctx, genInput, genOutput, aPlan)
	require.ErrorIs(t, err, errPendingToolContinuation)
	require.False(t, ok)
	require.Empty(t, aPlan.Steps)
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

func TestMergeStreamContent(t *testing.T) {
	cases := []struct {
		name     string
		current  string
		incoming string
		expect   string
	}{
		{
			name:     "starts with incoming when empty",
			current:  "",
			incoming: "Hello",
			expect:   "Hello",
		},
		{
			name:     "dedupes exact repeat",
			current:  "Hello",
			incoming: "Hello",
			expect:   "Hello",
		},
		{
			name:     "promotes cumulative snapshot",
			current:  "Hello",
			incoming: "Hello world",
			expect:   "Hello world",
		},
		{
			name:     "ignores older prefix snapshot",
			current:  "Hello world",
			incoming: "Hello",
			expect:   "Hello world",
		},
		{
			name:     "dedupes same content with formatting differences",
			current:  "```json{\"values\":{\"HOME\":\"/Users/awitas\"}}\n```",
			incoming: "```json\n{\"values\":{\"HOME\":\"/Users/awitas\"}}\n```",
			expect:   "```json{\"values\":{\"HOME\":\"/Users/awitas\"}}\n```",
		},
		{
			name:     "appends unrelated chunk",
			current:  "Hello ",
			incoming: "world",
			expect:   "Hello world",
		},
		{
			name:     "preserves whitespace-only streamed chunk",
			current:  "Hello",
			incoming: " ",
			expect:   "Hello ",
		},
		{
			name:     "preserves trailing space in cumulative snapshot",
			current:  "Hello",
			incoming: "Hello ",
			expect:   "Hello ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, mergeStreamContent(tc.current, tc.incoming))
		})
	}
}

func TestService_handleTypedStreamEvent_TurnCompletedUsesFinalResponseContent(t *testing.T) {
	ctx := context.Background()
	service := &Service{}
	genOutput := &core2.GenerateOutput{
		Content: `{
 "type": "elicitation",
 "message": "Please provide your favorite color so I can describe it in3 sentences."
}`,
	}
	aPlan := plan.New()
	nextStepIdx := 0
	var wg sync.WaitGroup
	var mux sync.Mutex

	event := &llm.StreamEvent{
		Kind: llm.StreamEventTurnCompleted,
		Response: &llm.GenerateResponse{
			Choices: []llm.Choice{
				{
					Index: 0,
					Message: llm.Message{
						Role:    llm.RoleAssistant,
						Content: `I will call python_user_visible.exec to create a PDF file containing the 20-word description and save it to /mnt/data/mouse_description.pdf.`,
					},
					FinishReason: "",
				},
				{
					Index: 1,
					Message: llm.Message{
						Role:    llm.RoleAssistant,
						Content: `I created the PDF with the 20-word description.` + "\n\n" + `Here is the sentence used:` + "\n" + `A small nocturnal rodent with soft gray fur, long whiskers, keen hearing, curious nature, quick agile movements, and sharp teeth.` + "\n\n" + `[Download the PDF](sandbox:/mnt/data/mouse_description.pdf)`,
					},
					FinishReason: "stop",
				},
			},
		},
	}

	err := service.handleTypedStreamEvent(ctx, event, &mux, genOutput, aPlan, &nextStepIdx, &wg, nil)
	require.NoError(t, err)
	require.NotNil(t, genOutput.Response)
	assert.Equal(t, event.Response, genOutput.Response)
	assert.Equal(t, event.Response.Choices[1].Message.Content, genOutput.Content)
}

func TestService_handleTypedStreamEvent_TextDeltaPreservesWhitespaceOnlyChunks(t *testing.T) {
	ctx := context.Background()
	service := &Service{}
	genOutput := &core2.GenerateOutput{}
	aPlan := plan.New()
	nextStepIdx := 0
	var wg sync.WaitGroup
	var mux sync.Mutex

	for _, delta := range []string{"Hello", " ", "world"} {
		err := service.handleTypedStreamEvent(ctx, &llm.StreamEvent{
			Kind:  llm.StreamEventTextDelta,
			Delta: delta,
		}, &mux, genOutput, aPlan, &nextStepIdx, &wg, nil)
		require.NoError(t, err)
	}

	assert.Equal(t, "Hello world", genOutput.Content)
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
	require.NotNil(t, msg.Narration)
	require.NotNil(t, msg.RawContent)
	assert.EqualValues(t, "I will use system_os-getEnv.", *msg.Content)
	assert.EqualValues(t, "I will use system_os-getEnv.", *msg.Narration)
	assert.EqualValues(t, "I will use system_os-getEnv.", *msg.RawContent)
	assert.EqualValues(t, 1, msg.Interim)
}
