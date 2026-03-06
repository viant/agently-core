package reactor

import (
	"context"
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
	assert.EqualValues(t, "Using resources-roots.", aPlan.Steps[0].Reason)
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
	assert.EqualValues(t, "Using system/os:getEnv.", *msg.Content)
	assert.EqualValues(t, "Using system/os:getEnv.", *msg.Preamble)
	assert.EqualValues(t, "Using system/os:getEnv.", *msg.RawContent)
	assert.EqualValues(t, 1, msg.Interim)
}
