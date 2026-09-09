package agent

import (
	"context"
	"testing"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

type testCancelRegistry struct {
	cancelTurnCalls         []string
	cancelConversationCalls []string
}

func (t *testCancelRegistry) Register(string, string, context.CancelFunc) {}
func (t *testCancelRegistry) Complete(string, string, context.CancelFunc) {}

func (t *testCancelRegistry) CancelTurn(turnID string) bool {
	t.cancelTurnCalls = append(t.cancelTurnCalls, turnID)
	return true
}

func (t *testCancelRegistry) CancelConversation(conversationID string) bool {
	t.cancelConversationCalls = append(t.cancelConversationCalls, conversationID)
	return true
}

func TestServiceTerminateCancelsConversation(t *testing.T) {
	reg := &testCancelRegistry{}
	svc := &Service{cancelReg: reg}

	if err := svc.Terminate(context.Background(), "conv-1"); err != nil {
		t.Fatalf("Terminate() unexpected error: %v", err)
	}

	if len(reg.cancelConversationCalls) != 1 || reg.cancelConversationCalls[0] != "conv-1" {
		t.Fatalf("expected CancelConversation to be called with conv-1, got %#v", reg.cancelConversationCalls)
	}
	if len(reg.cancelTurnCalls) != 0 {
		t.Fatalf("expected CancelTurn to remain unused, got %#v", reg.cancelTurnCalls)
	}
}

type maintenanceConversationClient struct {
	apiconv.Client
	conversation        *apiconv.Conversation
	conversationPatches []*apiconv.MutableConversation
	turnPatches         []*apiconv.MutableTurn
}

func (m *maintenanceConversationClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return m.conversation, nil
}

func (m *maintenanceConversationClient) PatchConversations(_ context.Context, patch *apiconv.MutableConversation) error {
	m.conversationPatches = append(m.conversationPatches, patch)
	return nil
}

func (m *maintenanceConversationClient) PatchTurn(_ context.Context, patch *apiconv.MutableTurn) error {
	m.turnPatches = append(m.turnPatches, patch)
	return nil
}

func TestServiceTerminatePersistsNonTerminalTurnsAsCanceled(t *testing.T) {
	client := &maintenanceConversationClient{conversation: (*apiconv.Conversation)(&agconv.ConversationView{
		Id: "conv-1",
		Transcript: []*agconv.TranscriptView{
			{Id: "running", ConversationId: "conv-1", Status: "running"},
			{Id: "queued", ConversationId: "conv-1", Status: "queued"},
			{Id: "done", ConversationId: "conv-1", Status: "succeeded"},
			{Id: "failed", ConversationId: "conv-1", Status: "failed"},
		},
	})}
	svc := &Service{conversation: client}

	if err := svc.Terminate(context.Background(), "conv-1"); err != nil {
		t.Fatalf("Terminate() unexpected error: %v", err)
	}
	if len(client.turnPatches) != 2 {
		t.Fatalf("expected running and queued turns to be patched, got %d", len(client.turnPatches))
	}
	for _, patch := range client.turnPatches {
		if patch.Status != "canceled" || patch.StatusReason == nil || *patch.StatusReason != "conversation_terminated" {
			t.Fatalf("unexpected turn patch: %#v", patch)
		}
	}
}
