package toolexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/textutil"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	asynccfg "github.com/viant/agently-core/protocol/async"
	asyncnarrator "github.com/viant/agently-core/protocol/async/narrator"
	"github.com/viant/agently-core/protocol/tool"
	toolasyncconfig "github.com/viant/agently-core/protocol/tool/asyncconfig"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
	"github.com/viant/agently-core/service/shared/asyncwait"
	toolstatus "github.com/viant/agently-core/service/tool/status"
)

const agentlyControlArgKey = "_agently"

var asyncNarrationDebounceWindow = 3 * time.Second

type asyncNarrationHandle struct {
	stepID   string
	stepName string
	cfg      *asynccfg.Config
	pairing  *toolstatus.NarrationPairing
	session  *asyncnarrator.Session
}

func WithAsyncWaitState(ctx context.Context) context.Context {
	return asyncwait.WithState(ctx)
}

func ConsumeAsyncWaitAfterStatus(ctx context.Context) []string {
	return asyncwait.ConsumeAfterStatus(ctx)
}

func withAsyncNarratorRunnerIfPresent(ctx context.Context) context.Context {
	runner, ok := AsyncNarratorRunnerFromContext(ctx)
	if !ok || runner == nil {
		return ctx
	}
	return asyncnarrator.WithLLMRunner(ctx, runner)
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

func stripAgentlyControlArgs(args map[string]interface{}) map[string]interface{} {
	if len(args) == 0 {
		return args
	}
	cloned := map[string]interface{}{}
	for key, value := range args {
		if key == agentlyControlArgKey {
			continue
		}
		cloned[key] = value
	}
	return cloned
}

func asyncExecutionModeOverride(args map[string]interface{}) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	raw, ok := args[agentlyControlArgKey]
	if !ok || raw == nil {
		return "", false
	}
	root, ok := raw.(map[string]interface{})
	if !ok {
		return "", false
	}
	asyncRaw, ok := root["async"]
	if !ok || asyncRaw == nil {
		return "", false
	}
	asyncMap, ok := asyncRaw.(map[string]interface{})
	if !ok {
		return "", false
	}
	value, ok := asyncMap["executionMode"]
	if !ok {
		return "", false
	}
	switch actual := value.(type) {
	case string:
		normalized := asynccfg.NormalizeExecutionMode(actual, "")
		if normalized == "" {
			return "", false
		}
		return normalized, true
	default:
		return "", false
	}
}

func effectiveAsyncConfig(cfg *asynccfg.Config, args map[string]interface{}) *asynccfg.Config {
	if cfg == nil {
		return nil
	}
	override, ok := asyncExecutionModeOverride(args)
	if !ok {
		return cfg
	}
	cloned := *cfg
	cloned.DefaultExecutionMode = override
	return &cloned
}

func effectiveExecutionMode(cfg *asynccfg.Config, args map[string]interface{}) string {
	if cfg == nil {
		return asynccfg.NormalizeExecutionMode("", string(asynccfg.ExecutionModeWait))
	}
	if override, ok := asyncExecutionModeOverride(args); ok {
		return override
	}
	if path := strings.TrimSpace(cfg.Run.ExecutionModePath); path != "" {
		if value, ok := args[path]; ok && value != nil {
			if mode := asynccfg.NormalizeExecutionMode(fmt.Sprint(value), cfg.DefaultExecutionMode); mode != "" {
				return mode
			}
		}
		if raw, ok := asynccfgLookup(args, path); ok && raw != nil {
			if mode := asynccfg.NormalizeExecutionMode(fmt.Sprint(raw), cfg.DefaultExecutionMode); mode != "" {
				return mode
			}
		}
	}
	return asynccfg.NormalizeExecutionMode(cfg.DefaultExecutionMode, string(asynccfg.ExecutionModeWait))
}

