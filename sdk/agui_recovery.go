package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	iauth "github.com/viant/agently-core/internal/auth"
	messagemodel "github.com/viant/agently-core/model/message"
	turnmodel "github.com/viant/agently-core/model/turn"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

// The inspector reads existing Datly-backed native execution. It never starts,
// resumes or repeats execution while attaching a protocol observer.
type aguiRecoveryRuntime interface {
	aguiInspectRun(context.Context, *aguistore.Run) (*aguiRecoveredNative, error)
}
type aguiRecoveredNative struct {
	Status   string
	Error    string
	Messages []aguistate.Object
	Pending  aguiPending
}

// recoverAGUIDurable reattaches an expired protocol observer to its existing
// logical turn. A claim guards journal writes across process boundaries.
func recoverAGUIDurable(ctx context.Context, client Client, runtime aguiRuntime, store aguistore.Store, record *aguistore.Run) (retErr error) {
	if client == nil || runtime == nil || store == nil || record == nil {
		return fmt.Errorf("AG-UI recovery requires runtime, store and run")
	}
	latest, err := store.GetRun(ctx, record.Principal, record.ThreadID, record.RunID)
	if err != nil {
		return err
	}
	if aguiRunTerminal(latest.Status) {
		return nil
	}
	if latest.Status != aguistore.StatusRunning {
		return fmt.Errorf("AG-UI recovery requires a running journal")
	}
	inspector, ok := runtime.(aguiRecoveryRuntime)
	if !ok {
		return fmt.Errorf("backend cannot inspect durable native execution")
	}
	owner := uuid.NewString()
	claimed, err := store.Claim(ctx, latest.Principal, latest.ThreadID, latest.RunID, latest.Revision, owner, time.Minute)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopHeartbeat := make(chan struct{})
	defer close(stopHeartbeat)
	go aguiRecoverHeartbeat(ctx, cancel, store, claimed, owner, stopHeartbeat)
	journal, err := aguiRecoveryJournal(ctx, store, claimed)
	if err != nil {
		return err
	}
	thread, err := store.GetThread(ctx, claimed.Principal, claimed.ThreadID)
	if err != nil {
		return err
	}
	projection, err := aguiRecoveryProjection(thread, journal)
	if err != nil {
		return err
	}
	translator, err := agui.RestoreTranslator(claimed.ThreadID, claimed.RunID, journal)
	if err != nil {
		return err
	}
	writer := &aguiJournalWriter{client: client, ctx: ctx, store: store, run: claimed, projection: projection, threadRevision: thread.Revision, leaseOwner: owner, messageBaseline: rawAGUI(projection.Messages)}
	defer func() {
		if retErr != nil && !aguiRunTerminal(writer.run.Status) {
			// A cancelled observer or lost lease must not cancel native execution
			// or turn a recoverable observer disconnect into a permanent error.
			if !errors.Is(retErr, context.Canceled) && !errors.Is(retErr, context.DeadlineExceeded) && !errors.Is(retErr, aguistore.ErrConflict) && !errors.Is(retErr, errAGUIObserverLost) {
				_ = writer.fail(retErr)
			}
		}
	}()
	activeInvocations := translator.RecoverableInvocations()
	rootFilter := func(e *streaming.Event) bool {
		return e != nil && (e.ConversationID == claimed.ConversationID || e.ConversationID == "" && e.StreamID == claimed.ConversationID) && e.TurnID == claimed.TurnID
	}
	childFor := func(e *streaming.Event) *requestctx.Invocation {
		if e == nil {
			return nil
		}
		for i := range activeInvocations {
			invocation := &activeInvocations[i]
			if !invocation.Detached && invocation.ConversationID == e.ConversationID && invocation.TurnID == e.TurnID {
				return invocation
			}
		}
		return nil
	}
	filter := func(e *streaming.Event) bool { return rootFilter(e) || childFor(e) != nil }
	// Subscribe before inspecting so a native terminal cannot fall between the
	// inspection and attachment. Polling also observes other-process completion.
	sub, err := subscribeNativeEvents(ctx, client, &StreamEventsInput{ConversationID: claimed.ConversationID, Filter: filter})
	if err != nil {
		return err
	}
	defer sub.Close()
	inspect := func() (*aguiRecoveredNative, error) { return inspector.aguiInspectRun(ctx, claimed) }
	native, err := inspect()
	if err != nil {
		return err
	}
	presentation := &aguiPresentation{}
	if native != nil {
		presentation.prefill(claimed.ConversationID, claimed.TurnID, "", projection.Messages, native.Messages)
	}
	if childInspector, ok := runtime.(aguiInvocationInspector); ok {
		for _, inv := range activeInvocations {
			child, inspectErr := childInspector.aguiInspectInvocation(ctx, claimed, inv)
			if inspectErr != nil {
				return inspectErr
			}
			if child != nil {
				presentation.prefill(inv.ConversationID, inv.TurnID, inv.ID, projection.Messages, child.Messages)
			}
		}
	}
	if native == nil {
		if !aguiRecoveryBeforeDispatch(journal) {
			return fmt.Errorf("native execution missing after dispatch boundary; result is uncertain and execution was not repeated")
		}
		var input agui.RunAgentInput
		if err = json.Unmarshal(claimed.Input, &input); err != nil {
			return err
		}
		var props struct {
			Agently *agui.Extension `json:"agently"`
		}
		if len(input.ForwardedProps) > 0 {
			_ = json.Unmarshal(input.ForwardedProps, &props)
		}
		var prior *aguistore.Run
		var pending aguiPending
		if claimed.PriorRunID != "" {
			prior, err = store.GetRun(ctx, claimed.Principal, claimed.ThreadID, claimed.PriorRunID)
			if err != nil {
				return err
			}
			if err = json.Unmarshal(prior.Pending, &pending); err != nil {
				return err
			}
		}
		query, err := queryForAGUI(&input, claimed.Principal, claimed.TurnID, props.Agently, prior)
		if err != nil {
			return err
		}
		session, err := aguiClientToolSession(&input)
		if err != nil {
			return err
		}
		if err = translator.SeedMessages(aguiObjectMessages(projection.Messages)); err != nil {
			return err
		}
		if err = writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "STATE_SNAPSHOT", "snapshot": projection.State}), rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": projection.Messages})}, nil); err != nil {
			return err
		}
		_ = sub.Close() // execution hook owns its own subscribe-before-execute.
		return executeAGUIRuntime(clienttool.WithSession(ctx, session), client, runtime, writer, translator, &input, query, pending, prior)
	}
	if handled, e := aguiRecoverClientContinuation(ctx, client, runtime, writer, translator, claimed, native); handled || e != nil {
		return e
	}
	finish := func(native *aguiRecoveredNative) (bool, error) {
		if native == nil {
			return false, fmt.Errorf("existing native execution disappeared during recovery")
		}
		switch strings.ToLower(strings.TrimSpace(native.Status)) {
		case "completed", "finished", "success", "succeeded", "failed", "error", "canceled", "cancelled", "waiting_for_user", "blocked":
		default:
			return false, nil
		}
		settled, childErr := aguiReconcileRecoveredChildren(ctx, runtime, writer, translator, native)
		if childErr != nil {
			return false, childErr
		}
		if !settled && native.Status != "failed" && native.Status != "error" && native.Status != "canceled" && native.Status != "cancelled" {
			return false, nil
		}
		return true, aguiFinishRecovered(writer, translator, native)
	}
	if done, e := finish(native); done || e != nil {
		return e
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, open := <-sub.C():
			if !open {
				return aguiObserverLost(sub.Reason())
			}
			if !filter(event) {
				continue
			}
			if invocation := childFor(event); invocation != nil && !rootFilter(event) {
				copy := *event
				if err = hydrateAGUITool(ctx, client, &copy); err != nil {
					return err
				}
				for _, projected := range presentation.project(ctx, &copy, client) {
					if err = writer.write(encodeAGUIEvents(translator.TranslateSubagent(*invocation, projected)), nil); err != nil {
						return err
					}
				}
				continue
			}
			switch event.Type {
			case streaming.EventTypeTurnCompleted, streaming.EventTypeTurnFailed, streaming.EventTypeTurnCanceled:
				native, e := inspect()
				if e != nil {
					return e
				}
				if done, e := finish(native); done || e != nil {
					return e
				}
				continue // native persistence may not yet expose the terminal.
			}
			copy := *event
			if copy.AssistantMessageID == claimed.TurnID {
				copy.AssistantMessageID = ""
			}
			if err = hydrateAGUITool(ctx, client, &copy); err != nil {
				return err
			}
			for _, projected := range presentation.project(ctx, &copy, client) {
				if err = writer.write(encodeAGUIEvents(translator.Translate(projected)), nil); err != nil {
					return err
				}
			}
			if translator.Done() {
				return nil
			}
		case <-ticker.C:
			native, e := inspect()
			if e != nil {
				return e
			}
			if done, e := finish(native); done || e != nil {
				return e
			}
		}
	}
}
func aguiRecoverHeartbeat(ctx context.Context, cancel context.CancelFunc, store aguistore.Store, run *aguistore.Run, owner string, stop <-chan struct{}) {
	revision := run.LeaseRevision
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			next, err := store.Renew(ctx, run.Principal, run.ThreadID, run.RunID, revision, owner, time.Minute)
			if err != nil {
				cancel()
				return
			}
			revision = next.LeaseRevision
		}
	}
}
func aguiRecoveryJournal(ctx context.Context, store aguistore.Store, run *aguistore.Run) ([]json.RawMessage, error) {
	var raw []json.RawMessage
	var after int64
	for after < run.LastSequence {
		page, err := store.Replay(ctx, run.Principal, run.ThreadID, run.RunID, after, 500)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return nil, fmt.Errorf("durable AG-UI journal is incomplete")
		}
		for _, event := range page {
			if event.Sequence != after+1 {
				return nil, fmt.Errorf("durable AG-UI journal sequence gap")
			}
			raw = append(raw, event.Event)
			after = event.Sequence
			if after == run.LastSequence {
				break
			}
		}
	}
	return raw, nil
}
func aguiRecoveryProjection(thread *aguistore.Thread, journal []json.RawMessage) (*aguistate.Projection, error) {
	baseline := false
	for _, raw := range journal {
		var e struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		if e.Type == "MESSAGES_SNAPSHOT" {
			baseline = true
		}
	}
	var state, messages json.RawMessage
	if !baseline {
		state, messages = thread.State, thread.Messages
	}
	p, err := aguistate.New(state, messages)
	if err != nil {
		return nil, err
	}
	for _, raw := range journal {
		if _, err = p.Apply(raw); err != nil {
			return nil, err
		}
	}
	return p, nil
}
func aguiRecoveryBeforeDispatch(journal []json.RawMessage) bool {
	if len(journal) != 1 {
		return false
	}
	var e struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(journal[0], &e) == nil && e.Type == "RUN_STARTED"
}
func aguiObjectMessages(messages []aguistate.Object) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, rawAGUI(message))
	}
	return result
}
func aguiFinishRecovered(writer *aguiJournalWriter, translator *agui.Translator, native *aguiRecoveredNative) error {
	native = aguiSanitizeRecoveredMessages(writer, native)
	merged := aguiMergeRecoveryMessages(writer.projection.Messages, native.Messages)
	previousIDs := map[string]bool{}
	knownCalls := map[string]bool{}
	for _, message := range writer.projection.Messages {
		if id, ok := message["id"].(string); ok {
			previousIDs[id] = true
		}
		for _, call := range aguiRecoveryCalls(message) {
			knownCalls[fmt.Sprint(call["id"])] = true
		}
	}
	var newStarts, newEnds, results []json.RawMessage
	var snapshot []aguistate.Object
	for _, message := range merged {
		id, _ := message["id"].(string)
		if message["role"] == "tool" && !previousIDs[id] {
			results = append(results, rawAGUI(map[string]any{"type": "TOOL_CALL_RESULT", "messageId": id, "toolCallId": message["toolCallId"], "content": message["content"]}))
			continue
		}
		snapshot = append(snapshot, message)
		for _, call := range aguiRecoveryCalls(message) {
			callID, _ := call["id"].(string)
			if knownCalls[callID] {
				continue
			}
			knownCalls[callID] = true
			function := aguiRecoveryObject(call["function"])
			opener := map[string]any{"type": "TOOL_CALL_START", "toolCallId": callID, "toolCallName": function["name"], "parentMessageId": id}
			end := map[string]any{"type": "TOOL_CALL_END", "toolCallId": callID}
			if owner, ok := message["subagentRunId"].(string); ok {
				opener["subagentRunId"] = owner
				end["subagentRunId"] = owner
			}
			newStarts = append(newStarts, rawAGUI(opener))
			newEnds = append(newEnds, rawAGUI(end))
		}
	}
	// Install authoritative arguments before END callbacks can dispatch client
	// tools. Newly recovered results remain explicit RESULT events so upstream
	// clients mark the calls answered rather than infer another client handoff.
	if err := writer.write(newStarts, nil); err != nil {
		return err
	}
	if err := writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": snapshot})}, nil); err != nil {
		return err
	}
	outcome := map[string]any{"type": "success"}
	status := strings.ToLower(strings.TrimSpace(native.Status))
	var terminal []json.RawMessage
	if status == "failed" || status == "error" {
		message := native.Error
		if message == "" {
			message = "native execution failed"
		}
		terminal = encodeAGUIEvents(translator.Fail(message, "AGENT_ERROR"))
	} else {
		if status == "canceled" || status == "cancelled" {
			outcome["type"] = "cancelled"
		}
		if len(native.Pending.Dependencies) > 0 || len(native.Pending.Interrupts) > 0 {
			known := map[string]bool{}
			for _, interrupt := range native.Pending.Interrupts {
				known[interrupt.ID] = true
			}
			for _, call := range native.Pending.ClientTools {
				if !known[aguiPendingCallID(call)] {
					native.Pending.Interrupts = append(native.Pending.Interrupts, aguiClientToolInterrupt(call))
				}
			}
		}
		if len(native.Pending.Interrupts) > 0 && outcome["type"] != "cancelled" {
			outcome = map[string]any{"type": "interrupt", "interrupts": native.Pending.Interrupts}
		}
		if len(native.Pending.ClientTools) > 0 && outcome["type"] == "success" {
			ids := make([]string, 0, len(native.Pending.ClientTools))
			for _, call := range native.Pending.ClientTools {
				ids = append(ids, aguiPendingCallID(call))
			}
			outcome["pendingToolCallIds"] = ids
		}
		if (status == "waiting_for_user" || status == "blocked") && len(native.Pending.Interrupts) == 0 && len(native.Pending.ClientTools) == 0 {
			return fmt.Errorf("native run is waiting but its continuation could not be recovered")
		}
		terminal = encodeAGUIEvents(translator.Finish("success"))
		if len(terminal) == 0 {
			return fmt.Errorf("recovered translator was already terminal")
		}
		var end map[string]any
		if err := decodeAGUIValue(terminal[len(terminal)-1], &end); err != nil {
			return err
		}
		end["outcome"] = outcome
		terminal[len(terminal)-1] = rawAGUI(end)
	}
	if len(terminal) == 0 {
		return fmt.Errorf("recovered translator was already terminal")
	}
	raw := append([]json.RawMessage(nil), terminal[:len(terminal)-1]...)
	raw = append(raw, newEnds...)
	raw = append(raw, results...)
	raw = append(raw, terminal[len(terminal)-1])
	return writer.write(raw, &native.Pending)
}
func aguiRecoverClientContinuation(ctx context.Context, client Client, runtime aguiRuntime, writer *aguiJournalWriter, translator *agui.Translator, record *aguistore.Run, native *aguiRecoveredNative) (bool, error) {
	if record.PriorRunID == "" || native == nil {
		return false, nil
	}
	prior, err := writer.store.GetRun(ctx, record.Principal, record.ThreadID, record.PriorRunID)
	if err != nil {
		return false, err
	}
	var pending aguiPending
	if err = json.Unmarshal(prior.Pending, &pending); err != nil {
		return false, err
	}
	if len(pending.ClientTools) == 0 && len(pending.Interrupts) == 0 {
		return false, nil
	}
	status := strings.ToLower(strings.TrimSpace(native.Status))
	if status != "waiting_for_user" && status != "blocked" {
		switch status {
		case "completed", "finished", "success", "succeeded", "failed", "error", "canceled", "cancelled":
			return false, nil
		}
		if len(pending.Interrupts) == 0 {
			return false, nil
		}
	}
	if len(pending.Interrupts) > 0 {
		if _, ok := client.(interface {
			aguiApplyInterrupt(context.Context, *aguistore.Run, agui.WireInterrupt, agui.WireResumeEntry) (aguiInterruptDisposition, error)
		}); !ok {
			return false, fmt.Errorf("checked human continuation recovery unavailable")
		}
	}
	var input agui.RunAgentInput
	if err = json.Unmarshal(record.Input, &input); err != nil {
		return false, err
	}
	var original struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err = json.Unmarshal(record.Input, &original); err != nil {
		return false, err
	}
	var resumes []agui.WireResumeEntry
	if len(input.Resume) > 0 {
		if err = json.Unmarshal(input.Resume, &resumes); err != nil {
			return false, err
		}
	}
	expected := map[string]bool{}
	for _, interrupt := range pending.Interrupts {
		expected[interrupt.ID] = true
	}
	seen := map[string]bool{}
	for _, answer := range resumes {
		if !expected[answer.InterruptId] || seen[answer.InterruptId] {
			return false, fmt.Errorf("invalid accepted interrupt identity %q", answer.InterruptId)
		}
		seen[answer.InterruptId] = true
	}
	for id := range expected {
		if !seen[id] {
			return false, fmt.Errorf("accepted continuation missing interrupt answer %q", id)
		}
	}
	var imported []aguistate.Object
	for _, call := range pending.ClientTools {
		found := false
		for _, raw := range original.Messages {
			var message aguistate.Object
			if err = decodeAGUIValue(raw, &message); err != nil {
				return false, err
			}
			if message["role"] == "tool" && message["toolCallId"] == aguiPendingCallID(call) {
				imported = append(imported, message)
				found = true
				break
			}
		}
		if !found {
			for _, answer := range resumes {
				if answer.InterruptId != aguiPendingCallID(call) {
					continue
				}
				content, toolError, e := aguiClientToolAnswer(call, answer)
				if e != nil {
					return false, e
				}
				var value any
				if e = decodeAGUIValue(content, &value); e != nil {
					return false, e
				}
				message := aguistate.Object{"id": call.ToolMessageID, "role": "tool", "toolCallId": aguiPendingCallID(call), "content": value}
				if toolError != "" {
					message["error"] = toolError
				}
				imported = append(imported, message)
				found = true
				break
			}
			if !found {
				return false, fmt.Errorf("accepted continuation is missing result for %q", aguiPendingCallID(call))
			}
		}
	}
	messages := aguiMergeRecoveryMessages(writer.projection.Messages, imported)
	if err = writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": messages})}, nil); err != nil {
		return false, err
	}
	var props struct {
		Agently *agui.Extension `json:"agently"`
	}
	if len(input.ForwardedProps) > 0 {
		_ = json.Unmarshal(input.ForwardedProps, &props)
	}
	query, err := queryForAGUI(&input, record.Principal, record.TurnID, props.Agently, prior)
	if err != nil {
		return false, err
	}
	session, err := aguiClientToolSession(&input)
	if err != nil {
		return false, err
	}
	// Native ResumeContinuation performs its own conditional persisted turn
	// claim; tool completion accepts only the identical previously stored result.
	return true, executeAGUIRuntime(clienttool.WithSession(ctx, session), client, runtime, writer, translator, &input, query, pending, prior)
}

