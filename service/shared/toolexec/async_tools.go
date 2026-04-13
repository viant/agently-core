package toolexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/logx"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/tool"
	toolasyncconfig "github.com/viant/agently-core/protocol/tool/asyncconfig"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
	"github.com/viant/agently-core/service/shared/asyncwait"
)

func WithAsyncWaitState(ctx context.Context) context.Context {
	return asyncwait.WithState(ctx)
}

func ConsumeAsyncWaitAfterStatus(ctx context.Context) []string {
	return asyncwait.ConsumeAfterStatus(ctx)
}

func asyncConfigForStep(ctx context.Context, reg tool.Registry, name string) (*asynccfg.Config, bool) {
	if cfg, ok := toolasyncconfig.ConfigFor(ctx, name); ok {
		return cfg, true
	}
	if reg == nil {
		return nil, false
	}
	resolver, ok := reg.(tool.AsyncResolver)
	if !ok {
		return nil, false
	}
	return resolver.AsyncConfig(name)
}

func prepareAsyncStartArgs(cfg *asynccfg.Config, args map[string]interface{}) map[string]interface{} {
	cloned := map[string]interface{}{}
	for key, value := range args {
		cloned[key] = value
	}
	if cfg == nil {
		return cloned
	}
	for key, value := range cfg.Run.ExtraArgs {
		cloned[key] = value
	}
	return cloned
}