func asynccfgLookup(root map[string]interface{}, path string) (interface{}, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return root, true
	}
	current := any(root)
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		actual, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		next, ok := actual[part]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func prepareAsyncStartArgs(cfg *asynccfg.Config, args map[string]interface{}) map[string]interface{} {
	cloned := stripAgentlyControlArgs(args)
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
	cfg = effectiveAsyncConfig(cfg, step.Args)
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
		if rec != nil && (asynccfg.ExecutionModeWaits(rec.ExecutionMode) || !changed) {
			asyncwait.MarkAfterStatus(ctx, matched.ID)
		}
		if changed {
			publishAsyncUpdateEvent(ctx, step.Name, matched.ToolCallID, matched.ID, payload, rec)
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
		sameToolRecall := sameToolName(cfg.Run.Tool, cfg.Status.Tool) && cfg.Status.ReuseRunArgs
		rec, _ := manager.Register(ctx, asynccfg.RegisterInput{
			ID:                   opID,
			ParentConvID:         turn.ConversationID,
			ParentTurnID:         turn.TurnID,
			ToolCallID:           step.ID,
			ToolMessageID:        strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)),
			ToolName:             step.Name,
			StatusToolName:       strings.TrimSpace(cfg.Status.Tool),
			StatusOperationIDArg: strings.TrimSpace(cfg.Status.OperationIDArg),
			SameToolRecall:       sameToolRecall,
			StatusArgs:           asyncStatusArgs(cfg, opID, step.Args),
			CancelToolName:       asyncCancelToolName(cfg),
			RequestArgsDigest:    requestDigest,
			RequestArgs:          normalizedAsyncArgs(cfg, step.Args),
			OperationIntent:      asynccfg.ExtractIntent(normalizedAsyncArgs(cfg, step.Args), cfg.Run.IntentPath, step.Name),
			OperationSummary:     asynccfg.ExtractSummary(normalizedAsyncArgs(cfg, step.Args), cfg.Run.SummaryPaths),
			ExecutionMode:        effectiveExecutionMode(cfg, step.Args),
			Status:               extracted.Status,
			Message:              extracted.Message,
			Percent:              extracted.Percent,
			KeyData:              cloneRaw(extracted.KeyData),
			Error:                extracted.Error,
			TimeoutMs:            cfg.TimeoutMs,
			IdleTimeoutMs:        cfg.IdleTimeoutMs,
			PollIntervalMs:       cfg.PollIntervalMs,
		})
		logx.InfoCtxf(ctx, "conversation", "tool async registered convo=%q turn=%q op_id=%q tool=%q async_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(opID), strings.TrimSpace(extracted.Status))
		if rec != nil {
			patchAsyncToolPersistence(context.Background(), convFromContext(ctx), rec, "", extracted)
			if asynccfg.ExecutionModeWaits(rec.ExecutionMode) && !sameToolRecall {
				maybeStartAsyncPoller(ctx, manager, reg, cfg, turn, opID, convFromContext(ctx))
			}
		}
		publishAsyncLifecycleEvent(ctx, step.Name, step.ID, opID, streaming.EventTypeToolCallStarted, extracted)
		if rec != nil && rec.Terminal() {
			publishAsyncUpdateEvent(ctx, step.Name, step.ID, opID, extracted, rec)
			return nil
		}
		if state := asynccfg.DeriveState(extracted.Status, extracted.Error, ""); state == asynccfg.StateWaiting || state == asynccfg.StateRunning || state == asynccfg.StateStarted {
			publishAsyncLifecycleEvent(ctx, step.Name, step.ID, opID, streaming.EventTypeToolCallWaiting, extracted)
		}
		return rec
	case sameToolName(step.Name, cfg.Status.Tool):
		opID := resolveAsyncStatusOperationID(ctx, manager, cfg, step)
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
		if rec != nil && asynccfg.ExecutionModeWaits(rec.ExecutionMode) {
			if rebound, _ := manager.BindToolCarrier(ctx, opID, step.ID, strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)), step.Name); rebound != nil {
				rec = rebound
			}
		}
		if rec != nil {
			patchAsyncToolPersistence(context.Background(), convFromContext(ctx), rec, "", payload)
		}
		if rec != nil && (asynccfg.ExecutionModeWaits(rec.ExecutionMode) || !changed) {
			asyncwait.MarkAfterStatus(ctx, opID)
		}
		if changed {
			publishAsyncUpdateEvent(ctx, step.Name, step.ID, opID, payload, rec)
		}
	}
	return nil
}

func resolveAsyncStatusOperationID(ctx context.Context, manager *asynccfg.Manager, cfg *asynccfg.Config, step StepInfo) string {
	if cfg == nil {
		return ""
	}
	opID := strings.TrimSpace(stringArg(step.Args, cfg.Status.OperationIDArg))
	if opID == "" && cfg.Status.ReuseRunArgs {
		if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
			requestDigest := requestArgsDigest(cfg, step.Args)
			if rec, found := manager.FindActiveByRequest(ctx, turn.ConversationID, turn.TurnID, step.Name, requestDigest); found && rec != nil {
				opID = strings.TrimSpace(rec.ID)
			}
		}
	}
	return opID
}