func aguiRecoveryObject(value any) aguistate.Object {
	switch v := value.(type) {
	case aguistate.Object:
		return v
	case map[string]any:
		return aguistate.Object(v)
	}
	return nil
}
func aguiMergeRecoveryMessages(existing, incoming []aguistate.Object) []aguistate.Object {
	result := append([]aguistate.Object(nil), existing...)
	for _, fresh := range incoming {
		id, _ := fresh["id"].(string)
		if id == "" {
			continue
		}
		// Existing wire aliases win over native physical identifiers. This also
		// keeps an imported frontend tool result attached to its original ID.
		if fresh["role"] == "tool" {
			for _, old := range result {
				if old["role"] == "tool" && old["toolCallId"] == fresh["toolCallId"] {
					id, _ = old["id"].(string)
					break
				}
			}
		} else if calls, ok := fresh["toolCalls"].([]any); ok {
			for _, call := range calls {
				candidate := aguiRecoveryObject(call)
				for _, old := range result {
					for _, saved := range aguiRecoveryCalls(old) {
						if candidate != nil && saved["id"] == candidate["id"] {
							id, _ = old["id"].(string)
						}
					}
				}
			}
		}
		found := -1
		for i, old := range result {
			if old["id"] == id {
				found = i
				break
			}
		}
		merged := aguistate.Object{}
		if found >= 0 {
			for key, value := range result[found] {
				merged[key] = value
			}
		}
		for key, value := range fresh {
			if key == "id" {
				continue
			}
			if key == "toolCalls" {
				merged[key] = aguiRecoveryMergeCalls(merged[key], value)
			} else {
				merged[key] = value
			}
		}
		merged["id"] = id
		if found >= 0 {
			result[found] = merged
		} else {
			result = append(result, merged)
		}
	}
	var tools, ordered []aguistate.Object
	for _, message := range result {
		if message["role"] == "tool" {
			tools = append(tools, message)
		} else {
			ordered = append(ordered, message)
		}
	}
	for _, message := range tools {
		position := len(ordered)
		found := false
		for i, assistant := range ordered {
			for _, call := range aguiRecoveryCalls(assistant) {
				if call["id"] == message["toolCallId"] {
					position = i + 1
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		for position < len(ordered) && ordered[position]["role"] == "tool" {
			position++
		}
		ordered = append(ordered, nil)
		copy(ordered[position+1:], ordered[position:])
		ordered[position] = message
	}
	return ordered
}
func aguiRecoveryCalls(message aguistate.Object) []aguistate.Object {
	calls, _ := message["toolCalls"].([]any)
	result := make([]aguistate.Object, 0, len(calls))
	for _, value := range calls {
		if call := aguiRecoveryObject(value); call != nil {
			result = append(result, call)
		}
	}
	return result
}
func aguiRecoveryMergeCalls(before, after any) []any {
	old, _ := before.([]any)
	incoming, _ := after.([]any)
	result := append([]any(nil), old...)
	for _, value := range incoming {
		fresh := aguiRecoveryObject(value)
		if fresh == nil {
			continue
		}
		position := -1
		merged := aguistate.Object{}
		for i, entry := range result {
			call := aguiRecoveryObject(entry)
			if call != nil && call["id"] == fresh["id"] {
				position = i
				for key, value := range call {
					merged[key] = value
				}
				break
			}
		}
		for key, value := range fresh {
			if key == "function" {
				fields := aguistate.Object{}
				for k, v := range aguiRecoveryObject(merged[key]) {
					fields[k] = v
				}
				for k, v := range aguiRecoveryObject(value) {
					if k == "name" && fields[k] != nil {
						continue
					}
					fields[k] = v
				}
				merged[key] = fields
			} else {
				merged[key] = value
			}
		}
		if position >= 0 {
			result[position] = merged
		} else {
			result = append(result, merged)
		}
	}
	return result
}

// cancelAGUIRun binds cancellation to an authorized durable mapping. There is
// deliberately no caller-supplied native turn ID in this operation.
func cancelAGUIRun(ctx context.Context, client Client, store aguistore.Store, principal, threadID, runID string) (bool, error) {
	if client == nil || store == nil || strings.TrimSpace(principal) == "" {
		return false, fmt.Errorf("authorized AG-UI cancellation scope is required")
	}
	if user := iauth.EffectiveUserID(ctx); user != "" && user != principal {
		return false, fmt.Errorf("AG-UI cancellation principal mismatch")
	}
	record, err := store.GetRun(ctx, principal, threadID, runID)
	if err != nil {
		return false, err
	}
	if record.Principal != principal || record.ThreadID != threadID || record.RunID != runID || record.TurnID == "" {
		return false, fmt.Errorf("AG-UI cancellation mapping is invalid")
	}
	inspector, ok := client.(aguiRecoveryRuntime)
	if !ok {
		return false, fmt.Errorf("backend cannot inspect native cancellation scope")
	}
	native, err := inspector.aguiInspectRun(ctx, record)
	if err != nil {
		return false, err
	}
	if native == nil {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(native.Status)) {
	case "queued":
		if err = client.CancelQueuedTurn(ctx, record.ConversationID, record.TurnID); err != nil {
			return false, err
		}
		return true, nil
	case "canceled", "cancelled":
		// A prior cancellation can have committed its run fence before native
		// tool/starter/turn cleanup completed. The native method repairs only the
		// already-canceled identity and returns false when fully terminal.
		return client.CancelTurn(ctx, record.TurnID)
	case "completed", "finished", "failed", "error":
		return false, nil
	default:
		return client.CancelTurn(ctx, record.TurnID)
	}
}

func (c *backendClient) aguiInspectRun(ctx context.Context, record *aguistore.Run) (*aguiRecoveredNative, error) {
	if c == nil || c.data == nil || c.conv == nil || record == nil || record.ConversationID == "" {
		return nil, fmt.Errorf("native AG-UI inspection is unavailable")
	}
	useAliases := false
	if record.LastSequence > 0 && c.goalInvoker != nil {
		page, err := c.aguiStore().Replay(ctx, record.Principal, record.ThreadID, record.RunID, 0, 1)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return nil, fmt.Errorf("durable identity journal is missing")
		}
		var header struct {
			Type     string `json:"type"`
			Metadata struct {
				Agently struct {
					Version string `json:"identityVersion"`
					TurnID  string `json:"nativeTurnId"`
				} `json:"agently"`
			} `json:"metadata"`
		}
		if err = json.Unmarshal(page[0].Event, &header); err != nil {
			return nil, err
		}
		if header.Type == "RUN_STARTED" && header.Metadata.Agently.Version == "1" {
			if header.Metadata.Agently.TurnID != record.TurnID {
				return nil, fmt.Errorf("native identity journal scope mismatch")
			}
			useAliases = true
		}
	}
	turn, err := c.data.GetTurnByID(ctx, &turnmodel.TurnLookupInput{ID: record.TurnID, ConversationID: record.ConversationID, Has: &turnmodel.TurnLookupInputHas{ID: true, ConversationID: true}}, principalDataOpts(ctx)...)
	if err != nil {
		if isTurnLookupUnavailable(err) {
			return nil, nil
		}
		return nil, err
	}
	if turn == nil {
		return nil, nil
	}
	if turn.Id != record.TurnID || turn.ConversationId != record.ConversationID {
		return nil, fmt.Errorf("native turn does not match durable AG-UI scope")
	}
	result := &aguiRecoveredNative{Status: turn.Status, Error: valueOrEmpty(turn.ErrorMessage)}
	// Read through existing paginated Datly components; do not open a database
	// connection or derive protocol history from legacy presentation rows.
	var rows []*messagemodel.MessageRowsView
	cursor := ""
	for {
		in := &messagemodel.MessageRowsInput{ConversationId: record.ConversationID, TurnId: record.TurnID, Roles: []string{"assistant", "tool", "control"}, Has: &messagemodel.MessageRowsInputHas{ConversationId: true, TurnId: true, Roles: true}}
		page, e := c.data.GetMessagesPage(ctx, in, &data.PageInput{Limit: 500, Direction: data.DirectionBefore, Cursor: cursor}, principalDataOpts(ctx)...)
		if e != nil {
			return nil, e
		}
		if page == nil {
			return nil, fmt.Errorf("native message page is unavailable")
		}
		rows = append(rows, page.Rows...)
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return nil, fmt.Errorf("native message cursor did not advance")
		}
		cursor = page.NextCursor
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].Id < rows[j].Id
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	var input agui.RunAgentInput
	if err = json.Unmarshal(record.Input, &input); err != nil {
		return nil, err
	}
	approvals, err := c.aguiPendingApprovalInterrupts(ctx, record.ConversationID, record.TurnID)
	if err != nil {
		return nil, err
	}
	approvalCalls := map[string]bool{}
	for i := range approvals {
		if approvals[i].ToolCallID != nil {
			id := *approvals[i].ToolCallID
			approvalCalls[id] = true
			if useAliases {
				public := agui.ProtocolToolCallID(record.TurnID, id)
				approvals[i].ToolCallID = &public
			}
		}
	}
	definitions := map[string]string{}
	for _, tool := range input.Tools {
		definitions[strings.ToLower(mcpname.Canonical(tool.Name))] = tool.Name
	}
	byID := map[string]aguistate.Object{}
	add := func(m aguistate.Object) {
		id, _ := m["id"].(string)
		if prior := byID[id]; prior != nil {
			for k, v := range m {
				prior[k] = v
			}
			return
		}
		byID[id] = m
		result.Messages = append(result.Messages, m)
	}
	for _, row := range rows {
		if row == nil || row.ConversationId != record.ConversationID || valueOrEmpty(row.TurnId) != record.TurnID {
			return nil, fmt.Errorf("native message scope mismatch")
		}
		if row.Archived != nil && *row.Archived == 1 || streaming.IsInternalMessageMode(valueOrEmpty(row.Mode)) {
			continue
		}
		msg, e := c.conv.GetMessage(ctx, row.Id, conversation.WithIncludeToolCall(true), conversation.WithIncludeModelCall(true))
		if e != nil {
			return nil, e
		}
		if msg == nil {
			return nil, fmt.Errorf("native message disappeared")
		}
		if msg.ConversationId != record.ConversationID || valueOrEmpty(msg.TurnId) != record.TurnID {
			return nil, fmt.Errorf("native message graph scope mismatch")
		}
		if msg.Role == "assistant" && msg.Type == "text" {
			text := valueOrEmpty(msg.Content)
			if msg.Content == nil {
				text = valueOrEmpty(msg.RawContent)
			}
			add(aguistate.Object{"id": msg.Id, "role": "assistant", "content": text})
		}
		if msg.ElicitationId != nil && strings.EqualFold(valueOrEmpty(msg.Status), "pending") {
			payload := c.resolveElicitationPayload(ctx, *msg.ElicitationId, valueOrEmpty(msg.ElicitationPayloadId), valueOrEmpty(msg.Content))
			interrupt := agui.WireInterrupt{ID: *msg.ElicitationId, Reason: "elicitation"}
			text := valueOrEmpty(msg.Content)
			interrupt.Message = &text
			if rawSchema, ok := payload["requestedSchema"]; ok {
				var schema map[string]json.RawMessage
				if e = json.Unmarshal(rawAGUI(rawSchema), &schema); e != nil {
					return nil, e
				}
				interrupt.ResponseSchema = &schema
			}
			result.Pending.Interrupts = append(result.Pending.Interrupts, interrupt)
		}
		call := msg.MessageToolCall
		if msg.Role != "tool" || call == nil {
			continue
		}
		args := "{}"
		if call.MessageRequestPayload != nil && call.MessageRequestPayload.InlineBody != nil {
			args = conversation.DecodeInlineBody(*call.MessageRequestPayload.InlineBody, call.MessageRequestPayload.Compression)
		}
		var arguments map[string]interface{}
		if e = decodeAGUIValue([]byte(args), &arguments); e != nil {
			return nil, fmt.Errorf("native tool arguments unavailable: %w", e)
		}
		parent := valueOrEmpty(msg.ParentMessageId)
		if parent == "" {
			parent = record.RunID + "/assistant"
		}
		owner := byID[parent]
		if owner == nil {
			owner = aguistate.Object{"id": parent, "role": "assistant"}
			add(owner)
		}
		name := mcpname.Display(call.ToolName)
		if declared := definitions[strings.ToLower(mcpname.Canonical(call.ToolName))]; declared != "" {
			name = declared
		}
		publicID := call.OpId
		if useAliases {
			publicID = agui.ProtocolToolCallID(record.TurnID, call.OpId)
		}
		calls, _ := owner["toolCalls"].([]any)
		owner["toolCalls"] = append(calls, map[string]any{"id": publicID, "type": "function", "function": map[string]any{"name": name, "arguments": args}})
		if !approvalCalls[call.OpId] && call.Status == "waiting_for_user" && definitions[strings.ToLower(mcpname.Canonical(call.ToolName))] != "" {
			result.Pending.ClientTools = append(result.Pending.ClientTools, clienttool.PendingCall{ID: call.OpId, ProtocolID: publicID, Name: name, Arguments: arguments, ToolMessageID: msg.Id, AssistantMessageID: parent, ConversationID: record.ConversationID, TurnID: record.TurnID, Iteration: aguiRecoveryInt(call.Iteration)})
		} else if !agui.ToolStatusPending(valueOrEmpty(msg.Status)) && (call.Status == "completed" || call.Status == "failed" || call.Status == "canceled") {
			var content any = msg.GetContent()
			if valueOrEmpty(msg.ContextSummary) == clienttool.ContentMIME {
				if e = decodeAGUIValue([]byte(msg.GetContent()), &content); e != nil {
					return nil, e
				}
			}
			add(aguistate.Object{"id": msg.Id, "role": "tool", "toolCallId": publicID, "content": content})
		}
	}
	result.Pending.Interrupts = append(result.Pending.Interrupts, approvals...)

	return result, nil
}

func aguiRecoveryInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