func maybeHandleAsyncTool(ctx context.Context, reg tool.Registry, step StepInfo, toolResult string, execErr error) *asynccfg.OperationRecord {
	if execErr != nil || strings.TrimSpace(toolResult) == "" {
		return nil
	}
	manager, ok := AsyncManagerFromContext(ctx)
	if !ok {
		return nil
	}
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok {
		return nil
	}
	cfg, ok := asyncConfigForStep(ctx, reg, step.Name)
	if !ok || cfg == nil {
		logx.DebugCtxf(ctx, "conversation", "tool async skip convo=%q turn=%q op_id=%q tool=%q reason=no_async_config", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name))
		return nil
	}
	requestDigest := requestArgsDigest(cfg, step.Args)
	var matched *asynccfg.OperationRecord
	if sameToolName(step.Name, cfg.Run.Tool) && sameToolName(step.Name, cfg.Status.Tool) && cfg.Status.ReuseRunArgs {
		matched, _ = manager.FindActiveByRequest(ctx, turn.ConversationID, turn.TurnID, step.Name, requestDigest)
		if matched != nil {
			logx.InfoCtxf(ctx, "conversation", "tool async same-tool recall matched convo=%q turn=%q op_id=%q tool=%q async_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(matched.ID))
		}
	}
	switch {
	case matched != nil:
		payload, err := asynccfg.ExtractPayload(toolResult, cfg.Status.Selector)
		if err != nil || payload == nil {
			logx.WarnCtxf(ctx, "conversation", "tool async same-tool recall ignored convo=%q turn=%q op_id=%q tool=%q async_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(matched.ID), err)
			return nil
		}
		rec, changed := manager.Update(ctx, asynccfg.UpdateInput{
			ID:      matched.ID,
			Status:  payload.Status,
			Message: payload.Message,
			Percent: payload.Percent,
			KeyData: cloneRaw(payload.KeyData),
			Error:   payload.Error,
		})
		if rec != nil {
			patchAsyncToolPersistence(context.Background(), convFromContext(ctx), rec, "", payload)
		}
		if rec != nil {
			asyncwait.MarkAfterStatus(ctx, matched.ID)
		}
		if changed {
			publishAsyncUpdateEvent(ctx, step.Name, matched.ID, payload, rec)
		}
		return nil
	case sameToolName(step.Name, cfg.Run.Tool):
		opID := strings.TrimSpace(extractAsyncOperationID(toolResult, cfg.Run.OperationIDPath))
		if opID == "" {
			opID = synthesizeAsyncOperationID(step, requestDigest)
		}
		extracted := &asynccfg.Extracted{}
		if cfg.Run.Selector != nil {
			if payload, err := asynccfg.ExtractPayload(toolResult, *cfg.Run.Selector); err == nil && payload != nil {
				extracted = payload
			}
		}
		normalizeAsyncExtracted(toolResult, extracted)
		rec := manager.Register(ctx, asynccfg.RegisterInput{
			ID:                            opID,
			ParentConvID:                  turn.ConversationID,
			ParentTurnID:                  turn.TurnID,
			ToolCallID:                    step.ID,
			ToolMessageID:                 strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)),
			ToolName:                      step.Name,
			StatusToolName:                strings.TrimSpace(cfg.Status.Tool),
			StatusArgs:                    asyncStatusArgs(cfg, opID, step.Args),
			CancelToolName:                asyncCancelToolName(cfg),
			RequestArgsDigest:             requestDigest,
			RequestArgs:                   normalizedAsyncArgs(cfg, step.Args),
			WaitForResponse:               cfg.WaitForResponse,
			Status:                        extracted.Status,
			Message:                       extracted.Message,
			Percent:                       extracted.Percent,
			KeyData:                       cloneRaw(extracted.KeyData),
			Error:                         extracted.Error,
			TimeoutMs:                     cfg.TimeoutMs,
			PollIntervalMs:                cfg.PollIntervalMs,
			MaxReinforcementsPerOperation: cfg.MaxReinforcementsPerOperation,
			MinIntervalBetweenMs:          cfg.MinIntervalBetweenMs,
			Reinforcement:                 cfg.Reinforcement,
			ReinforcementPrompt:           cfg.ReinforcementPrompt,
		})
		logx.InfoCtxf(ctx, "conversation", "tool async registered convo=%q turn=%q op_id=%q tool=%q async_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(opID), strings.TrimSpace(extracted.Status))
		publishAsyncLifecycleEvent(ctx, step.Name, opID, streaming.EventTypeToolCallStarted, extracted)
		if rec != nil && rec.Terminal() {
			publishAsyncUpdateEvent(ctx, step.Name, opID, extracted, rec)
			return nil
		}
		if state := asynccfg.DeriveState(extracted.Status, extracted.Error, ""); state == asynccfg.StateWaiting || state == asynccfg.StateRunning || state == asynccfg.StateStarted {
			publishAsyncLifecycleEvent(ctx, step.Name, opID, streaming.EventTypeToolCallWaiting, extracted)
		}
		return rec
	case sameToolName(step.Name, cfg.Status.Tool):
		opID := strings.TrimSpace(stringArg(step.Args, cfg.Status.OperationIDArg))
		if opID == "" && cfg.Status.ReuseRunArgs {
			if rec, ok := manager.FindActiveByRequest(ctx, turn.ConversationID, turn.TurnID, step.Name, requestDigest); ok && rec != nil {
				opID = rec.ID
			}
		}
		if opID == "" {
			return nil
		}
		payload, err := asynccfg.ExtractPayload(toolResult, cfg.Status.Selector)
		if err != nil || payload == nil {
			return nil
		}
		normalizeAsyncExtracted(toolResult, payload)
		rec, changed := manager.Update(ctx, asynccfg.UpdateInput{
			ID:      opID,
			Status:  payload.Status,
			Message: payload.Message,
			Percent: payload.Percent,
			KeyData: cloneRaw(payload.KeyData),
			Error:   payload.Error,
		})
		if rec != nil {
			patchAsyncToolPersistence(context.Background(), convFromContext(ctx), rec, "", payload)
		}
		if rec != nil {
			asyncwait.MarkAfterStatus(ctx, opID)
		}
		if changed {
			publishAsyncUpdateEvent(ctx, step.Name, opID, payload, rec)
		}
	}
	return nil
}

func normalizeAsyncExtracted(raw string, extracted *asynccfg.Extracted) {
	if extracted == nil {
		return
	}
	if strings.TrimSpace(extracted.Status) != "" || strings.TrimSpace(extracted.Error) != "" {
		return
	}
	if len(extracted.KeyData) > 0 {
		extracted.Status = "completed"
		return
	}
	var root interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &root); err != nil || root == nil {
		return
	}
	switch root.(type) {
	case []interface{}:
		extracted.Status = "completed"
		extracted.KeyData = json.RawMessage(strings.TrimSpace(raw))
	}
}