func maybeAwaitAsyncStatusResult(ctx context.Context, reg tool.Registry, step StepInfo) (string, bool, error) {
	manager, ok := AsyncManagerFromContext(ctx)
	if !ok || manager == nil {
		return "", false, nil
	}
	cfg, ok := asyncConfigForStep(ctx, reg, step.Name)
	if !ok || cfg == nil {
		return "", false, nil
	}
	cfg = effectiveAsyncConfig(cfg, step.Args)
	if strings.TrimSpace(cfg.Status.Tool) == "" || !sameToolName(step.Name, cfg.Status.Tool) {
		return "", false, nil
	}
	if sameToolName(cfg.Run.Tool, cfg.Status.Tool) && cfg.Status.ReuseRunArgs {
		return "", false, nil
	}
	opID := resolveAsyncStatusOperationID(ctx, manager, cfg, step)
	if opID == "" {
		logx.DebugCtxf(ctx, "conversation", "async wait skip tool=%q op_id=empty", strings.TrimSpace(step.Name))
		return "", false, nil
	}
	rec, ok := manager.Get(ctx, opID)
	if !ok || rec == nil || !asynccfg.ExecutionModeWaits(rec.ExecutionMode) {
		logx.DebugCtxf(ctx, "conversation", "async wait skip tool=%q op_id=%q found=%t rec_nil=%t execution_mode=%q", strings.TrimSpace(step.Name), strings.TrimSpace(opID), ok, rec == nil, func() string {
			if rec == nil {
				return ""
			}
			return strings.TrimSpace(rec.ExecutionMode)
		}())
		return "", false, nil
	}
	logx.InfoCtxf(ctx, "conversation", "async wait start tool=%q op_id=%q execution_mode=%q status=%q message=%q", strings.TrimSpace(step.Name), strings.TrimSpace(opID), strings.TrimSpace(rec.ExecutionMode), strings.TrimSpace(rec.Status), strings.TrimSpace(rec.Message))
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		maybeStartAsyncPoller(ctx, manager, reg, cfg, turn, opID, convFromContext(ctx))
	}
	narration := startAsyncNarration(ctx, cfg, step, rec)
	var (
		sub   <-chan asynccfg.ChangeEvent
		subID uint64
	)
	if narration != nil {
		sub, subID = manager.Subscribe([]string{opID})
		defer manager.Unsubscribe(subID)
	}
	ch := manager.AwaitTerminal(ctx, []string{opID})
	select {
	case <-ctx.Done():
		return "", false, ctx.Err()
	case result, ok := <-ch:
		if !ok {
			return "", false, nil
		}
		finishAsyncNarration(ctx, narration, opID, step.Name)
		data, err := json.Marshal(result)
		if err != nil {
			return "", false, err
		}
		return string(data), true, nil
	case ev, ok := <-sub:
		if !ok {
			sub = nil
		} else {
			observeAsyncNarration(ctx, narration, ev)
		}
		for {
			select {
			case <-ctx.Done():
				return "", false, ctx.Err()
			case result, ok := <-ch:
				if !ok {
					return "", false, nil
				}
				finishAsyncNarration(ctx, narration, opID, step.Name)
				data, err := json.Marshal(result)
				if err != nil {
					return "", false, err
				}
				return string(data), true, nil
			case ev, ok := <-sub:
				if !ok {
					sub = nil
					continue
				}
				observeAsyncNarration(ctx, narration, ev)
			case <-asyncNarrationChannel(narration):
				flushAsyncNarration(ctx, narration, opID, step.Name, "debounced update")
			}
		}
	}
}

func startAsyncNarration(ctx context.Context, cfg *asynccfg.Config, step StepInfo, rec *asynccfg.OperationRecord) *asyncNarrationHandle {
	conv := convFromContext(ctx)
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if conv == nil || !ok || rec == nil {
		logx.WarnCtxf(ctx, "conversation", "async narrator skipped tool=%q op_id=%q conv_nil=%t turn_ok=%t rec_nil=%t", strings.TrimSpace(step.Name), func() string {
			if rec == nil {
				return ""
			}
			return strings.TrimSpace(rec.ID)
		}(), conv == nil, ok, rec == nil)
		return nil
	}
	statusSvc := toolstatus.New(conv)
	pairing := toolstatus.NewNarrationPairing(statusSvc)
	handle := &asyncNarrationHandle{
		stepID:   step.ID,
		stepName: step.Name,
		cfg:      cfg,
		pairing:  pairing,
		session: asyncnarrator.NewSession(asyncNarrationDebounceWindow, func(text string) error {
			_, err := pairing.Upsert(ctx, step.ID, turn, step.Name, "assistant", "tool", "narrator", text)
			return err
		}),
	}
	narratorCtx := withAsyncNarratorRunnerIfPresent(ctx)
	if text, err := asyncnarrator.StartNarration(narratorCtx, cfg, rec); err == nil && text != "" {
		logx.InfoCtxf(ctx, "conversation", "async narrator preamble tool=%q op_id=%q text=%q", strings.TrimSpace(step.Name), strings.TrimSpace(rec.ID), textutil.Head(text, 256))
		if err := handle.session.Start(text); err != nil {
			logx.WarnCtxf(ctx, "conversation", "async preamble start failed op_id=%q tool=%q err=%v", strings.TrimSpace(rec.ID), strings.TrimSpace(step.Name), err)
		}
	} else if err == nil {
		logx.WarnCtxf(ctx, "conversation", "async narrator produced empty preamble op_id=%q tool=%q", strings.TrimSpace(rec.ID), strings.TrimSpace(step.Name))
	} else if err != nil {
		logx.WarnCtxf(ctx, "conversation", "async narrator start failed op_id=%q tool=%q err=%v", strings.TrimSpace(rec.ID), strings.TrimSpace(step.Name), err)
	}
	return handle
}

