package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

type aguiDetachedObserver struct {
	*aguiInvocationObserver
	invocation requestctx.Invocation
	returned   chan aguiInvocationReturn
}

func (o *aguiDetachedObserver) BeforeInvocation(ctx context.Context, inv requestctx.Invocation) error {
	if inv.ParentInvocationID == o.invocation.ID {
		inv.ParentInvocationID = ""
	}
	return o.aguiInvocationObserver.BeforeInvocation(ctx, inv)
}
func (o *aguiDetachedObserver) InvocationReturned(ctx context.Context, result requestctx.InvocationResult) error {
	if result.Invocation.ID != o.invocation.ID {
		return o.aguiInvocationObserver.InvocationReturned(ctx, result)
	}
	request := aguiInvocationReturn{result: result, done: make(chan error, 1)}
	select {
	case o.returned <- request:
	case <-o.ctx.Done():
		return nil
	}
	select {
	case err := <-request.done:
		return err
	case <-o.ctx.Done():
		return nil
	}
}

// Detached native execution owns a protocol journal of its own. Initialization
// subscribes and commits RUN_STARTED before the native invocation may execute.
func startAGUIDetached(ctx context.Context, parent *aguiInvocationObserver, inv requestctx.Invocation) (context.Context, error) {
	if parent.store == nil || parent.run == nil || parent.runtime == nil {
		return nil, fmt.Errorf("detached AG-UI observer requires durable runtime")
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	childThread, err := aguiWireThreadForConversation(ctx, parent.store, parent.run.Principal, inv.ConversationID)
	if err != nil {
		cancel()
		return nil, err
	}
	inputFields := map[string]any{"threadId": childThread, "runId": inv.ID, "parentRunId": parent.run.RunID, "messages": []any{}, "forwardedProps": map[string]any{"agentlyInvocation": inv}}
	var original struct {
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(parent.run.Input, &original); err != nil {
		cancel()
		return nil, err
	}
	if len(original.Tools) > 0 {
		inputFields["tools"] = original.Tools
	} else if session := clienttool.FromContext(ctx); session != nil {
		definitions := make([]map[string]any, 0)
		for _, tool := range session.Definitions() {
			parameters := tool.Parameters
			if parameters == nil {
				parameters = map[string]any{}
			}
			definition := map[string]any{"name": tool.Name, "description": tool.Description, "parameters": parameters}
			if metadata := session.Metadata(tool.Name); len(metadata) > 0 {
				definition["metadata"] = metadata
			}
			definitions = append(definitions, definition)
		}
		if len(definitions) > 0 {
			inputFields["tools"] = definitions
		}
	}
	input := rawAGUI(inputFields)
	record, fresh, err := parent.store.Admit(ctx, aguistore.Admission{Principal: parent.run.Principal, ThreadID: childThread, RunID: inv.ID, ParentRunID: parent.run.RunID, TurnID: inv.TurnID, Input: input})
	if err != nil {
		cancel()
		return nil, err
	}
	if record == nil || record.ConversationID != inv.ConversationID || record.TurnID != inv.TurnID {
		cancel()
		return nil, fmt.Errorf("detached invocation native binding mismatch")
	}
	if !fresh {
		cancel()
		return nil, fmt.Errorf("detached invocation already admitted")
	}
	record, err = parent.store.Claim(ctx, record.Principal, record.ThreadID, record.RunID, record.Revision, uuid.NewString(), time.Minute)
	if err != nil {
		cancel()
		return nil, err
	}
	filter := func(e *streaming.Event) bool {
		return e != nil && e.ConversationID == inv.ConversationID && e.TurnID == inv.TurnID
	}
	sub, err := subscribeNativeEvents(ctx, parent.client, &StreamEventsInput{ConversationID: inv.ConversationID, Filter: filter})
	if err != nil {
		cancel()
		return nil, err
	}
	thread, err := parent.store.GetThread(ctx, record.Principal, record.ThreadID)
	if err != nil {
		sub.Close()
		cancel()
		return nil, err
	}
	projection, err := aguistate.New(thread.State, thread.Messages)
	if err != nil {
		sub.Close()
		cancel()
		return nil, err
	}
	translator := agui.NewTranslator(record.ThreadID, record.RunID)
	if err = translator.SetNativeIdentity(record.TurnID); err != nil {
		sub.Close()
		cancel()
		return nil, err
	}
	if err = translator.SeedMessages(aguiObjectMessages(projection.Messages)); err != nil {
		sub.Close()
		cancel()
		return nil, err
	}
	writer := &aguiJournalWriter{client: parent.client, ctx: ctx, store: parent.store, run: record, projection: projection, threadRevision: thread.Revision, leaseOwner: record.LeaseOwner, messageBaseline: append(json.RawMessage(nil), thread.Messages...)}
	start := encodeAGUIEvents(translator.Start())
	var opening map[string]json.RawMessage
	_ = json.Unmarshal(start[0], &opening)
	opening["parentRunId"] = rawAGUI(parent.run.RunID)
	start[0] = rawAGUI(opening)
	if err = writer.write(start, nil); err != nil {
		sub.Close()
		cancel()
		return nil, err
	}
	if err = writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "STATE_SNAPSHOT", "snapshot": projection.State}), rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": projection.Messages})}, nil); err != nil {
		sub.Close()
		cancel()
		return nil, err
	}
	observer := &aguiDetachedObserver{aguiInvocationObserver: newAGUIInvocationObserver(ctx, parent.client, record.ConversationID, record.TurnID), invocation: inv, returned: make(chan aguiInvocationReturn, 1)}
	observer.runtime, observer.store, observer.run = parent.runtime, parent.store, record
	go func() {
		defer cancel()
		defer sub.Close()
		heartbeat := make(chan struct{})
		defer close(heartbeat)
		go aguiRecoverHeartbeat(ctx, cancel, parent.store, record, record.LeaseOwner, heartbeat)
		presentation := &aguiPresentation{}
		nativeQueued := false
		consume := func(event *streaming.Event) error {
			if !filter(event) || event.Type == streaming.EventTypeTurnCompleted {
				return nil
			}
			if err := hydrateAGUITool(ctx, parent.client, event); err != nil {
				return err
			}
			for _, projected := range presentation.project(ctx, event, parent.client) {
				if err := writer.write(encodeAGUIEvents(translator.Translate(projected)), nil); err != nil {
					return err
				}
			}
			return nil
		}
		finish := func(returned requestctx.InvocationResult) error {
			if returned.Error != "" || returned.NativeStatus == "failed" || returned.NativeStatus == "error" {
				if returned.Error == "" {
					returned.Error = "Detached agent run failed"
				}
				return writer.write(encodeAGUIEvents(translator.Fail(returned.Error, "AGENT_ERROR")), nil)
			}
			pending := aguiPending{ClientTools: returned.ClientToolCalls, Dependencies: returned.ClientToolDependencies}
			for i := range pending.ClientTools {
				call := &pending.ClientTools[i]
				if call.TurnID == "" {
					call.TurnID = record.TurnID
				}
				if call.ConversationID == "" {
					call.ConversationID = record.ConversationID
				}
				call.ProtocolID = agui.ProtocolToolCallID(call.TurnID, call.ID)
				if !translator.HasToolCall(call.TurnID, call.ID) {
					if err := writer.write(encodeAGUIEvents(translator.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ConversationID: call.ConversationID, TurnID: call.TurnID, ToolCallID: call.ID, ToolName: call.Name, AssistantMessageID: call.AssistantMessageID, Arguments: call.Arguments})), nil); err != nil {
						return err
					}
				}
			}
			if !translator.HasText() && returned.Content != "" {
				if err := consume(&streaming.Event{Type: streaming.EventTypeAssistant, ConversationID: record.ConversationID, TurnID: record.TurnID, AssistantMessageID: record.RunID + "/answer", Content: returned.Content, Patch: map[string]any{"role": "assistant"}}); err != nil {
					return err
				}
			}
			outcome := map[string]any{"type": "success"}
			if len(pending.ClientTools) > 0 {
				if len(pending.Dependencies) > 0 {
					for _, call := range pending.ClientTools {
						pending.Interrupts = append(pending.Interrupts, aguiClientToolInterrupt(call))
					}
					outcome = map[string]any{"type": "interrupt", "interrupts": pending.Interrupts}
				} else {
					ids := []string{}
					for _, call := range pending.ClientTools {
						ids = append(ids, aguiPendingCallID(call))
					}
					outcome["pendingToolCallIds"] = ids
				}
			}
			if returned.NativeStatus == "waiting_for_user" && len(pending.ClientTools) == 0 {
				inspector, ok := parent.runtime.(aguiRecoveryRuntime)
				if !ok {
					return fmt.Errorf("detached human interrupt requires authoritative receipt inspection")
				}
				native, inspectErr := inspector.aguiInspectRun(ctx, writer.run)
				if inspectErr != nil {
					return inspectErr
				}
				if native == nil || len(native.Pending.Interrupts) == 0 {
					return fmt.Errorf("detached human interrupt has no canonical pending descriptor")
				}
				pending.Interrupts = native.Pending.Interrupts
			}
			if len(pending.Interrupts) > 0 {
				outcome = map[string]any{"type": "interrupt", "interrupts": pending.Interrupts}
			}
			if returned.NativeStatus == "canceled" || returned.NativeStatus == "cancelled" {
				outcome = map[string]any{"type": "cancelled"}
			}
			terminal := encodeAGUIEvents(translator.Finish("success"))
			var end map[string]json.RawMessage
			_ = json.Unmarshal(terminal[len(terminal)-1], &end)
			end["outcome"] = rawAGUI(outcome)
			terminal[len(terminal)-1] = rawAGUI(end)
			return writer.write(terminal, &pending)
		}
		poll := time.NewTicker(500 * time.Millisecond)
		defer poll.Stop()
		for !translator.Done() {
			select {
			case <-ctx.Done():
				return
			case <-poll.C:
				if !nativeQueued {
					continue
				}
				inspector, ok := parent.runtime.(aguiRecoveryRuntime)
				if !ok {
					continue
				}
				native, inspectErr := inspector.aguiInspectRun(ctx, writer.run)
				if inspectErr != nil {
					log.Printf("AG-UI detached inspection: %v", inspectErr)
					continue
				}
				if native == nil {
					continue
				}
				switch strings.ToLower(native.Status) {
				case "queued", "running", "pending", "":
					continue
				}
				result := requestctx.InvocationResult{Invocation: inv, NativeStatus: native.Status, Error: native.Error, ClientToolCalls: native.Pending.ClientTools, ClientToolDependencies: native.Pending.Dependencies}
				for _, message := range native.Messages {
					if message["role"] == "assistant" {
						if content, ok := message["content"].(string); ok {
							result.Content = content
						}
					}
				}
				if err := finish(result); err != nil {
					_ = writer.fail(err)
				}
				return
			case event, open := <-sub.C():
				if !open {
					return
				}
				if err := consume(event); err != nil {
					log.Printf("AG-UI detached observer: %v", err)
					return
				}
			case child := <-observer.events:
				if child.observerErr != nil {
					child.ack <- child.observerErr
					return
				}
				var events []agui.Event
				if child.registered {
					events = translator.RegisterInvocation(child.invocation)
				} else if child.returned != nil {
					result := *child.returned
					result.Content, _ = plainAGUIContent(result.Content, true)
					events = translator.InvocationReturned(result)
				} else {
					for _, event := range presentation.project(ctx, child.event, parent.client) {
						events = append(events, translator.TranslateSubagent(child.invocation, event)...)
					}
				}
				err := writer.write(encodeAGUIEvents(events), nil)
				child.ack <- err
				if err != nil {
					return
				}
			case request := <-observer.returned:
				draining := true
				for draining {
					select {
					case event, open := <-sub.C():
						if !open {
							draining = false
							break
						}
						if err := consume(event); err != nil {
							request.done <- err
							return
						}
					default:
						draining = false
					}
				}
				if request.result.NativeStatus == "queued" || request.result.NativeStatus == "running" {
					nativeQueued = true
					request.done <- nil
					continue
				}
				err := finish(request.result)
				request.done <- err
				if err != nil {
					_ = writer.fail(err)
				}
				return
			}
		}
	}()
	return requestctx.WithInvocationObserver(context.WithoutCancel(ctx), observer), nil
}
