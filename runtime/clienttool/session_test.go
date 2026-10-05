package clienttool

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestSessionRejectsAliasesAndBackendCollisions(t *testing.T) {
	_, err := NewSession([]llm.ToolDefinition{{Name: "local/lookup"}, {Name: "local-lookup"}})
	require.ErrorContains(t, err, "duplicate")
	session, err := NewSession([]llm.ToolDefinition{{Name: "local/lookup", Parameters: map[string]interface{}{"type": "object"}}})
	require.NoError(t, err)
	_, ok := session.Lookup("local-lookup")
	require.True(t, ok)
	require.ErrorContains(t, session.ValidateBackend([]llm.ToolDefinition{{Name: "local:lookup"}}), "collides")
	ctx := WithSession(context.Background(), session)
	require.Same(t, session, FromContext(ctx))
	definitions := session.Definitions()
	definitions[0].Parameters["type"] = "string"
	require.Equal(t, "object", session.Definitions()[0].Parameters["type"])
}

func TestSessionReservesConcurrentCallsAndRejectsConflicts(t *testing.T) {
	session, err := NewSession([]llm.ToolDefinition{{Name: "local"}})
	require.NoError(t, err)
	var writes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := session.Defer("call", "local", map[string]interface{}{"id": 1}, func() (PendingCall, error) { writes.Add(1); return PendingCall{ToolMessageID: "message"}, nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, writes.Load())
	require.Len(t, session.Pending(), 1)
	snapshot := session.Pending()
	snapshot[0].Arguments["id"] = 9
	require.Equal(t, json.Number("1"), session.Pending()[0].Arguments["id"])
	_, err = session.Defer("call", "local", map[string]interface{}{"id": 2}, func() (PendingCall, error) { t.Fatal("conflict must not persist"); return PendingCall{}, nil })
	require.ErrorContains(t, err, "conflicting")
	require.Error(t, session.Error())
}

func TestSessionPersistenceFailurePoisonsTheRequest(t *testing.T) {
	session, err := NewSession([]llm.ToolDefinition{{Name: "local"}})
	require.NoError(t, err)
	failure := errors.New("database unavailable")
	_, err = session.Defer("call", "local", nil, func() (PendingCall, error) { return PendingCall{}, failure })
	require.ErrorIs(t, err, failure)
	require.ErrorIs(t, session.Error(), failure)
	require.Empty(t, session.Pending())
	_, err = session.Defer("retry", "local", nil, func() (PendingCall, error) { t.Fatal("failed request cannot continue"); return PendingCall{}, nil })
	require.ErrorIs(t, err, failure)
}

func TestSessionPreservesExactArgumentsAndOpaqueDefinitionMetadata(t *testing.T) {
	session, err := NewSession([]llm.ToolDefinition{{Name: "local", Parameters: map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"const": json.Number("9007199254740993")}}}}})
	require.NoError(t, err)
	metadata := json.RawMessage(`{"opaque":{"n":9007199254740993,"decimal":0.123456789012345678901},"flag":false,"nullable":null}`)
	require.NoError(t, session.SetMetadata("local", metadata))
	metadata[2] = 'X'
	call, err := session.DeferForTurn("thread", "turn", "call", "local", map[string]any{"n": json.Number("9007199254740993"), "decimal": json.Number("0.123456789012345678901")}, func() (PendingCall, error) {
		return PendingCall{ConversationID: "thread", TurnID: "turn", ToolMessageID: "tool"}, nil
	})
	require.NoError(t, err)
	require.Equal(t, json.Number("9007199254740993"), call.Arguments["n"])
	require.Equal(t, json.Number("0.123456789012345678901"), call.Arguments["decimal"])
	require.Contains(t, string(call.Metadata), "9007199254740993")
	require.Contains(t, string(call.Metadata), "0.123456789012345678901")
	require.Contains(t, string(call.Metadata), `"flag":false`)
	require.Equal(t, json.Number("9007199254740993"), session.Definitions()[0].Parameters["properties"].(map[string]any)["n"].(map[string]any)["const"])
	replay, err := session.DeferForTurn("thread", "turn", "call", "local", call.Arguments, func() (PendingCall, error) {
		t.Fatal("exact duplicate must not persist again")
		return PendingCall{}, nil
	})
	require.NoError(t, err)
	require.Equal(t, call, replay)
}