func asyncNarrationChannel(handle *asyncNarrationHandle) <-chan time.Time {
	if handle == nil || handle.session == nil {
		return nil
	}
	return handle.session.Channel()
}

func observeAsyncNarration(ctx context.Context, handle *asyncNarrationHandle, ev asynccfg.ChangeEvent) {
	if handle == nil || handle.session == nil || handle.pairing == nil {
		return
	}
	if strings.TrimSpace(handle.pairing.MessageID(handle.stepID)) == "" {
		return
	}
	narratorCtx := withAsyncNarratorRunnerIfPresent(ctx)
	preamble, err := asyncnarrator.UpdateNarration(narratorCtx, handle.cfg, ev)
	if err != nil {
		logx.WarnCtxf(ctx, "conversation", "async narrator update failed op_id=%q tool=%q err=%v", strings.TrimSpace(ev.OperationID), strings.TrimSpace(handle.stepName), err)
		return
	}
	if err := handle.session.Push(preamble); err != nil {
		logx.WarnCtxf(ctx, "conversation", "async preamble update failed op_id=%q tool=%q err=%v", strings.TrimSpace(ev.OperationID), strings.TrimSpace(handle.stepName), err)
	}
}

func flushAsyncNarration(ctx context.Context, handle *asyncNarrationHandle, opID, toolName, phase string) {
	if handle == nil || handle.session == nil || handle.pairing == nil {
		return
	}
	if strings.TrimSpace(handle.pairing.MessageID(handle.stepID)) == "" {
		return
	}
	if err := handle.session.Flush(); err != nil {
		logx.WarnCtxf(ctx, "conversation", "async preamble %s failed op_id=%q tool=%q err=%v", strings.TrimSpace(phase), strings.TrimSpace(opID), strings.TrimSpace(toolName), err)
	}
}

func finishAsyncNarration(ctx context.Context, handle *asyncNarrationHandle, opID, toolName string) {
	if handle == nil {
		return
	}
	flushAsyncNarration(ctx, handle, opID, toolName, "flush")
	if handle.pairing != nil {
		handle.pairing.Release(handle.stepID)
	}
}

