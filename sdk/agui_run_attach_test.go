package sdk

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
)

const attachRequest = `{"threadId":"thread","runId":"external-run","messages":[],"tools":[],"context":[],"state":{},"forwardedProps":{"agently":{"version":"1","operation":"run.attach","requestId":"reload","payload":{}}}}`

func TestAGUIAttachReplaysOwnedRunWithoutOriginalClientInput(t *testing.T) {
	client, server := newDurableAGUIServer(t)
	client.query = func(_ context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{Content: "hello"}, nil
	}
	original := strings.TrimSuffix(aguiChatRequest, "}") + `,"forwardedProps":{"agently":{"version":"1","operation":"chat","payload":{"context":{"privateHint":"not-exposed-in-attachment"}}}}}`
	status, originalWire := durablePost(t, server, original, nil)
	require.Equal(t, 200, status, originalWire)
	status, attachedWire := durablePost(t, server, attachRequest, nil)
	require.Equal(t, 200, status, attachedWire)
	require.Equal(t, originalWire, attachedWire)
	require.NotContains(t, attachedWire, "not-exposed-in-attachment")
	require.EqualValues(t, 1, client.queries.Load())
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(attachedWire)), "RUN_FINISHED")
}

func TestAGUIAttachFollowsExistingLiveWorkerAfterNavigationDetach(t *testing.T) {
	client, server := newDurableAGUIServer(t)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	entered := make(chan struct{})
	client.query = func(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		_ = client.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: input.ConversationID, TurnID: input.MessageID, MessageID: "assistant", Content: "first"})
		close(entered)
		<-release
		return &agentsvc.QueryOutput{Content: "first second"}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(aguiChatRequest))
	require.NoError(t, err)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("original query did not start")
	}
	_ = response.Body.Close()
	request, err = http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(attachRequest))
	require.NoError(t, err)
	attached, err := server.Client().Do(request)
	require.NoError(t, err)
	defer attached.Body.Close()
	require.Equal(t, 200, attached.StatusCode)
	scanner := bufio.NewScanner(attached.Body)
	var wire strings.Builder
	observedLive := false
	for scanner.Scan() {
		line := scanner.Text()
		wire.WriteString(line + "\n")
		if strings.Contains(line, `"TEXT_MESSAGE_CONTENT"`) && strings.Contains(line, `"first"`) {
			observedLive = true
			break
		}
	}
	require.True(t, observedLive, "attachment must replay live prefix before original worker completes")
	require.EqualValues(t, 1, client.queries.Load())
	once.Do(func() { close(release) })
	for scanner.Scan() {
		wire.WriteString(scanner.Text() + "\n")
	}
	require.NoError(t, scanner.Err())
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(wire.String())), "RUN_FINISHED")
	require.EqualValues(t, 1, client.queries.Load())
}

func TestAGUIAttachCannotAdmitNewWorkOrCrossPrincipalScope(t *testing.T) {
	client, server := newDurableAGUIServer(t)
	client.query = func(_ context.Context, _ *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{Content: "hello"}, nil
	}
	status, _ := durablePost(t, server, attachRequest, nil)
	require.Equal(t, 404, status)
	require.Zero(t, client.queries.Load())
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	for _, invalid := range []string{
		strings.Replace(attachRequest, `"messages":[]`, `"messages":[{"id":"u2","role":"user","content":"execute this"}]`, 1),
		strings.Replace(attachRequest, `"state":{}`, `"state":{"changed":true}`, 1),
		strings.Replace(attachRequest, `"payload":{}`, `"payload":{"runId":"different"}`, 1),
		strings.Replace(attachRequest, `"version":"1"`, `"version":"2"`, 1),
	} {
		status, _ := durablePost(t, server, invalid, nil)
		require.Equal(t, 400, status)
	}
	handler := handleAGUIRun(client, nil)
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "foreign"})))
	}))
	defer foreign.Close()
	status, wire = durablePost(t, foreign, attachRequest, nil)
	require.Equal(t, 404, status, wire)
	require.NotContains(t, wire, `"type":"RUN_STARTED"`)
	require.EqualValues(t, 1, client.queries.Load())
	request, err := http.NewRequest("POST", server.URL, strings.NewReader(attachRequest))
	require.NoError(t, err)
	request.Header.Set("Last-Event-ID", aguiCursor("thread", "external-run", 100000))
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	require.Equal(t, 400, response.StatusCode)
}
