package message

import (
	"context"
	"testing"

	"github.com/viant/agently-core/protocol/agent/execution"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type fakeAskUserElicitor struct {
	req *execution.Elicitation
}

func (f *fakeAskUserElicitor) Elicit(_ context.Context, _ *runtimerequestctx.TurnMeta, _ string, req *execution.Elicitation) (string, string, map[string]interface{}, error) {
	f.req = req
	return "msg-1", "submit", map[string]interface{}{"ok": true}, nil
}

func TestAskUser_UsesRequestedSchemaWhenProvided(t *testing.T) {
	fake := &fakeAskUserElicitor{}
	svc := &Service{elicitor: fake}
	ctx := runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})

	in := &AskUserInput{
		Message: "Review the selected recommendations.",
		RequestedSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"intent": map[string]interface{}{
					"type":    "string",
					"default": "submit_selected",
				},
			},
			"required": []string{"intent"},
		},
	}
	out := &AskUserOutput{}

	if err := svc.askUser(ctx, in, out); err != nil {
		t.Fatalf("askUser() error = %v", err)
	}
	if fake.req == nil {
		t.Fatalf("elicitor did not receive request")
	}
	if fake.req.Message != "Review the selected recommendations." {
		t.Fatalf("message = %q", fake.req.Message)
	}
	if got := fake.req.RequestedSchema.Type; got != "object" {
		t.Fatalf("requested schema type = %q", got)
	}
	if _, ok := fake.req.RequestedSchema.Properties["intent"]; !ok {
		t.Fatalf("requested schema missing intent property")
	}
	if out.Action != "submit" {
		t.Fatalf("action = %q", out.Action)
	}
	if got, _ := out.Payload["ok"].(bool); !got {
		t.Fatalf("payload = %#v", out.Payload)
	}
}
