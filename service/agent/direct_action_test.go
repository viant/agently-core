package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	intakesvc "github.com/viant/agently-core/service/intake"
)

func TestDirectActionFromContext(t *testing.T) {
	ctx := map[string]any{
		intakesvc.ContextKey: &intakesvc.Context{
			DirectAction: intakesvc.DirectActionContext{
				ToolName:      "ui/view:open",
				Input:         map[string]any{"id": "order"},
				AssistantText: "Opened the details window.",
			},
		},
	}
	got := directActionFromContext(ctx)
	if got == nil {
		t.Fatalf("expected direct action")
	}
	if got.ToolName != "ui/view:open" {
		t.Fatalf("toolName = %q", got.ToolName)
	}
}

func TestValidateDirectAction(t *testing.T) {
	ok := &intakesvc.DirectActionContext{
		ToolName:      "ui/view:open",
		Input:         map[string]any{"id": "order"},
		InputJSON:     `{"id":"order"}`,
		AssistantText: "Opened the details window.",
	}
	if err := validateDirectAction(ok); err != nil {
		t.Fatalf("expected valid direct action, got %v", err)
	}
	okRead := &intakesvc.DirectActionContext{
		ToolName:      "resources/read",
		Input:         map[string]any{"path": "/tmp/recovery.md", "rootId": "local"},
		InputJSON:     `{"path":"/tmp/recovery.md","rootId":"local"}`,
		AssistantText: "Opening the requested file for review.",
	}
	if err := validateDirectAction(okRead); err != nil {
		t.Fatalf("expected resources/read direct action to be valid, got %v", err)
	}
	bad := &intakesvc.DirectActionContext{
		ToolName:      "system/exec",
		Input:         map[string]any{"cmd": "whoami"},
		AssistantText: "no",
	}
	if err := validateDirectAction(bad); err != nil {
		t.Fatalf("expected structural validation to pass, got %v", err)
	}
	missingViewID := &intakesvc.DirectActionContext{
		ToolName:      "ui/view:open",
		Input:         map[string]any{"AdLineId": "7288336"},
		AssistantText: "Opening forecast view.",
	}
	require.ErrorContains(t, validateDirectAction(missingViewID), "input.id is required")
}

func TestAuthorizeDirectAction_UsesIntakeToolItemsAndBundles(t *testing.T) {
	svc := &Service{
		registry: &fakeRegistry{defs: []llm.ToolDefinition{
			{Name: "resources/read"},
			{Name: "ui/view:open"},
			{Name: "system/exec:execute"},
		}},
		toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{
				{
					ID: "ui-direct",
					Match: []llm.Tool{
						{Name: "ui/view:open"},
					},
				},
			}, nil
		},
	}
	input := &QueryInput{
		Agent: &agentmdl.Agent{
			Intake: agentmdl.Intake{
				Tool: agentmdl.Tool{
					Bundles: []string{"ui-direct"},
					Items:   []*llm.Tool{{Name: "resources/read"}},
				},
			},
		},
	}
	require.NoError(t, svc.authorizeDirectAction(context.Background(), input, &intakesvc.DirectActionContext{
		ToolName:      "resources/read",
		Input:         map[string]any{"path": "/tmp/recovery.md"},
		AssistantText: "open",
	}))
	require.NoError(t, svc.authorizeDirectAction(context.Background(), input, &intakesvc.DirectActionContext{
		ToolName:      "ui/view:open",
		Input:         map[string]any{"id": "order"},
		AssistantText: "open",
	}))
	require.Error(t, svc.authorizeDirectAction(context.Background(), input, &intakesvc.DirectActionContext{
		ToolName:      "system/exec:execute",
		Input:         map[string]any{"cmd": "pwd"},
		AssistantText: "open",
	}))
}

