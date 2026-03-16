package agent

import (
	"context"
	"testing"
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
