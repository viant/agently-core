package modelcall

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/runtime/evidence"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type testPublication struct {
	mu     sync.Mutex
	stream *evidence.FenceStream
	reject bool
}

func (p *testPublication) Fence(_ context.Context, kind, body string) (string, error) {
	if p.reject {
		return "", fmt.Errorf("wrong date association")
	}
	return `{"verified":true}`, nil
}
func (p *testPublication) Content(ctx context.Context, content string) (string, error) {
	stream := evidence.NewFenceStream(func(kind, body string) (string, error) { return p.Fence(ctx, kind, body) })
	return stream.Push(content, true)
}
func (p *testPublication) Stream(ctx context.Context, _ string, delta string, final bool) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stream == nil {
		p.stream = evidence.NewFenceStream(func(kind, body string) (string, error) { return p.Fence(ctx, kind, body) })
	}
	return p.stream.Push(delta, final)
}

func TestRecorderEvidenceRejectsBeforeVisiblePublicationAndKeepsProviderAudit(t *testing.T) {
	t.Setenv(streamPersistModeEnv, "final")
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			client := convmem.New()
			base := memory.WithConversationID(context.Background(), "evidence-conversation")
			require.NoError(t, client.PatchConversations(base, conversationmodel.NewConversationStatus("evidence-conversation", "")))
			publisher := &captureStreamPublisher{}
			ctx := evidence.WithPublication(base, &testPublication{reject: reject})
			ctx = WithRecorderObserver(WithStreamPublisher(ctx, publisher), client)
			observer := ObserverFromContext(ctx)
			ctx, err := observer.OnCallStart(ctx, Info{Provider: "fixture", Model: "fixture", LLMRequest: &llm.GenerateRequest{Options: &llm.Options{Mode: "chat"}}})
			require.NoError(t, err)
			original := "```forge-data\n{\"unverified\":123}\n```\n"
			require.NoError(t, observer.OnStreamDelta(ctx, []byte(original)))
			err = observer.OnCallEnd(ctx, Info{Model: "fixture", StreamText: original})
			visible := strings.Join(publisher.deltas(), "")
			require.NotContains(t, visible, "unverified")
			message, readErr := client.GetMessage(ctx, memory.ModelMessageIDFromContext(ctx))
			require.NoError(t, readErr)
			require.NotNil(t, message.ModelCall)
			require.NotNil(t, message.ModelCall.StreamPayloadId)
			payload, readErr := client.GetPayload(ctx, *message.ModelCall.StreamPayloadId)
			require.NoError(t, readErr)
			require.NotNil(t, payload.InlineBody)
			require.Equal(t, original, string(*payload.InlineBody), "raw provider audit must remain exact")
			if reject {
				require.True(t, evidence.IsRejection(err), "%v", err)
				require.Empty(t, visible)
				require.Equal(t, "failed", message.ModelCall.Status)
				require.NotContains(t, message.GetContentPreferContent(), "unverified")
			} else {
				require.NoError(t, err)
				require.Contains(t, visible, "verified")
				require.Contains(t, message.GetContentPreferContent(), "verified")
			}
		})
	}
}