func changeEventFromRecord(rec *asynccfg.OperationRecord) asynccfg.ChangeEvent {
	if rec == nil {
		return asynccfg.ChangeEvent{}
	}
	var percent *int
	if rec.Percent != nil {
		value := *rec.Percent
		percent = &value
	}
	return asynccfg.ChangeEvent{
		OperationID:  strings.TrimSpace(rec.ID),
		Status:       strings.TrimSpace(rec.Status),
		Message:      strings.TrimSpace(rec.Message),
		Percent:      percent,
		KeyData:      cloneRaw(rec.KeyData),
		Error:        strings.TrimSpace(rec.Error),
		State:        rec.State,
		ChangedAt:    rec.UpdatedAt,
		ToolName:     strings.TrimSpace(rec.ToolName),
		Intent:       strings.TrimSpace(rec.OperationIntent),
		Summary:      strings.TrimSpace(rec.OperationSummary),
		Conversation: strings.TrimSpace(rec.ParentConvID),
		TurnID:       strings.TrimSpace(rec.ParentTurnID),
	}
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
	cfg = effectiveAsyncConfig(cfg, step.Args)
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

func maybeStartAsyncPoller(ctx context.Context, manager *asynccfg.Manager, reg tool.Registry, cfg *asynccfg.Config, turn runtimerequestctx.TurnMeta, opID string, conv apiconv.Client) {
	if cfg == nil || manager == nil || reg == nil || strings.TrimSpace(opID) == "" {
		return
	}
	if strings.TrimSpace(cfg.Status.Tool) == "" {
		return
	}
	// Create a cancelable context rooted at Background so the poller outlives
	// the parent HTTP request, while still being stoppable via CancelTurnPollers
	// or Manager.Close.
	pollCtx, cancel := context.WithCancel(context.Background())
	pollCtx = rehydrateAsyncPollContext(ctx, pollCtx, turn)
	// AdmitPoller atomically checks the Manager isn't closed, gates
	// double-start for the same op id, registers the cancel, and bumps
	// pollerWG — Close() waits on that wg to join every admitted
	// goroutine before returning.
	if !manager.AdmitPoller(ctx, opID, cancel) {
		cancel()
		return
	}
	maybeCreateAsyncStatusCarrier(pollCtx, manager, cfg, turn, opID, conv)
	go func() {
		defer cancel() // idempotent — ensures cleanup if PollAsyncOperation returns early
		PollAsyncOperation(pollCtx, manager, reg, cfg, turn, opID, conv)
	}()
}

func maybeCreateAsyncStatusCarrier(ctx context.Context, manager *asynccfg.Manager, cfg *asynccfg.Config, turn runtimerequestctx.TurnMeta, opID string, conv apiconv.Client) {
	if conv == nil || manager == nil || cfg == nil {
		return
	}
	statusTool := strings.TrimSpace(cfg.Status.Tool)
	if statusTool == "" || sameToolName(statusTool, cfg.Run.Tool) {
		return
	}
	rec, ok := manager.Get(ctx, opID)
	if !ok || rec == nil || !asynccfg.ExecutionModeWaits(rec.ExecutionMode) {
		return
	}
	if sameToolName(rec.ToolName, statusTool) && strings.TrimSpace(rec.ToolMessageID) != "" && strings.TrimSpace(rec.ToolCallID) != "" {
		return
	}
	startedAt := time.Now()
	toolMsgID, err := createToolMessage(ctx, conv, turn, startedAt, statusTool)
	if err != nil {
		logx.WarnCtxf(ctx, "conversation", "async status carrier message create failed op_id=%q tool=%q err=%v", strings.TrimSpace(opID), statusTool, err)
		return
	}
	toolCallID := "async-status:" + strings.TrimSpace(opID)
	if err := initToolCall(ctx, conv, toolMsgID, toolCallID, turn, statusTool, startedAt, ""); err != nil {
		logx.WarnCtxf(ctx, "conversation", "async status carrier tool call init failed op_id=%q tool=%q err=%v", strings.TrimSpace(opID), statusTool, err)
		return
	}
	rebound, _ := manager.BindToolCarrier(ctx, opID, toolCallID, toolMsgID, statusTool)
	if rebound == nil {
		return
	}
	payload := &asynccfg.Extracted{
		Status:  strings.TrimSpace(rebound.Status),
		Message: strings.TrimSpace(rebound.Message),
		Percent: rebound.Percent,
		KeyData: cloneRaw(rebound.KeyData),
		Error:   strings.TrimSpace(rebound.Error),
	}
	patchAsyncToolPersistence(context.Background(), conv, rebound, "", payload)
}

// rehydrateAsyncPollContext copies the request-scoped values that the autonomous
// poller needs from src into dst. dst must already be a cancelable background
// context created by the caller.
//
// Values carried over:
//   - conversation/turn/message identity (for SSE event routing)
//   - request mode and stream publisher (for live updates)
//   - auth identity and token bundle (for status tools that need credentials)
func rehydrateAsyncPollContext(src context.Context, dst context.Context, turn runtimerequestctx.TurnMeta) context.Context {
	if strings.TrimSpace(turn.ConversationID) != "" {
		dst = runtimerequestctx.WithConversationID(dst, strings.TrimSpace(turn.ConversationID))
	}
	dst = runtimerequestctx.WithTurnMeta(dst, turn)
	if msgID := strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(src)); msgID != "" {
		dst = runtimerequestctx.WithToolMessageID(dst, msgID)
	}
	assistantMessageID := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(src))
	if assistantMessageID == "" {
		assistantMessageID = strings.TrimSpace(runtimerequestctx.TurnModelMessageID(turn.TurnID))
	}
	if assistantMessageID == "" {
		assistantMessageID = strings.TrimSpace(turn.ParentMessageID)
	}
	if assistantMessageID != "" {
		dst = runtimerequestctx.WithModelMessageID(dst, assistantMessageID)
	}
	if mode := strings.TrimSpace(runtimerequestctx.RequestModeFromContext(src)); mode != "" {
		dst = runtimerequestctx.WithRequestMode(dst, mode)
	}
	if pub, ok := modelcallctx.StreamPublisherFromContext(src); ok {
		dst = modelcallctx.WithStreamPublisher(dst, pub)
	}
	if conv := convFromContext(src); conv != nil {
		dst = WithAsyncConversation(dst, conv)
	}
	if runner, ok := AsyncNarratorRunnerFromContext(src); ok {
		dst = WithAsyncNarratorRunner(dst, runner)
	}
	dst = runtimerequestctx.CloneUserAsk(dst, src)
	// Auth context — required for status tools that make authenticated outbound
	// calls (e.g. generic MCP/external async tools).
	if user := iauth.User(src); user != nil {
		dst = iauth.WithUserInfo(dst, user)
	}
	if tokens := iauth.TokensFromContext(src); tokens != nil {
		dst = iauth.WithTokens(dst, tokens)
	}
	if provider := iauth.Provider(src); provider != "" {
		dst = iauth.WithProvider(dst, provider)
	}
	return dst
}

