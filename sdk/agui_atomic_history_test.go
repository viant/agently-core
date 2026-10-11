package sdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

func TestAGUIConcurrentNativeJournalWritersRetainExactHistory(t *testing.T) {
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(context.Background())
	repository := aguistore.New(server)

	seed, _, err := repository.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "atomic-history", RunID: "seed", Input: rawAGUI(map[string]any{"threadId": "atomic-history", "runId": "seed", "messages": []any{}})})
	require.NoError(t, err)
	initial, err := repository.GetThread(ctx, "owner", "atomic-history")
	require.NoError(t, err)
	historical := []aguistate.Object{{"id": "seed-history", "role": "assistant", "content": "historical answer"}}
	_, err = repository.Append(ctx, "owner", seed.ThreadID, seed.RunID, seed.Revision, []json.RawMessage{
		rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": seed.ThreadID, "runId": seed.RunID}),
		rawAGUI(map[string]any{"type": "STATE_SNAPSHOT", "snapshot": map[string]any{}}),
		rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": historical}),
		rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": seed.ThreadID, "runId": seed.RunID, "outcome": map[string]any{"type": "success"}}),
	}, &aguistore.Change{State: rawAGUI(map[string]any{}), Messages: rawAGUI(historical), ExpectedThreadRevision: initial.Revision})
	require.NoError(t, err)
	const workers = 12
	writers := make([]*aguiJournalWriter, workers)
	for i := 0; i < workers; i++ {
		id := fmt.Sprintf("writer-%d", i)
		input := rawAGUI(map[string]any{"threadId": "atomic-history", "runId": id, "messages": []any{map[string]any{"id": "user-" + id, "role": "user", "content": id}}})
		run, fresh, err := repository.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "atomic-history", RunID: id, TurnID: "turn-" + id, ClientMessageID: "user-" + id, Input: input})
		require.NoError(t, err)
		require.True(t, fresh)
		claimed, err := repository.Claim(ctx, "owner", run.ThreadID, run.RunID, run.Revision, id, time.Minute)
		require.NoError(t, err)
		writers[i] = &aguiJournalWriter{ctx: ctx, store: repository, run: claimed, leaseOwner: id}
	}
	thread, err := repository.GetThread(ctx, "owner", "atomic-history")
	require.NoError(t, err)
	for i, w := range writers {
		projection, err := aguistate.New(thread.State, thread.Messages)
		require.NoError(t, err)
		id := fmt.Sprintf("writer-%d", i)
		projection.Messages = append(projection.Messages, aguistate.Object{"id": "user-" + id, "role": "user", "content": id})
		w.projection = projection
		w.threadRevision = thread.Revision
		w.messageBaseline = append(json.RawMessage(nil), thread.Messages...)
	}
	ready := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for _, w := range writers {
		wg.Add(1)
		go func(w *aguiJournalWriter) {
			defer wg.Done()
			<-ready
			results <- w.write([]json.RawMessage{
				rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": w.run.ThreadID, "runId": w.run.RunID}),
				rawAGUI(map[string]any{"type": "STATE_SNAPSHOT", "snapshot": map[string]any{}}),
				rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": w.projection.Messages}),
				rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": w.run.ThreadID, "runId": w.run.RunID, "outcome": map[string]any{"type": "success"}}),
			}, nil)
		}(w)
	}
	close(ready)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	thread, err = repository.GetThread(ctx, "owner", "atomic-history")
	require.NoError(t, err)
	var messages []aguistate.Object
	require.NoError(t, json.Unmarshal(thread.Messages, &messages))
	require.Len(t, messages, workers+1)
	seen := map[string]bool{}
	for _, message := range messages {
		id := message["id"].(string)
		require.False(t, seen[id])
		seen[id] = true
		if id == "seed-history" {
			require.Equal(t, "historical answer", message["content"])
		} else {
			require.Equal(t, id[5:], message["content"])
		}
	}
	for i := 0; i < workers; i++ {
		id := fmt.Sprintf("writer-%d", i)
		require.True(t, seen["user-"+id])
		run, err := repository.GetRun(ctx, "owner", "atomic-history", id)
		require.NoError(t, err)
		require.Equal(t, aguistore.StatusFinished, run.Status)
		require.Equal(t, int64(4), run.LastSequence)
	}
}

type embeddedAppendDecorator struct {
	*aguistore.ComponentStore
	appends int
	cause   error
}

func (s *embeddedAppendDecorator) Append(context.Context, string, string, string, int64, []json.RawMessage, *aguistore.Change) (*aguistore.Run, error) {
	s.appends++
	return nil, s.cause
}