func TestConversationMetadata_PreservesUnknownWorkspaceKeysInExtra(t *testing.T) {
	raw := `{"workspace":{"windowId":"order_123","windowKey":"order"},"workspaceState":{"selectedWindowId":"order_123","windows":[{"windowId":"order_123","windowKey":"order"}]}}`
	var decoded ConversationMetadata
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	require.Contains(t, decoded.Extra, "workspace")
	require.Contains(t, decoded.Extra, "workspaceState")
	encoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	require.JSONEq(t, raw, string(encoded))
}

func TestNormalizeInterfaceMap(t *testing.T) {
	type payload struct {
		Parameters struct {
			RecordID []int `json:"RecordId"`
		} `json:"parameters"`
	}
	value := payload{}
	value.Parameters.RecordID = []int{2656980}
	got := normalizeInterfaceMap(value.Parameters)
	require.Equal(t, map[string]interface{}{
		"RecordId": []interface{}{float64(2656980)},
	}, got)
}

func TestAnnotateDirectActionExecution(t *testing.T) {
	svc := &Service{}
	input := &QueryInput{
		ConversationID: "conv-1",
		MessageID:      "turn-1",
		Context: map[string]any{
			intakesvc.ContextKey: &intakesvc.Context{
				DirectAction: intakesvc.DirectActionContext{
					ToolName:      "ui/view:open",
					Input:         map[string]any{"id": "order"},
					AssistantText: "Opened order.",
				},
			},
		},
	}
	action := directActionFromContext(input.Context)
	require.NotNil(t, action)
	result := `{"ok":true,"windowId":"order_2656980"}`
	svc.annotateDirectActionExecution(input, action, &result)
	tc := intakesvc.FromContext(input.Context)
	require.NotNil(t, tc)
	require.True(t, tc.DirectActionExecution.Executed)
	require.Equal(t, "ui/view:open", tc.DirectActionExecution.ToolName)
	require.Equal(t, map[string]interface{}{"ok": true, "windowId": "order_2656980"}, tc.DirectActionExecution.Result)
	require.Equal(t, `{"ok":true,"windowId":"order_2656980"}`, tc.DirectActionExecution.ResultText)
	require.Equal(t, true, input.Context["intake.directActionExecuted"])
	require.Equal(t, "ui/view:open", input.Context["intake.directActionTool"])
}

func TestPublishDirectActionAssistantMessage_WritesCompletedStatus(t *testing.T) {
	recorder := &intakeRecordingConvClient{}
	svc := &Service{conversation: recorder}
	input := &QueryInput{
		ConversationID: "conv-1",
		MessageID:      "turn-1",
	}
	err := svc.publishDirectActionAssistantMessage(context.Background(), input, "Opened the details window.")
	require.NoError(t, err)
	require.NotNil(t, recorder.lastMessage)
	require.NotNil(t, recorder.lastMessage.Status)
	require.Equal(t, "completed", *recorder.lastMessage.Status)
	require.NotNil(t, recorder.lastMessage.Content)
	require.Equal(t, "Opened the details window.", *recorder.lastMessage.Content)
	require.True(t, recorder.lastMessageAdd)
}

func TestMaybeRunDirectAction_InvalidActionFallsThrough(t *testing.T) {
	svc := &Service{}
	input := &QueryInput{
		ConversationID: "conv-1",
		MessageID:      "turn-1",
		Context: map[string]any{
			intakesvc.ContextKey: &intakesvc.Context{
				Prompting: intakesvc.PromptingContext{
					SuggestedProfileID: "workspace_console",
				},
				DirectAction: intakesvc.DirectActionContext{
					ToolName:      "ui/view:open",
					Input:         map[string]any{"AdLineId": "7288336"},
					AssistantText: "Opening Review window.",
				},
			},
		},
	}
	output := &QueryOutput{}

	handled, err := svc.maybeRunDirectAction(context.Background(), input, output)
	require.NoError(t, err)
	require.False(t, handled)

	tc := intakesvc.FromContext(input.Context)
	require.NotNil(t, tc)
	require.Empty(t, tc.DirectAction.ToolName)
	require.Equal(t, "workspace_console", tc.Prompting.SuggestedProfileID)
}