// detachedAsyncPollContext is kept for call sites that pre-date the cancelable
// poller design. New code should use rehydrateAsyncPollContext with an explicit
// cancelable base context.
func detachedAsyncPollContext(ctx context.Context, turn runtimerequestctx.TurnMeta) context.Context {
	return rehydrateAsyncPollContext(ctx, context.Background(), turn)
}

// pollerState is the per-invocation setup of a PollAsyncOperation run.
// Split out so the individual tick-path helpers can be unit-tested with
// a synthetic state instead of standing up the full PollAsyncOperation
// loop + ticker.
type pollerState struct {
	manager    *asynccfg.Manager
	reg        tool.Registry
	cfg        *asynccfg.Config
	conv       apiconv.Client
	opID       string
	interval   time.Duration
	statusArgs map[string]interface{}
	cancelArgs map[string]interface{}
	narration  *asyncNarrationHandle
}

// newPollerState resolves the setup-time inputs for a poller run:
// narration handle (if a record exists), poll interval, pre-built
// status/cancel arg maps. Returns nil when required inputs are missing
// so PollAsyncOperation can bail fast.
func newPollerState(ctx context.Context, manager *asynccfg.Manager, reg tool.Registry, cfg *asynccfg.Config, opID string, conv apiconv.Client) *pollerState {
	if cfg == nil || manager == nil || reg == nil || strings.TrimSpace(opID) == "" {
		return nil
	}
	var narration *asyncNarrationHandle
	if rec, ok := manager.Get(context.Background(), opID); ok && rec != nil {
		narration = startAsyncNarration(ctx, cfg, StepInfo{
			ID:   "narrator:" + strings.TrimSpace(opID),
			Name: strings.TrimSpace(cfg.Status.Tool),
		}, rec)
	}
	interval := time.Duration(cfg.PollIntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = 2 * time.Second
	}
	cancelArgs := map[string]interface{}{}
	if cfg.Cancel != nil && strings.TrimSpace(cfg.Cancel.OperationIDArg) != "" {
		cancelArgs[cfg.Cancel.OperationIDArg] = opID
	}
	return &pollerState{
		manager:    manager,
		reg:        reg,
		cfg:        cfg,
		conv:       conv,
		opID:       opID,
		interval:   interval,
		statusArgs: resolvePollerStatusArgs(context.Background(), manager, cfg, opID),
		cancelArgs: cancelArgs,
		narration:  narration,
	}
}

// handleTimeoutIfExpired checks the op's wall-clock TimeoutAt and, if
// expired, fires the cancel tool (best-effort, warns on failure),
// transitions the op to StateFailed, narrates the change, persists, and
// publishes a final event. Returns true when the timeout was handled —
// the caller should stop the loop.
//
// now is injected so tests can drive the timeout deterministically
// without sleeping.
func (p *pollerState) handleTimeoutIfExpired(ctx context.Context, now time.Time) bool {
	rec, ok := p.manager.Get(context.Background(), p.opID)
	if !ok || rec == nil || rec.TimeoutAt == nil || !now.After(*rec.TimeoutAt) {
		return false
	}
	if p.cfg.Cancel != nil && strings.TrimSpace(p.cfg.Cancel.Tool) != "" {
		if _, err := p.reg.Execute(ctx, p.cfg.Cancel.Tool, p.cancelArgs); err != nil {
			// Cancel is best-effort on timeout. Don't block the terminal
			// transition on a failing cancel tool (silent failure
			// re-creates the problem TimeoutMs was supposed to prevent).
			// But log so operators see when cancel consistently fails.
			logx.WarnCtxf(ctx, "conversation", "async cancel tool failed op_id=%q cancel_tool=%q err=%v",
				strings.TrimSpace(p.opID),
				strings.TrimSpace(p.cfg.Cancel.Tool),
				err)
		}
	}
	payload := &asynccfg.Extracted{
		Status: "failed",
		Error:  "operation timed out",
	}
	rec, _ = p.manager.Update(context.Background(), asynccfg.UpdateInput{
		ID:     p.opID,
		Status: "failed",
		Error:  "operation timed out",
		State:  asynccfg.StateFailed,
	})
	if rec != nil {
		observeAsyncNarration(ctx, p.narration, changeEventFromRecord(rec))
	}
	patchAsyncToolPersistence(context.Background(), p.conv, rec, "operation timed out", nil)
	publishAsyncUpdateEvent(ctx, p.cfg.Run.Tool, rec.ToolCallID, p.opID, payload, rec)
	return true
}

