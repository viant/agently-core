package sdk

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
)

func TestAGUIParallelHTTPRunsPreserveBothAcceptedHistoriesAndReplay(t *testing.T) {
	c, server := newDurableAGUIServer(t)
	aAppend, bAppend, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var aOnce, bOnce, finishOnce sync.Once
	defer aOnce.Do(func() { close(aAppend) })
	defer bOnce.Do(func() { close(bAppend) })
	defer finishOnce.Do(func() { close(finish) })
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		id, prefix, suffix, release := "a-answer", "prefix", " suffix", aAppend
		if in.Query == "B" {
			id, prefix, suffix, release = "b-answer", "b", " second", bAppend
		}
		if err := c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: in.ConversationID, TurnID: in.MessageID, AssistantMessageID: id, Content: prefix}); err != nil {
			return nil, err
		}
		<-release
		offset := len(prefix)
		if err := c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: in.ConversationID, TurnID: in.MessageID, AssistantMessageID: id, Content: suffix, ContentOffset: &offset}); err != nil {
			return nil, err
		}
		<-finish
		return &agentsvc.QueryOutput{Content: prefix + suffix}, nil
	}
	post := func(run, user, text string) (string, *http.Response) {
		body := string(rawAGUI(map[string]any{"threadId": "thread", "runId": run, "messages": []any{map[string]any{"id": user, "role": "user", "content": text}}}))
		response, err := server.Client().Post(server.URL, "application/json", strings.NewReader(body))
		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode)
		return body, response
	}
	has := func(text string) bool {
		thread, err := c.store.GetThread(context.Background(), "owner", "thread")
		return err == nil && strings.Contains(string(thread.Messages), text)
	}
	bodyA, responseA := post("http-a", "user-a", "A")
	defer responseA.Body.Close()
	require.Eventually(t, func() bool { return has("prefix") }, 3*time.Second, 5*time.Millisecond)
	bodyB, responseB := post("http-b", "user-b", "B")
	defer responseB.Body.Close()
	require.Eventually(t, func() bool { return has(`"id":"b-answer"`) }, 3*time.Second, 5*time.Millisecond)
	aOnce.Do(func() { close(aAppend) })
	require.Eventually(t, func() bool { return has("prefix suffix") }, 3*time.Second, 5*time.Millisecond)
	bOnce.Do(func() { close(bAppend) })
	require.Eventually(t, func() bool { return has("b second") }, 3*time.Second, 5*time.Millisecond)
	require.True(t, has("prefix suffix"), "the second HTTP producer cannot revert the first")
	finishOnce.Do(func() { close(finish) })
	wireA, err := io.ReadAll(responseA.Body)
	require.NoError(t, err)
	wireB, err := io.ReadAll(responseB.Body)
	require.NoError(t, err)
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(string(wireA))), "RUN_FINISHED")
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(string(wireB))), "RUN_FINISHED")
	thread, err := c.store.GetThread(context.Background(), "owner", "thread")
	require.NoError(t, err)
	require.Contains(t, string(thread.Messages), "prefix suffix")
	require.Contains(t, string(thread.Messages), "b second")
	for _, pair := range []struct{ Body, Wire string }{{bodyA, string(wireA)}, {bodyB, string(wireB)}} {
		status, replay := durablePost(t, server, pair.Body, nil)
		require.Equal(t, 200, status)
		require.Equal(t, pair.Wire, replay)
	}
	require.EqualValues(t, 2, c.queries.Load(), "both reattachments replay without another native Query")
}