func waitForAsyncRecallPollWindow(ctx context.Context, reg tool.Registry, step StepInfo, turn runtimerequestctx.TurnMeta) error {
	manager, ok := AsyncManagerFromContext(ctx)
	if !ok {
		return nil
	}
	cfg, ok := asyncConfigForStep(ctx, reg, step.Name)
	if !ok || cfg == nil {
		return nil
	}
	if !cfg.Status.ReuseRunArgs || !sameToolName(step.Name, cfg.Run.Tool) || !sameToolName(step.Name, cfg.Status.Tool) {
		return nil
	}
	requestDigest := requestArgsDigest(cfg, step.Args)
	if requestDigest == "" {
		return nil
	}
	rec, ok := manager.FindActiveByRequest(ctx, turn.ConversationID, turn.TurnID, step.Name, requestDigest)
	if !ok || rec == nil || rec.Terminal() {
		return nil
	}
	delay := nextAsyncPollDelay(rec)
	if delay <= 0 {
		return nil
	}
	logx.InfoCtxf(ctx, "conversation", "tool async recall wait convo=%q turn=%q op_id=%q tool=%q async_id=%q delay=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(rec.ID), delay)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func extractAsyncOperationID(toolResult string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	opID, err := asynccfg.ExtractOperationID(toolResult, path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(opID)
}

func requestArgsDigest(cfg *asynccfg.Config, args map[string]interface{}) string {
	normalized := normalizedAsyncArgs(cfg, args)
	if len(normalized) == 0 {
		return ""
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return ""
	}
	return string(data)
}

func nextAsyncPollDelay(rec *asynccfg.OperationRecord) time.Duration {
	if rec == nil {
		return 0
	}
	intervalMs := rec.PollIntervalMs
	if intervalMs <= 0 {
		return 0
	}
	nextAt := rec.UpdatedAt.Add(time.Duration(intervalMs) * time.Millisecond)
	if !nextAt.After(time.Now()) {
		return 0
	}
	return time.Until(nextAt)
}

func normalizedAsyncArgs(cfg *asynccfg.Config, args map[string]interface{}) map[string]interface{} {
	if len(args) == 0 {
		return nil
	}
	cloned := make(map[string]interface{}, len(args))
	for key, value := range args {
		if key == "timeoutMs" {
			continue
		}
		cloned[key] = value
	}
	if cfg != nil {
		for key := range cfg.Run.ExtraArgs {
			delete(cloned, key)
		}
		for key := range cfg.Status.ExtraArgs {
			delete(cloned, key)
		}
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func synthesizeAsyncOperationID(step StepInfo, requestDigest string) string {
	if id := strings.TrimSpace(step.ID); id != "" {
		return id
	}
	name := strings.TrimSpace(step.Name)
	if requestDigest == "" {
		if name == "" {
			return "async"
		}
		return "async:" + name
	}
	if name == "" {
		return fmt.Sprintf("async:%x", []byte(requestDigest))
	}
	return name + ":" + requestDigest
}

func sameToolName(actual, expected string) bool {
	return strings.EqualFold(strings.TrimSpace(mcpname.Canonical(actual)), strings.TrimSpace(mcpname.Canonical(expected)))
}

func PollAsyncOperation(ctx context.Context, manager AsyncManager, reg tool.Registry, cfg *asynccfg.Config, turn runtimerequestctx.TurnMeta, opID string, conv apiconv.Client) {
	if cfg == nil || manager == nil || reg == nil || strings.TrimSpace(opID) == "" {
		return
	}
	interval := time.Duration(cfg.PollIntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	statusArgs := map[string]interface{}{cfg.Status.OperationIDArg: opID}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if rec, ok := manager.Get(context.Background(), opID); ok && rec != nil && rec.TimeoutAt != nil && time.Now().After(*rec.TimeoutAt) {
				if cfg.Cancel != nil && strings.TrimSpace(cfg.Cancel.Tool) != "" {
					cancelArgs := map[string]interface{}{cfg.Cancel.OperationIDArg: opID}
					_, _ = reg.Execute(runtimerequestctx.WithConversationID(context.Background(), turn.ConversationID), cfg.Cancel.Tool, cancelArgs)
				}
				payload := &asynccfg.Extracted{
					Status: "failed",
					Error:  "operation timed out",
				}
				rec, _ = manager.Update(context.Background(), asynccfg.UpdateInput{
					ID:     opID,
					Status: "failed",
					Error:  "operation timed out",
					State:  asynccfg.StateFailed,
				})
				patchAsyncToolPersistence(context.Background(), conv, rec, "operation timed out", nil)
				publishAsyncUpdateEvent(ctx, cfg.Run.Tool, opID, payload, rec)
				return
			}
			result, err := reg.Execute(runtimerequestctx.WithConversationID(context.Background(), turn.ConversationID), cfg.Status.Tool, statusArgs)
			if err != nil {
				rec, terminal := manager.RecordPollFailure(context.Background(), opID, err.Error(), isTransientPollError(err))
				if terminal {
					patchAsyncToolPersistence(context.Background(), conv, rec, err.Error(), nil)
					publishAsyncUpdateEvent(ctx, cfg.Status.Tool, opID, &asynccfg.Extracted{Status: "failed", Error: err.Error()}, rec)
					return
				}
				if !waitPollBackoff(ctx, pollBackoff(interval, rec)) {
					return
				}
				continue
			}
			_, _ = manager.ResetPollFailures(context.Background(), opID)
			payload, err := asynccfg.ExtractPayload(result, cfg.Status.Selector)
			if err != nil || payload == nil {
				continue
			}
			rec, _ := manager.Update(context.Background(), asynccfg.UpdateInput{
				ID:      opID,
				Status:  payload.Status,
				Message: payload.Message,
				Percent: payload.Percent,
				KeyData: cloneRaw(payload.KeyData),
				Error:   payload.Error,
			})
			patchAsyncToolPersistence(context.Background(), conv, rec, "", payload)
			publishAsyncUpdateEvent(ctx, cfg.Status.Tool, opID, payload, rec)
			if rec != nil && rec.Terminal() {
				return
			}
		}
	}
}

func patchAsyncToolPersistence(ctx context.Context, conv apiconv.Client, rec *asynccfg.OperationRecord, fallbackErr string, payload *asynccfg.Extracted) {
	if conv == nil || rec == nil || strings.TrimSpace(rec.ToolMessageID) == "" {
		return
	}
	content := ""
	respPayloadID := ""
	if payload != nil {
		switch {
		case rec.Terminal() && len(payload.KeyData) > 0:
			content = strings.TrimSpace(string(payload.KeyData))
		case strings.TrimSpace(payload.Message) != "":
			content = strings.TrimSpace(payload.Message)
		case strings.TrimSpace(payload.Status) != "":
			content = strings.TrimSpace(payload.Status)
		}
		switch {
		case len(payload.KeyData) > 0:
			if id, err := persistResponsePayload(ctx, conv, string(payload.KeyData)); err == nil {
				respPayloadID = id
			}
		case strings.TrimSpace(payload.Message) != "":
			if id, err := persistResponsePayload(ctx, conv, payload.Message); err == nil {
				respPayloadID = id
			}
		case strings.TrimSpace(payload.Error) != "":
			if id, err := persistResponsePayload(ctx, conv, payload.Error); err == nil {
				respPayloadID = id
			}
		}
	}
	if content != "" {
		_ = updateToolMessageContent(ctx, conv, rec.ToolMessageID, content)
	}
	status := "running"
	if rec.Terminal() {
		status = strings.TrimSpace(string(rec.State))
	}
	errMsg := fallbackErr
	if payload != nil && strings.TrimSpace(payload.Error) != "" {
		errMsg = strings.TrimSpace(payload.Error)
	}
	if rec.Terminal() {
		_ = completeToolCall(ctx, conv, rec.ToolMessageID, rec.ToolCallID, rec.ToolName, status, time.Now(), respPayloadID, errMsg)
		return
	}
	_ = updateAsyncToolCallState(ctx, conv, rec.ToolMessageID, rec.ToolCallID, rec.ToolName, status, respPayloadID, errMsg)
}

type asyncConvKey struct{}

func WithAsyncConversation(ctx context.Context, conv apiconv.Client) context.Context {
	if conv == nil {
		return ctx
	}
	return context.WithValue(ctx, asyncConvKey{}, conv)
}

func convFromContext(ctx context.Context) apiconv.Client {
	if ctx == nil {
		return nil
	}
	conv, _ := ctx.Value(asyncConvKey{}).(apiconv.Client)
	return conv
}

func stringArg(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(toString(value))
}

func toString(value interface{}) string {
	switch actual := value.(type) {
	case string:
		return actual
	default:
		return strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(string(mustJSON(actual))), "\"", ""))
	}
}

