package message

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/evidence"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type denyEvidencePublication struct{}

func (denyEvidencePublication) Content(context.Context, string) (string, error) {
	return "", fmt.Errorf("missing forecast bindings")
}
func (denyEvidencePublication) Fence(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("missing forecast bindings")
}
func (denyEvidencePublication) Stream(context.Context, string, string, bool) (string, error) {
	return "", fmt.Errorf("missing forecast bindings")
}

func TestMessageAddCannotBypassEvidenceWithInterimFlag(t *testing.T) {
	for _, interim := range []bool{false, true} {
		conv := &addFakeConv{}
		service := New(conv)
		ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conversation", TurnID: "turn", ParentMessageID: "starter"})
		ctx = evidence.WithPublication(ctx, denyEvidencePublication{})
		err := service.add(ctx, &AddInput{Content: "```forge-data\n{}\n```", Interim: &interim}, &AddOutput{})
		require.True(t, evidence.IsRejection(err), "%v", err)
		require.Empty(t, conv.patchedMessages)
		require.Empty(t, conv.patchedConvs)
	}
}