func TestAGUIPromotedPreparedAppendDoesNotBypassDecorator(t *testing.T) {
	client, _ := newDurableAGUIServer(t)
	nativeStore := client.store.(*aguistore.ComponentStore)
	ctx := context.Background()
	run, _, err := nativeStore.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "decorated", RunID: "decorated-run", Input: rawAGUI(map[string]any{"threadId": "decorated", "runId": "decorated-run", "messages": []any{}})})
	require.NoError(t, err)
	run, err = nativeStore.Claim(ctx, "owner", run.ThreadID, run.RunID, run.Revision, "observer", time.Minute)
	require.NoError(t, err)
	thread, err := nativeStore.GetThread(ctx, "owner", run.ThreadID)
	require.NoError(t, err)
	projection, err := aguistate.New(thread.State, thread.Messages)
	require.NoError(t, err)
	cause := fmt.Errorf("decorator append rejected")
	decorated := &embeddedAppendDecorator{ComponentStore: nativeStore, cause: cause}
	// Go promotes the optional methods, but native ownership remains the inner
	// store. The writer must use this decorator's original Append contract.
	var atomic aguistore.PreparedAppender = decorated
	require.False(t, atomic.OwnsPreparedAppend(decorated))
	writer := &aguiJournalWriter{ctx: ctx, store: decorated, run: run, projection: projection, leaseOwner: "observer", messageBaseline: thread.Messages}
	err = writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": run.ThreadID, "runId": run.RunID})}, nil)
	require.ErrorIs(t, err, cause)
	require.Equal(t, 1, decorated.appends)
	latest, err := nativeStore.GetRun(ctx, "owner", run.ThreadID, run.RunID)
	require.NoError(t, err)
	require.Zero(t, latest.LastSequence)
	require.Equal(t, run.Revision, latest.Revision)
}

