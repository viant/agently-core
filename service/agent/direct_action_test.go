package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
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

func TestConversationMetadata_RoundTripsWorkspace(t *testing.T) {
	meta := ConversationMetadata{
		Workspace: &WorkspaceWindowMetadata{
			WindowID:     "order_123",
			WindowKey:    "order",
			WindowTitle:  "Order Summary",
			Presentation: "hosted",
			Region:       "chat.top",
			ParentKey:    "chat/new",
			InTab:        true,
			Parameters: map[string]interface{}{
				"AdOrderId": []interface{}{2656980},
			},
		},
	}
	data, err := json.Marshal(meta)
	require.NoError(t, err)
	var decoded ConversationMetadata
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotNil(t, decoded.Workspace)
	require.Equal(t, "order_123", decoded.Workspace.WindowID)
	require.Equal(t, "order", decoded.Workspace.WindowKey)
	require.Equal(t, "Order Summary", decoded.Workspace.WindowTitle)
	require.Equal(t, "hosted", decoded.Workspace.Presentation)
	require.Equal(t, "chat.top", decoded.Workspace.Region)
	require.Equal(t, "chat/new", decoded.Workspace.ParentKey)
	require.True(t, decoded.Workspace.InTab)
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

type recordingConversationClient struct {
	conv      *apiconv.Conversation
	lastPatch *apiconv.MutableConversation
}

func (c *recordingConversationClient) GetConversation(ctx context.Context, id string, options ...apiconv.Option) (*apiconv.Conversation, error) {
	return c.conv, nil
}
func (c *recordingConversationClient) GetConversations(ctx context.Context, input *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}
func (c *recordingConversationClient) PatchConversations(ctx context.Context, conversations *apiconv.MutableConversation) error {
	c.lastPatch = conversations
	if c.conv == nil {
		now := time.Now()
		c.conv = &apiconv.Conversation{Id: conversations.Id, CreatedAt: now, UpdatedAt: &now, LastActivity: &now}
	}
	if conversations.Has != nil && conversations.Has.Metadata {
		c.conv.Metadata = conversations.Metadata
	}
	return nil
}
func (c *recordingConversationClient) GetPayload(ctx context.Context, id string) (*apiconv.Payload, error) {
	return nil, nil
}
func (c *recordingConversationClient) PatchPayload(ctx context.Context, payload *apiconv.MutablePayload) error {
	return nil
}
func (c *recordingConversationClient) PatchMessage(ctx context.Context, message *apiconv.MutableMessage) error {
	return nil
}
func (c *recordingConversationClient) GetMessage(ctx context.Context, id string, options ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (c *recordingConversationClient) GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string) (*apiconv.Message, error) {
	return nil, nil
}
func (c *recordingConversationClient) PatchModelCall(ctx context.Context, modelCall *apiconv.MutableModelCall) error {
	return nil
}
func (c *recordingConversationClient) PatchToolCall(ctx context.Context, toolCall *apiconv.MutableToolCall) error {
	return nil
}
func (c *recordingConversationClient) PatchTurn(ctx context.Context, turn *apiconv.MutableTurn) error {
	return nil
}
func (c *recordingConversationClient) DeleteConversation(ctx context.Context, id string) error {
	return nil
}
func (c *recordingConversationClient) DeleteMessage(ctx context.Context, conversationID, messageID string) error {
	return nil
}

func TestPersistDirectActionWorkspaceState_PatchesConversationMetadata(t *testing.T) {
	now := time.Now()
	client := &recordingConversationClient{
		conv: &apiconv.Conversation{
			Id:           "conv-1",
			CreatedAt:    now,
			UpdatedAt:    &now,
			LastActivity: &now,
		},
	}
	svc := &Service{conversation: client}
	err := svc.persistDirectActionWorkspaceState(context.Background(), &QueryInput{ConversationID: "conv-1"}, "ui/view:open", map[string]interface{}{
		"id": "order",
		"parameters": struct {
			AdOrderID []int `json:"AdOrderId"`
		}{AdOrderID: []int{2656980}},
	}, `{"windowId":"order_1527048368","windowKey":"order"}`)
	require.NoError(t, err)
	require.NotNil(t, client.lastPatch)
	require.NotNil(t, client.lastPatch.Metadata)
	var meta ConversationMetadata
	require.NoError(t, json.Unmarshal([]byte(*client.lastPatch.Metadata), &meta))
	require.NotNil(t, meta.Workspace)
	require.Equal(t, "order_1527048368", meta.Workspace.WindowID)
	require.Equal(t, "order", meta.Workspace.WindowKey)
	require.Equal(t, "hosted", meta.Workspace.Presentation)
	require.Equal(t, "chat.top", meta.Workspace.Region)
}
