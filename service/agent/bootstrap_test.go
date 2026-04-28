package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	agproto "github.com/viant/agently-core/protocol/agent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

func TestRenderBootstrapToolContext_DataDriven(t *testing.T) {
	falseValue := false
	testCases := []struct {
		name        string
		call        agproto.BootstrapToolCall
		result      string
		contains    []string
		notContains []string
	}{
		{
			name: "default provenance header includes tool and args",
			call: agproto.BootstrapToolCall{
				ID:   "agent_directory",
				Tool: "llm/agents:list",
				Args: map[string]interface{}{"includeInternal": false},
			},
			result: `{"items":[]}`,
			contains: []string{
				"# Runtime Bootstrap Tool Result",
				"runtime-owned bootstrap tool call",
				"Tool: `llm/agents:list`",
				`"includeInternal": false`,
				`{"items":[]}`,
			},
		},
		{
			name: "custom header expands placeholders",
			call: agproto.BootstrapToolCall{
				ID:   "agent_directory",
				Tool: "llm/agents:list",
				Args: map[string]interface{}{"includeInternal": false},
				Inject: agproto.BootstrapInject{
					Header: "Tool {{tool}} for {{id}}\nArgs: {{args}}",
				},
			},
			result: `{"items":[]}`,
			contains: []string{
				"Tool llm/agents:list for agent_directory",
				`"includeInternal": false`,
				"## Result",
			},
			notContains: []string{
				"# Runtime Bootstrap Tool Result",
			},
		},
		{
			name: "header can be suppressed",
			call: agproto.BootstrapToolCall{
				ID:   "agent_directory",
				Tool: "llm/agents:list",
				Inject: agproto.BootstrapInject{
					IncludeHeader: &falseValue,
				},
			},
			result: `{"items":[]}`,
			contains: []string{
				"## Result",
				`{"items":[]}`,
			},
			notContains: []string{
				"# Runtime Bootstrap Tool Result",
				"Tool: `llm/agents:list`",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := renderBootstrapToolContext(testCase.call, testCase.result)
			for _, expected := range testCase.contains {
				assert.Contains(t, got, expected)
			}
			for _, unexpected := range testCase.notContains {
				assert.NotContains(t, got, unexpected)
			}
		})
	}
}

type captureBootstrapPublisher struct {
	events []*streaming.Event
}

func (c *captureBootstrapPublisher) Publish(_ context.Context, event *streaming.Event) error {
	c.events = append(c.events, event)
	return nil
}

func TestPublishBootstrapToolEvent_SetsBootstrapIdentity(t *testing.T) {
	publisher := &captureBootstrapPublisher{}
	service := &Service{streamPub: publisher}
	ctx := runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{
		ConversationID:  "conv-bootstrap",
		TurnID:          "turn-bootstrap",
		ParentMessageID: "parent-msg",
	})
	input := &QueryInput{
		ConversationID: "conv-bootstrap",
		Agent:          &agproto.Agent{Identity: agproto.Identity{ID: "steward"}},
	}

	service.publishBootstrapToolEvent(ctx, input, streaming.EventTypeToolCallStarted, "bootstrap:agents", "llm/agents:list", map[string]interface{}{"includeInternal": false}, "")

	if assert.Len(t, publisher.events, 1) {
		event := publisher.events[0]
		assert.Equal(t, streaming.EventTypeToolCallStarted, event.Type)
		assert.Equal(t, "conv-bootstrap", event.ConversationID)
		assert.Equal(t, "turn-bootstrap", event.TurnID)
		assert.Equal(t, "turn-bootstrap:bootstrap", event.PageID)
		assert.Equal(t, "bootstrap", event.ExecutionRole)
		assert.Equal(t, "bootstrap", event.Phase)
		assert.Equal(t, "systemContext", event.Mode)
		assert.Equal(t, "llm/agents:list", event.ToolName)
		assert.Equal(t, "bootstrap:agents", event.ToolCallID)
		assert.False(t, event.CreatedAt.Equal(time.Time{}))
	}
}
