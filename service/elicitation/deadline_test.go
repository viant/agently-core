package elicitation

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/mcp-protocol/schema"
	"testing"
	"time"
)

func TestRecordPersistsAndPublishesWaiterDeadline(t *testing.T) {
	client := newSeqRecordingConv("thread")
	publisher := &captureEventsPublisher{}
	service := New(client, nil, router.New(), NoopAwaiterFactory())
	service.SetStreamPublisher(publisher)
	deadline := time.Now().Add(time.Minute).UTC()
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	request := &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose", ElicitationId: "ask"}}
	_, err := service.Record(ctx, &requestctx.TurnMeta{ConversationID: "thread", TurnID: "turn"}, "assistant", request)
	require.NoError(t, err)
	require.Equal(t, deadline.Format(time.RFC3339Nano), request.Deadline)
	require.NotEmpty(t, publisher.events)
	require.Equal(t, request.Deadline, publisher.events[len(publisher.events)-1].ElicitationData["deadline"])
	found := false
	for _, payload := range client.payloads {
		if payload.Kind != "elicitation_request" {
			continue
		}
		var value map[string]interface{}
		require.NoError(t, json.Unmarshal(*payload.InlineBody, &value))
		require.Equal(t, request.Deadline, value["deadline"])
		found = true
	}
	require.True(t, found)
}
