package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	conversationread "github.com/viant/agently-core/internal/datly/conversation/read"
	queueread "github.com/viant/agently-core/internal/datly/turnqueue/read"
	"github.com/viant/agently-core/internal/store/queuereorder"
	"github.com/viant/agently-core/runtime/streaming"
	dexec "github.com/viant/datly/exec"
)

type canonicalQueueInvoker func(context.Context, dexec.ComponentRequest) (any, error)

func (f canonicalQueueInvoker) InvokeComponent(ctx context.Context, req dexec.ComponentRequest) (any, error) {
	return f(ctx, req)
}

func TestCanonicalQueueCurrentPositionIsLosslessAndPreservesLegacy(t *testing.T) {
	position := int64(9007199254740993)
	calls := 0
	client := &backendClient{goalInvoker: canonicalQueueInvoker(func(_ context.Context, req dexec.ComponentRequest) (any, error) {
		calls++
		input, ok := req.Input.(*queueread.QueueRowsInput)
		require.True(t, ok)
		require.Equal(t, "owned", input.ConversationId)
		require.Equal(t, "queued", input.QueueStatus)
		require.True(t, input.NativeQueuedOnly)
		require.True(t, input.Has.NativeQueuedOnly)
		return &queueread.QueueRowsOutput{Data: []*queueread.QueueRowView{
			{ConversationId: "owned", TurnId: "a", Status: "queued", QueueSeq: position},
			{ConversationId: "owned", TurnId: "b", Status: "queued", QueueSeq: 2},
		}}, nil
	})}
	state := &ConversationState{ConversationID: "owned", Turns: []*TurnState{
		{TurnID: "a", Status: TurnStatusQueued, QueueSeq: 7},
		{TurnID: "b", Status: TurnStatusQueued, QueueSeq: 8},
		{TurnID: "finished", Status: TurnStatusCompleted, QueueSeq: 9},
	}}
	require.NoError(t, client.populateCanonicalQueueSequences(context.Background(), state))
	require.Equal(t, "9007199254740993", state.Turns[0].QueueSequence)
	require.Equal(t, 7, state.Turns[0].QueueSeq)
	require.Empty(t, state.Turns[2].QueueSequence)
	raw, err := json.Marshal(state.Turns[0])
	require.NoError(t, err)
	require.Contains(t, string(raw), `"queueSequence":"9007199254740993"`)
	// A later canonical refresh follows current storage, not the old admission order.
	position = 1
	require.NoError(t, client.populateCanonicalQueueSequences(context.Background(), state))
	require.Equal(t, "1", state.Turns[0].QueueSequence)
	require.Equal(t, 7, state.Turns[0].QueueSeq)
	require.Equal(t, 2, calls)
}

func TestCanonicalQueueRejectsForeignRowsAndPropagatesReadFailure(t *testing.T) {
	for _, test := range []struct {
		name string
		rows []*queueread.QueueRowView
		err  error
	}{
		{name: "foreign conversation", rows: []*queueread.QueueRowView{{ConversationId: "foreign", TurnId: "a", Status: "queued", QueueSeq: 2}}},
		{name: "nonqueued row", rows: []*queueread.QueueRowView{{ConversationId: "owned", TurnId: "a", Status: "succeeded", QueueSeq: 2}}},
		{name: "reader failure", err: errors.New("reader unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &backendClient{goalInvoker: canonicalQueueInvoker(func(context.Context, dexec.ComponentRequest) (any, error) {
				return &queueread.QueueRowsOutput{Data: test.rows}, test.err
			})}
			state := &ConversationState{ConversationID: "owned", Turns: []*TurnState{{TurnID: "a", Status: TurnStatusQueued, QueueSeq: 7}}}
			require.Error(t, client.populateCanonicalQueueSequences(context.Background(), state))
			require.Empty(t, state.Turns[0].QueueSequence)
			require.Equal(t, 7, state.Turns[0].QueueSeq)
		})
	}
}

func TestCanonicalQueueDoesNotInventPositionForAmbiguousOrMissingRows(t *testing.T) {
	client := &backendClient{goalInvoker: canonicalQueueInvoker(func(context.Context, dexec.ComponentRequest) (any, error) {
		return &queueread.QueueRowsOutput{Data: []*queueread.QueueRowView{
			{ConversationId: "owned", TurnId: "a", Status: "queued", QueueSeq: 1},
			{ConversationId: "owned", TurnId: "a", Status: "queued", QueueSeq: 2},
		}}, nil
	})}
	state := &ConversationState{ConversationID: "owned", Turns: []*TurnState{
		{TurnID: "a", Status: TurnStatusQueued}, {TurnID: "missing", Status: TurnStatusQueued},
	}}
	require.NoError(t, client.populateCanonicalQueueSequences(context.Background(), state))
	for _, turn := range state.Turns {
		require.Empty(t, turn.QueueSequence)
	}
}

func TestQueuedMovePublishesHintOnlyAfterSuccessfulComponentReturn(t *testing.T) {
	for _, succeeds := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failed transaction"}[succeeds], func(t *testing.T) {
			collector := &goalEventCollector{}
			client := &backendClient{streaming: collector}
			client.goalInvoker = canonicalQueueInvoker(func(_ context.Context, req dexec.ComponentRequest) (any, error) {
				switch input := req.Input.(type) {
				case *conversationread.ConversationInput:
					require.Equal(t, "owned", input.Id)
					return &conversationread.ConversationOutput{Data: []*conversationread.ConversationView{{Id: "owned"}}}, nil
				case *queueread.QueueRowsInput:
					return &queueread.QueueRowsOutput{Data: []*queueread.QueueRowView{
						{ConversationId: "owned", TurnId: "a", QueueSeq: 1},
						{ConversationId: "owned", TurnId: "b", QueueSeq: 2},
					}}, nil
				case *queuereorder.Input:
					require.Empty(t, collector.events, "no availability hint before the component completes")
					if !succeeds {
						return nil, queuereorder.ErrConflict
					}
					return &queuereorder.Output{}, nil
				default:
					t.Fatalf("unexpected input %T", input)
					return nil, nil
				}
			})
			err := moveQueuedTurn(client, context.Background(), &MoveQueuedTurnInput{ConversationID: "owned", TurnID: "b", Direction: "up"})
			if !succeeds {
				require.Error(t, err)
				require.Empty(t, collector.events)
				return
			}
			require.NoError(t, err)
			require.Len(t, collector.events, 1)
			require.Equal(t, streaming.EventTypeConversationMetaUpdated, collector.events[0].Type)
			require.Equal(t, "owned", collector.events[0].ConversationID)
			require.Equal(t, "owned", collector.events[0].StreamID)
			require.Equal(t, map[string]any{"aguiUpdated": true}, collector.events[0].Patch)
		})
	}
}