// executeStatusTick runs one status-tool call and routes the result.
// Return values:
//
//   - `continueLoop`: true when the poller should loop again (transient
//     failure with unexhausted retries, or a non-terminal successful
//     tick). false when the loop should exit (ctx cancelled during
//     backoff, terminal failure, or terminal success).
//
// Splitting this out lets tests exercise each of the three outcome
// classes — (a) status-tool error with transient retry, (b) terminal
// failure from exhausted retries, (c) status success with extraction —
// by injecting a registry / manager pair and a single expected payload.
func (p *pollerState) executeStatusTick(ctx context.Context) (continueLoop bool) {
	// Use the poll context (ctx) so that the registry receives the same
	// conversation/turn/message/mode/publisher values assembled by
	// detachedAsyncPollContext, rather than a bare context.Background().
	result, err := p.reg.Execute(ctx, p.cfg.Status.Tool, p.statusArgs)
	if err != nil {
		rec, terminal := p.manager.RecordPollFailure(context.Background(), p.opID, err.Error(), isTransientPollError(err))
		if terminal {
			patchAsyncToolPersistence(context.Background(), p.conv, rec, err.Error(), nil)
			publishAsyncUpdateEvent(ctx, p.cfg.Status.Tool, rec.ToolCallID, p.opID, &asynccfg.Extracted{Status: "failed", Error: err.Error()}, rec)
			return false
		}
		if !waitPollBackoff(ctx, pollBackoff(p.interval, rec)) {
			return false
		}
		return true
	}
	_, _ = p.manager.ResetPollFailures(context.Background(), p.opID)
	payload, err := asynccfg.ExtractPayload(result, p.cfg.Status.Selector)
	if err != nil || payload == nil {
		// Extraction failure or nil payload: skip this tick but keep
		// the loop alive — the next poll may succeed (selector
		// extraction can transiently mismatch during streamed status
		// responses). Poll-failure counter is NOT bumped here; that
		// counter tracks status-tool-call errors only.
		return true
	}
	rec, _ := p.manager.Update(context.Background(), asynccfg.UpdateInput{
		ID:      p.opID,
		Status:  payload.Status,
		Message: payload.Message,
		Percent: payload.Percent,
		KeyData: cloneRaw(payload.KeyData),
		Error:   payload.Error,
	})
	if rec != nil {
		observeAsyncNarration(ctx, p.narration, changeEventFromRecord(rec))
	}
	patchAsyncToolPersistence(context.Background(), p.conv, rec, "", payload)
	publishAsyncUpdateEvent(ctx, p.cfg.Status.Tool, rec.ToolCallID, p.opID, payload, rec)
	if rec != nil && rec.Terminal() {
		return false
	}
	return true
}

// PollAsyncOperation drives the background status-polling loop for an
// async op. The heavy lifting is split into pollerState setup +
// handleTimeoutIfExpired + executeStatusTick so the tick-path logic is
// unit-testable with synthetic state. This function itself is just the
// loop/ticker orchestrator plus the narration lifecycle.
func PollAsyncOperation(ctx context.Context, manager *asynccfg.Manager, reg tool.Registry, cfg *asynccfg.Config, turn runtimerequestctx.TurnMeta, opID string, conv apiconv.Client) {
	// FinishPoller defer is placed FIRST so it always runs for an
	// admitted poller, even if newPollerState returns nil. Without
	// this, a pathological setup path (missing cfg/manager/reg/opID)
	// would leak the admission made by AdmitPoller in the caller —
	// m.pollers and pollerWG would drift out of alignment with the
	// actual goroutine population.
	if manager != nil {
		defer manager.FinishPoller(ctx, opID)
	}
	state := newPollerState(ctx, manager, reg, cfg, opID, conv)
	if state == nil {
		return
	}
	defer finishAsyncNarration(ctx, state.narration, opID, strings.TrimSpace(cfg.Status.Tool))

	ticker := time.NewTicker(state.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if state.handleTimeoutIfExpired(ctx, time.Now()) {
				return
			}
			if !state.executeStatusTick(ctx) {
				return
			}
		}
	}
}

