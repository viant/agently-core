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
				AssistantText: "Opened the order summary window.",
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
		AssistantText: "Opened the order summary window.",
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
			AdOrderID []int `json:"AdOrderId"`
		} `json:"parameters"`
	}
	value := payload{}
	value.Parameters.AdOrderID = []int{2656980}
	got := normalizeInterfaceMap(value.Parameters)
	require.Equal(t, map[string]interface{}{
		"AdOrderId": []interface{}{float64(2656980)},
	}, got)
}
