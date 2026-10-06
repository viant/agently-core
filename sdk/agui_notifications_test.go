package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type runNotice struct {
	run     *aguistore.Run
	queries int32
	err     error
}
type notifyingAGUIClient struct {
	*durableAGUIClient
	notices chan runNotice
}

func (c *notifyingAGUIClient) aguiNotifyRunUpdated(ctx context.Context, thread string) {
	run, err := c.store.GetRun(ctx, "owner", thread, "external-run")
	c.notices <- runNotice{run: run, queries: c.queries.Load(), err: err}
}
func TestAGUINotificationsFollowAdmissionAndTerminalCommitWithoutReplayLoop(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	client := &notifyingAGUIClient{durableAGUIClient: base, notices: make(chan runNotice, 8)}
	base.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{Content: "done"}, nil
	}
	handler := handleAGUIRun(client, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "owner"})))
	}))
	defer server.Close()
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	require.Eventually(t, func() bool { return len(client.notices) == 2 }, time.Second, 10*time.Millisecond)
	admission := <-client.notices
	terminal := <-client.notices
	require.NoError(t, admission.err)
	require.NoError(t, terminal.err)
	require.Equal(t, aguistore.StatusAdmitted, admission.run.Status)
	require.Zero(t, admission.queries, "discovery follows durable admission before native execution")
	require.Equal(t, aguistore.StatusFinished, terminal.run.Status)
	require.Positive(t, terminal.run.LastSequence)
	status, replay := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status)
	require.Equal(t, wire, replay)
	require.Empty(t, client.notices, "observing a completed run must not create notification/bootstrap loops")
	require.EqualValues(t, 1, client.queries.Load())
}