func TestAGUIAtomicMultiActivityBatchPreservesAliasesAndImmutableAncestry(t *testing.T) {
	client, _ := newDurableAGUIServer(t)
	ctx := context.Background()
	run, _, err := client.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "batch-root", RunID: "batch-run", TurnID: "batch-native-turn", Input: rawAGUI(map[string]any{"threadId": "batch-root", "runId": "batch-run", "messages": []any{}})})
	require.NoError(t, err)
	run, err = client.store.Claim(ctx, "owner", run.ThreadID, run.RunID, run.Revision, "observer", time.Minute)
	require.NoError(t, err)
	thread, err := client.store.GetThread(ctx, "owner", run.ThreadID)
	require.NoError(t, err)
	projection, err := aguistate.New(thread.State, thread.Messages)
	require.NoError(t, err)
	writer := &aguiJournalWriter{ctx: ctx, store: client.store, run: run, projection: projection, leaseOwner: "observer", messageBaseline: thread.Messages}
	translator := agui.NewTranslator(run.ThreadID, run.RunID)
	require.NoError(t, translator.SetNativeIdentity(run.TurnID))
	require.NoError(t, writer.write(encodeAGUIEvents(translator.Start()), nil))
	inv := requestctx.Invocation{ID: "batch-child", Name: "child", ConversationID: "original-child", TurnID: "child-turn", ParentConversationID: run.ThreadID, ParentTurnID: run.TurnID, ParentToolCallID: "parent-op"}
	require.NoError(t, writer.write(encodeAGUIEvents(translator.RegisterInvocation(inv)), nil))
	binding := AGUIMCPAppBinding{ThreadID: inv.ConversationID, NativeTurnID: inv.TurnID, InvocationID: inv.ID, ServerID: "same-server", ServerHash: "hash", ResourceURI: "ui://child/app"}
	activity := func(id string, app AGUIMCPAppBinding) []json.RawMessage {
		raw, err := MCPAppsActivity(id, app, json.RawMessage(`{"resultType":"complete","content":[]}`), nil)
		require.NoError(t, err)
		return encodeAGUIEvents(translator.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeProtocol, ConversationID: inv.ConversationID, TurnID: inv.TurnID, ProtocolEvent: raw}))
	}
	require.NoError(t, writer.write(activity("existing-app", binding), nil))
	aliases := func() map[string]string {
		result := map[string]string{}
		for _, m := range writer.projection.Messages {
			if m["activityType"] != "mcp-apps" {
				continue
			}
			var content struct {
				Binding AGUIMCPAppBinding `json:"_agentlyApp"`
			}
			require.NoError(t, json.Unmarshal(rawAGUI(m["content"]), &content))
			result[m["id"].(string)] = content.Binding.PublicServerID
		}
		return result
	}
	oldAlias := aliases()["existing-app"]
	var nativeMessages []aguistate.Object
	require.NoError(t, json.Unmarshal(rawAGUI(writer.projection.Messages), &nativeMessages))
	for _, m := range nativeMessages {
		if m["activityType"] != "mcp-apps" {
			continue
		}
		content := m["content"].(map[string]any)
		var app AGUIMCPAppBinding
		require.NoError(t, json.Unmarshal(rawAGUI(content["_agentlyApp"]), &app))
		app.PublicServerID, app.AppInstanceID = "", ""
		content["serverId"], content["_agentlyApp"] = app.ServerID, app
	}
	before := writer.run.LastSequence
	batch := []json.RawMessage{rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": nativeMessages})}
	batch = append(batch, activity("first-batch-app", binding)...)
	batch = append(batch, activity("second-batch-app", binding)...)
	require.NoError(t, writer.write(batch, nil))
	issued := aliases()
	require.Equal(t, oldAlias, issued["existing-app"])
	require.Len(t, issued, 3)
	require.NotEqual(t, issued["first-batch-app"], issued["second-batch-app"])
	events, err := client.store.Replay(ctx, "owner", run.ThreadID, run.RunID, before, 256)
	require.NoError(t, err)
	for id, alias := range issued {
		app, err := ResolveAGUIMCPApp(ctx, client.store, "owner", alias, "hash")
		require.NoError(t, err, id)
		require.Equal(t, inv.ConversationID, app.ThreadID)
		require.Equal(t, inv.TurnID, app.NativeTurnID)
		if id == "existing-app" {
			continue
		}
		decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(alias, "agui-app:"))
		require.NoError(t, err)
		var identity mcpAppJournalIdentity
		require.NoError(t, json.Unmarshal(decoded, &identity))
		require.Greater(t, identity.Sequence, before+1, "actual alias sequence follows the aggregate snapshot")
		found := false
		for _, event := range events {
			if event.Sequence == identity.Sequence {
				require.Contains(t, string(event.Event), alias)
				found = true
			}
		}
		require.True(t, found)
	}
	committed := writer.run.LastSequence
	forged := binding
	forged.ThreadID = "foreign-child"
	require.Error(t, writer.write(activity("forged-activity", forged), nil))
	require.Equal(t, committed, writer.run.LastSequence)
	require.Equal(t, issued, aliases())

	// A checkpoint introduced in this same uncommitted batch is not authority.
	uncommitted := inv
	uncommitted.ID = "same-batch-only-child"
	uncommitted.ConversationID, uncommitted.TurnID, uncommitted.ParentToolCallID = "uncommitted-child", "uncommitted-turn", "uncommitted-parent-op"
	pendingBinding := binding
	pendingBinding.InvocationID = uncommitted.ID
	pendingBinding.ThreadID, pendingBinding.NativeTurnID = uncommitted.ConversationID, uncommitted.TurnID
	pendingRaw, err := MCPAppsActivity("uncommitted-app", pendingBinding, json.RawMessage(`{"content":[]}`), nil)
	require.NoError(t, err)
	var pendingEvent map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(pendingRaw, &pendingEvent))
	pendingEvent["subagentRunId"] = rawAGUI(uncommitted.ID)
	pendingBatch := encodeAGUIEvents(translator.RegisterInvocation(uncommitted))
	require.Contains(t, string(rawAGUI(pendingBatch)), `"phase":"registered"`)
	pendingBatch = append(pendingBatch, rawAGUI(pendingEvent))
	require.Error(t, writer.write(pendingBatch, nil))
	require.Equal(t, committed, writer.run.LastSequence)
	require.Equal(t, issued, aliases())
	// Snapshot normalization retains its original exact binding guard as well.
	require.NoError(t, json.Unmarshal(rawAGUI(writer.projection.Messages), &nativeMessages))
	for _, m := range nativeMessages {
		if m["activityType"] == "mcp-apps" {
			content := m["content"].(map[string]any)
			var app AGUIMCPAppBinding
			require.NoError(t, json.Unmarshal(rawAGUI(content["_agentlyApp"]), &app))
			app.ThreadID = "foreign-child"
			content["_agentlyApp"] = app
			break
		}
	}
	require.Error(t, writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": nativeMessages})}, nil))
	require.Equal(t, committed, writer.run.LastSequence)
	latest, err := client.store.GetRun(ctx, "owner", run.ThreadID, run.RunID)
	require.NoError(t, err)
	require.Equal(t, committed, latest.LastSequence)
	require.Equal(t, issued, aliases())
}