func mustJSON(value interface{}) []byte {
	data, _ := json.Marshal(value)
	return data
}

func cloneRaw(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return nil
	}
	copyBuf := make([]byte, len(v))
	copy(copyBuf, v)
	return copyBuf
}

func asyncStatusArgs(cfg *asynccfg.Config, opID string, stepArgs map[string]interface{}) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	args := map[string]interface{}{}
	if cfg.Status.ReuseRunArgs {
		for key, value := range normalizedAsyncArgs(cfg, stepArgs) {
			args[key] = value
		}
	}
	if arg := strings.TrimSpace(cfg.Status.OperationIDArg); arg != "" && strings.TrimSpace(opID) != "" {
		args[arg] = strings.TrimSpace(opID)
	}
	for key, value := range cfg.Status.ExtraArgs {
		args[key] = value
	}
	return args
}

func asyncCancelToolName(cfg *asynccfg.Config) string {
	if cfg == nil || cfg.Cancel == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Cancel.Tool)
}

func publishAsyncUpdateEvent(ctx context.Context, toolName, opID string, payload *asynccfg.Extracted, rec *asynccfg.OperationRecord) {
	if payload == nil {
		return
	}
	eventType := streaming.EventTypeToolCallDelta
	if rec != nil {
		switch rec.State {
		case asynccfg.StateCompleted:
			eventType = streaming.EventTypeToolCallCompleted
		case asynccfg.StateFailed:
			eventType = streaming.EventTypeToolCallFailed
		case asynccfg.StateCanceled:
			eventType = streaming.EventTypeToolCallCanceled
		case asynccfg.StateWaiting, asynccfg.StateRunning, asynccfg.StateStarted:
			eventType = streaming.EventTypeToolCallWaiting
		}
	}
	publishAsyncLifecycleEvent(ctx, toolName, opID, eventType, payload)
}

