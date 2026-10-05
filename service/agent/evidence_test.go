package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/runtime/evidence"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

type rejectAssistantEvidence struct{}

func (rejectAssistantEvidence) Content(context.Context, string) (string, error) {
	return "", fmt.Errorf("wrong source date")
}
func (rejectAssistantEvidence) Fence(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("wrong source date")
}
func (rejectAssistantEvidence) Stream(context.Context, string, string, bool) (string, error) {
	return "", fmt.Errorf("wrong source date")
}

func TestFinalAssistantEvidenceRejectsBeforeNewOrExistingMessageWrite(t *testing.T) {
	client := convmem.New()
	service := &Service{conversation: client}
	turn := requestctx.TurnMeta{ConversationID: "conversation", TurnID: "turn"}
	ctx := evidence.WithPublication(context.Background(), rejectAssistantEvidence{})
	conversation := apiconv.NewConversation()
	conversation.SetId(turn.ConversationID)
	require.NoError(t, client.PatchConversations(ctx, conversation))
	existing := apiconv.NewMessage()
	existing.SetId("existing")
	existing.SetConversationID(turn.ConversationID)
	existing.SetTurnID(turn.TurnID)
	existing.SetRole("assistant")
	existing.SetContent("Safe previous text")
	require.NoError(t, client.PatchMessage(ctx, existing))
	for _, id := range []string{"", "existing"} {
		err := service.persistFinalAssistantMessage(ctx, &turn, id, "```forge-data\n{}\n```")
		require.True(t, evidence.IsRejection(err), "%v", err)
	}
	saved, err := client.GetMessage(ctx, "existing")
	require.NoError(t, err)
	require.Equal(t, "Safe previous text", saved.GetContentPreferContent())
}

type captureEvidenceFactory struct{ input evidence.Input }

func (f *captureEvidenceFactory) Capture(_ context.Context, input evidence.Input) (evidence.Pending, error) {
	f.input = input
	return nil, nil
}
func (f *captureEvidenceFactory) Restore(ctx context.Context, _ evidence.Turn) (context.Context, error) {
	return ctx, nil
}

func TestQueryEvidenceCaptureOwnsOriginalContextBytes(t *testing.T) {
	factory := &captureEvidenceFactory{}
	service := &Service{}
	WithEvidenceFactory(factory)(service)
	client := map[string]interface{}{"forecastIntent": map[string]interface{}{"version": 1}}
	input := &QueryInput{Context: map[string]interface{}{"client": client}}
	at := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	_, err := service.captureEvidence(context.Background(), input, at)
	require.NoError(t, err)
	input.Context["client"] = map[string]interface{}{"forecastIntent": "routing rewrite"}
	client["forecastIntent"] = "nested mutation"
	require.JSONEq(t, `{"client":{"forecastIntent":{"version":1}}}`, string(factory.input.Context))
	require.Equal(t, at, factory.input.ReceivedAt)
	require.False(t, factory.input.Nested)
	input.ParentConversationID = "parent"
	_, err = service.captureEvidence(context.Background(), input, at)
	require.NoError(t, err)
	require.True(t, factory.input.Nested)
}