// resolvePollerStatusArgs returns the status arguments the poller should use for
// every subsequent status-tool call. It prefers the fully-prepared StatusArgs
// already stored on the OperationRecord (which captures ReuseRunArgs and
// ExtraArgs), and falls back to deriving the minimal set from cfg when the
// record is not available.
func resolvePollerStatusArgs(ctx context.Context, manager *asynccfg.Manager, cfg *asynccfg.Config, opID string) map[string]interface{} {
	if rec, ok := manager.Get(ctx, opID); ok && rec != nil && len(rec.StatusArgs) > 0 {
		// Clone so the poller has a stable, immutable copy.
		args := make(map[string]interface{}, len(rec.StatusArgs))
		for k, v := range rec.StatusArgs {
			args[k] = v
		}
		return args
	}
	// Fallback: reconstruct from config alone (no ReuseRunArgs support in this path).
	args := map[string]interface{}{}
	if arg := strings.TrimSpace(cfg.Status.OperationIDArg); arg != "" {
		args[arg] = strings.TrimSpace(opID)
	}
	for k, v := range cfg.Status.ExtraArgs {
		args[k] = v
	}
	return args
}

func patchAsyncToolPersistence(ctx context.Context, conv apiconv.Client, rec *asynccfg.OperationRecord, fallbackErr string, payload *asynccfg.Extracted) {
	if conv == nil || rec == nil || strings.TrimSpace(rec.ToolMessageID) == "" {
		return
	}
	if !asynccfg.ExecutionModeWaits(rec.ExecutionMode) {
		return
	}
	content := asyncPersistenceContent(rec, payload)
	respPayloadID := ""
	if payload != nil {
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

func asyncPersistenceContent(rec *asynccfg.OperationRecord, payload *asynccfg.Extracted) string {
	if rec == nil || payload == nil {
		return ""
	}
	toolName := strings.TrimSpace(rec.ToolName)
	if sameToolName(toolName, "llm/agents:start") || sameToolName(toolName, "llm/agents:status") {
		kind := "progress"
		if rec.Terminal() {
			kind = "answer"
		}
		message := strings.TrimSpace(payload.Message)
		if rec.Terminal() && message == "" {
			message = strings.TrimSpace(payload.Status)
		}
		doc := map[string]interface{}{
			"conversationId": strings.TrimSpace(rec.ID),
			"status":         strings.TrimSpace(payload.Status),
		}
		if message != "" {
			doc["message"] = message
			doc["messageKind"] = kind
		}
		if rec.Terminal() {
			doc["hasFinalResponse"] = strings.TrimSpace(message) != ""
		}
		if data, err := json.Marshal(doc); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	switch {
	case rec.Terminal() && len(payload.KeyData) > 0:
		return strings.TrimSpace(string(payload.KeyData))
	case strings.TrimSpace(payload.Message) != "":
		return strings.TrimSpace(payload.Message)
	case strings.TrimSpace(payload.Status) != "":
		return strings.TrimSpace(payload.Status)
	default:
		return ""
	}
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

func publishAsyncUpdateEvent(ctx context.Context, toolName, toolCallID, opID string, payload *asynccfg.Extracted, rec *asynccfg.OperationRecord) {
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
	publishAsyncLifecycleEvent(ctx, toolName, toolCallID, opID, eventType, payload)
}

func publishAsyncLifecycleEvent(ctx context.Context, toolName, toolCallID, opID string, eventType streaming.EventType, payload *asynccfg.Extracted) {
	pub, ok := modelcallctx.StreamPublisherFromContext(ctx)
	if !ok {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	assistantMessageID := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	if assistantMessageID == "" {
		assistantMessageID = strings.TrimSpace(runtimerequestctx.TurnModelMessageID(turn.TurnID))
	}
	if assistantMessageID == "" {
		assistantMessageID = strings.TrimSpace(turn.ParentMessageID)
	}
	event := &streaming.Event{
		Type:               eventType,
		ConversationID:     strings.TrimSpace(turn.ConversationID),
		StreamID:           strings.TrimSpace(turn.ConversationID),
		TurnID:             strings.TrimSpace(turn.TurnID),
		MessageID:          strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)),
		ToolMessageID:      strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)),
		ToolCallID:         strings.TrimSpace(toolCallID),
		OperationID:        strings.TrimSpace(opID),
		ToolName:           strings.TrimSpace(toolName),
		AssistantMessageID: assistantMessageID,
		ParentMessageID:    strings.TrimSpace(turn.ParentMessageID),
		CreatedAt:          time.Now(),
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