func publishAsyncLifecycleEvent(ctx context.Context, toolName, opID string, eventType streaming.EventType, payload *asynccfg.Extracted) {
	pub, ok := modelcallctx.StreamPublisherFromContext(ctx)
	if !ok {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	event := &streaming.Event{
		Type:           eventType,
		ConversationID: strings.TrimSpace(turn.ConversationID),
		StreamID:       strings.TrimSpace(turn.ConversationID),
		TurnID:         strings.TrimSpace(turn.TurnID),
		MessageID:      strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)),
		ToolMessageID:  strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)),
		ToolCallID:     strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)),
		OperationID:    strings.TrimSpace(opID),
		ToolName:       strings.TrimSpace(toolName),
		CreatedAt:      time.Now(),
	}
	if payload != nil {
		event.Status = strings.TrimSpace(payload.Status)
		event.Content = strings.TrimSpace(payload.Message)
		if strings.TrimSpace(payload.Error) != "" {
			event.Error = strings.TrimSpace(payload.Error)
		}
		if len(payload.KeyData) > 0 {
			var response map[string]interface{}
			if err := json.Unmarshal(payload.KeyData, &response); err == nil {
				event.ResponsePayload = response
			}
		}
	}
	event.NormalizeIdentity(strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	_ = pub.Publish(ctx, &modelcallctx.StreamEvent{
		ConversationID: strings.TrimSpace(turn.ConversationID),
		Event:          event,
	})
}

func isTransientPollError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	for _, marker := range []string{"bad request", "unauthorized", "forbidden", "not found", "invalid", " 400", " 401", " 403", " 404"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

func pollBackoff(base time.Duration, rec *asynccfg.OperationRecord) time.Duration {
	failures := 1
	if rec != nil && rec.PollFailures > 0 {
		failures = rec.PollFailures
	}
	if base <= 0 {
		base = 2 * time.Second
	}
	backoff := time.Duration(failures) * base
	max := 5 * base
	if backoff > max {
		return max
	}
	return backoff
}

func waitPollBackoff(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
