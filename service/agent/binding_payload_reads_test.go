package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/runtime/requestctx"
)

type bindingCountingClient struct {
	*stubConversationClient
	conv    *apiconv.Conversation
	mu      sync.Mutex
	calls   int
	delay   time.Duration
	failure error
	missing bool
}

func (c *bindingCountingClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return c.conv, nil
}
func (c *bindingCountingClient) GetPayload(ctx context.Context, id string) (*apiconv.Payload, error) {
	c.mu.Lock()
	c.calls++
	delay, failure, missing := c.delay, c.failure, c.missing
	payload := c.payloads[id]
	c.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if failure != nil {
		return nil, failure
	}
	if missing {
		return nil, nil
	}
	return payload, nil
}
func (c *bindingCountingClient) count() int { c.mu.Lock(); defer c.mu.Unlock(); return c.calls }
func payloadReadFixture(t *testing.T) (*Service, *bindingCountingClient, context.Context, *QueryInput) {
	body := []byte(gzipString(t, `{"public":"fixture result"}`))
	now := time.Now()
	turn := "turn"
	name := "fixture/read"
	client := &bindingCountingClient{stubConversationClient: &stubConversationClient{payloads: map[string]*apiconv.Payload{"payload": {Id: "payload", InlineBody: &body, Compression: "gzip", MimeType: "application/json"}}}}
	tools := []*conversationmodel.ToolMessageView{}
	for _, id := range []string{"one", "two", "three"} {
		tools = append(tools, &conversationmodel.ToolMessageView{Id: id, CreatedAt: now, ToolName: &name, ToolCall: &conversationmodel.ToolCallView{OpId: id, ToolName: name, Status: "completed", ResponsePayload: &conversationmodel.ModelCallStreamPayloadView{Id: "payload", InlineBody: stringPtr("stale inline fallback")}, RequestPayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: stringPtr(`{"value":"public"}`)}}})
	}
	client.conv = &apiconv.Conversation{Id: "conversation", Transcript: []*conversationmodel.TranscriptView{{Id: turn, ConversationId: "conversation", Status: "running", Message: []*conversationmodel.MessageView{{Id: "user", ConversationId: "conversation", TurnId: &turn, Role: "user", Type: "text", Content: stringPtr("public fixture"), CreatedAt: now, ToolMessage: tools}}}}}
	client.conv.Transcript[0].Message[0].ToolMessage = tools
	for _, tm := range tools {
		client.conv.Transcript[0].Message = append(client.conv.Transcript[0].Message, &conversationmodel.MessageView{Id: tm.Id, ConversationId: "conversation", TurnId: &turn, Role: "assistant", Type: "tool_op", CreatedAt: now, ToolMessage: []*conversationmodel.ToolMessageView{tm}})
	}
	svc := &Service{conversation: client, defaults: &config.Defaults{PreviewSettings: config.PreviewSettings{Limit: 4096}}}
	ctx := requestctx.WithTurnMeta(authctx.WithCanonicalUserID(context.Background(), "owner"), requestctx.TurnMeta{ConversationID: "conversation", TurnID: turn})
	input := &QueryInput{ConversationID: "conversation", Query: "public fixture", Agent: &agentmdl.Agent{}}
	return svc, client, ctx, input
}
func TestBuildBindingPayloadReadsShareOnlyOneAssembly(t *testing.T) {
	svc, client, ctx, input := payloadReadFixture(t)
	client.delay = 15 * time.Millisecond
	started := time.Now()
	history, err := svc.buildHistoryWithLimit(ctx, client.conv.GetTranscript(), input)
	require.NoError(t, err)
	historyCalls := client.count()
	require.Greater(t, historyCalls, 0)
	tools, err := svc.buildToolExecutions(ctx, input, client.conv, "turn")
	require.NoError(t, err)
	baseline := time.Since(started)
	baselineCalls := client.count()
	require.Greater(t, baselineCalls, historyCalls)
	client.mu.Lock()
	client.calls = 0
	client.mu.Unlock()
	started = time.Now()
	bound, err := svc.BuildBinding(ctx, input)
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.Equal(t, 1, client.count())
	require.Len(t, tools.Calls, 4) // The existing joined parent and three concrete calls stay unchanged.
	require.Equal(t, history.History.Messages, bound.History.Messages)
	_, err = svc.BuildBinding(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 2, client.count(), "same caller next binding must read afresh")
	other := authctx.WithCanonicalUserID(ctx, "other-owner")
	_, err = svc.BuildBinding(other, input)
	require.NoError(t, err)
	require.Equal(t, 3, client.count(), "different caller never borrows prior binding payload")
	t.Logf("synthetic uniquePayloads=1 baselineGetPayload=%d scopedGetPayload=1 baseline=%s scoped=%s (15ms injected read)", baselineCalls, baseline, elapsed)
}
func TestBindingPayloadReadsCancellationFailureAndPrincipalIsolation(t *testing.T) {
	svc, client, ctx, _ := payloadReadFixture(t)
	scope := svc.withBindingPayloadReads(ctx)
	first, err := svc.bindingPayload(scope, "payload")
	require.NoError(t, err)
	(*first.InlineBody)[0] = 0
	first.MimeType = "changed"
	again, err := svc.bindingPayload(scope, "payload")
	require.NoError(t, err)
	require.NotEqual(t, byte(0), (*again.InlineBody)[0])
	require.Equal(t, "application/json", again.MimeType)
	require.Equal(t, 1, client.count())
	canceled, cancel := context.WithCancel(scope)
	cancel()
	_, err = svc.bindingPayload(canceled, "payload")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, client.count())
	_, err = svc.bindingPayload(authctx.WithCanonicalUserID(scope, "different"), "payload")
	require.NoError(t, err)
	require.Equal(t, 2, client.count())
	for _, mode := range []string{"error", "missing"} {
		fresh := svc.withBindingPayloadReads(ctx)
		client.mu.Lock()
		client.failure = nil
		client.missing = mode == "missing"
		if mode == "error" {
			client.failure = errors.New("fixture error")
		}
		before := client.calls
		client.mu.Unlock()
		for i := 0; i < 2; i++ {
			_, _ = svc.bindingPayload(fresh, "payload")
		}
		require.Equal(t, before+2, client.count(), mode+" must not be cached")
	}
}
func TestBindingPayloadReadsConcurrentSharingAndAttachmentBytes(t *testing.T) {
	svc, client, ctx, _ := payloadReadFixture(t)
	client.delay = 20 * time.Millisecond
	scope := svc.withBindingPayloadReads(ctx)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := svc.bindingPayload(scope, "payload")
			require.NoError(t, err)
			require.NotNil(t, p)
		}()
	}
	wg.Wait()
	require.Equal(t, 1, client.count())
	image := []byte{0x89, 0x50, 0x4e, 0x47}
	client.mu.Lock()
	client.payloads["image"] = &apiconv.Payload{Id: "image", MimeType: "image/png", InlineBody: &image, URI: stringPtr("scratchpad://artifact/public-fixture")}
	client.mu.Unlock()
	_, err := svc.bindingPayload(scope, "image")
	require.NoError(t, err)
	msg := &apiconv.Message{Id: "image-message", AttachmentPayloadId: stringPtr("image"), CreatedAt: time.Now()}
	attachments, err := svc.attachmentsFromMessage(scope, msg, nil, false)
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	require.Equal(t, image, attachments[0].Data)
	require.Equal(t, "image/png", attachments[0].MIMEType())
	require.Equal(t, 2, client.count(), "attachment restoration shares exact successful payload ID")
}

func TestBindingPayloadReadsClosedScopeAndCanceledWaiter(t *testing.T) {
	svc, client, ctx, _ := payloadReadFixture(t)
	client.delay = 25 * time.Millisecond
	scope := svc.withBindingPayloadReads(ctx)
	done := make(chan error, 1)
	go func() { _, err := svc.bindingPayload(scope, "payload"); done <- err }()
	require.Eventually(t, func() bool { return client.count() == 1 }, time.Second, time.Millisecond)
	waiter, cancel := context.WithCancel(scope)
	cancel()
	_, err := svc.bindingPayload(waiter, "payload")
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, <-done)
	require.Equal(t, 1, client.count())
	closeBindingPayloadReads(scope)
	_, err = svc.bindingPayload(scope, "payload")
	require.NoError(t, err)
	require.Equal(t, 2, client.count(), "escaped context cannot retain reads after the binding ends")
}
