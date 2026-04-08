package executil

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/runtime/streaming"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
)

func asyncConfigForStep(ctx context.Context, reg tool.Registry, name string) (*asynccfg.Config, bool) {
	if cfg, ok := tool.AsyncConfigFor(ctx, name); ok {
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
	cfg, ok := asyncConfigForStep(ctx, reg, step.Name)
	if !ok || cfg == nil {
		return nil
	}
	turn, ok := memory.TurnMetaFromContext(ctx)
	if !ok {
		return nil
	}
	switch {
	case sameToolName(step.Name, cfg.Run.Tool):
		opID, err := asynccfg.ExtractOperationID(toolResult, cfg.Run.OperationIDPath)
		if err != nil || strings.TrimSpace(opID) == "" {
			return nil
		}
		extracted := &asynccfg.Extracted{}
		if cfg.Run.Selector != nil {
			if payload, err := asynccfg.ExtractPayload(toolResult, *cfg.Run.Selector); err == nil && payload != nil {
				extracted = payload
			}
		}
		rec := manager.Register(ctx, asynccfg.RegisterInput{
			ID:                            opID,
			ParentConvID:                  turn.ConversationID,
			ParentTurnID:                  turn.TurnID,
			ToolCallID:                    step.ID,
			ToolMessageID:                 strings.TrimSpace(memory.ToolMessageIDFromContext(ctx)),
			ToolName:                      step.Name,
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
		publishAsyncLifecycleEvent(ctx, step.Name, opID, streaming.EventTypeToolCallStarted, extracted)
		if state := asynccfg.DeriveState(extracted.Status, extracted.Error, ""); state == asynccfg.StateWaiting || state == asynccfg.StateRunning || state == asynccfg.StateStarted {
			publishAsyncLifecycleEvent(ctx, step.Name, opID, streaming.EventTypeToolCallWaiting, extracted)
		}
		go PollAsyncOperation(context.WithoutCancel(ctx), manager, reg, cfg, turn, opID, convFromContext(ctx))
		return rec
	case sameToolName(step.Name, cfg.Status.Tool):
		opID := strings.TrimSpace(stringArg(step.Args, cfg.Status.OperationIDArg))
		if opID == "" {
			return nil
		}
		payload, err := asynccfg.ExtractPayload(toolResult, cfg.Status.Selector)
		if err != nil || payload == nil {
			return nil
		}
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
		if rec != nil && !rec.Terminal() {
			MarkAsyncWaitAfterStatus(ctx, opID)
		}
		if changed {
			publishAsyncUpdateEvent(ctx, step.Name, opID, payload, rec)
		}
	}
	return nil
}

func sameToolName(actual, expected string) bool {
	return strings.EqualFold(strings.TrimSpace(mcpname.Canonical(actual)), strings.TrimSpace(mcpname.Canonical(expected)))
}

func PollAsyncOperation(ctx context.Context, manager AsyncManager, reg tool.Registry, cfg *asynccfg.Config, turn memory.TurnMeta, opID string, conv apiconv.Client) {
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
					_, _ = reg.Execute(memory.WithConversationID(context.Background(), turn.ConversationID), cfg.Cancel.Tool, cancelArgs)
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
			result, err := reg.Execute(memory.WithConversationID(context.Background(), turn.ConversationID), cfg.Status.Tool, statusArgs)
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
	turn, _ := memory.TurnMetaFromContext(ctx)
	event := &streaming.Event{
		Type:           eventType,
		ConversationID: strings.TrimSpace(turn.ConversationID),
		StreamID:       strings.TrimSpace(turn.ConversationID),
		TurnID:         strings.TrimSpace(turn.TurnID),
		MessageID:      strings.TrimSpace(memory.ToolMessageIDFromContext(ctx)),
		ToolMessageID:  strings.TrimSpace(memory.ToolMessageIDFromContext(ctx)),
		ToolCallID:     strings.TrimSpace(memory.ToolMessageIDFromContext(ctx)),
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
