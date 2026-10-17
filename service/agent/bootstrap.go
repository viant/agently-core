package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	agproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	intakesvc "github.com/viant/agently-core/service/intake"
)

type bootstrapCacheEntry struct {
	content string
}

type bootstrapPrefetchKey struct{}

type bootstrapPrefetchResult struct {
	done chan struct{}
	doc  *binding.Document
	err  error
}

// bootstrapPrefetch is request-owned. In particular failures are retained here,
// rather than entering the existing cross-request success cache or being retried
// immediately when binding consumes the result.
type bootstrapPrefetch struct {
	agentID string
	userID  string
	scope   namespaceScope
	calls   map[string]*bootstrapPrefetchResult
}

func bootstrapPrefetchCallKey(call agproto.BootstrapToolCall) (string, error) {
	raw, err := json.Marshal(call)
	return string(raw), err
}

// startBootstrapPrefetch runs only explicitly opted agent calls. It neither
// selects a profile nor injects documents; binding remains that boundary.
func (s *Service) startBootstrapPrefetch(ctx context.Context, input *QueryInput) (context.Context, func(), func()) {
	// Nested/delegated queries must never consume another request's futures.
	ctx = context.WithValue(ctx, bootstrapPrefetchKey{}, (*bootstrapPrefetch)(nil))
	noop := func() {}
	if s == nil || s.registry == nil || s.intakeSvc == nil || input == nil || input.Agent == nil || !input.Agent.Intake.Enabled || strings.TrimSpace(input.Query) == "" {
		return ctx, noop, noop
	}
	opted := false
	for _, call := range input.Agent.Bootstrap.ToolCalls {
		if call.Prefetch {
			opted = true
			break
		}
	}
	if !opted {
		return ctx, noop, noop
	}
	if input.EffectiveIntentProfileID() != "" || !s.shouldRunIntake(ctx, input, &input.Agent.Intake) || s.resolveWorkspaceUIIntentOverride(ctx, input) != nil {
		return ctx, noop, noop
	}
	if text, _ := presetAssistantFromContext(input.Context); text != "" || directActionFromContext(input.Context) != nil {
		return ctx, noop, noop
	}
	if existing := intakesvc.FromContext(input.Context); existing != nil && existing.Routing.Source == intakesvc.SourceCallerProvided {
		return ctx, noop, noop
	}
	// These are the only input fields used by bootstrap args, cache keys and
	// events. No worker retains QueryInput or mutable agent/request maps.
	started := time.Now()
	exposure := resolveToolCallExposure(input)
	snapshot := &QueryInput{Query: input.Query, ConversationID: input.ConversationID, UserId: input.UserId,
		ToolCallExposure: &exposure, Agent: &agproto.Agent{Identity: agproto.Identity{ID: input.Agent.ID}}}
	batch := &bootstrapPrefetch{agentID: input.Agent.ID, userID: input.UserId, scope: namespaceScopeFrom(ctx), calls: map[string]*bootstrapPrefetchResult{}}
	workerCtx, cancel := context.WithCancel(ctx)
	var timeout time.Duration
	if s.defaults != nil {
		timeout = time.Duration(s.defaults.ToolCallTimeoutSec) * time.Second
	}
	for _, call := range input.Agent.Bootstrap.ToolCalls {
		if !call.Prefetch || strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Tool) == "" || !bootstrapSystemInjection(call) {
			continue
		}
		key, err := s.bootstrapPrefetchKey(ctx, snapshot, call)
		if err != nil || batch.calls[key] != nil {
			continue // Existing binding reports serialization errors as before.
		}
		frozen := call
		args, safe := snapshotBootstrapValue(call.Args, 0)
		if !safe {
			// Unsupported typed values retain serial execution.
			continue
		}
		if args != nil {
			frozen.Args = args.(map[string]interface{})
		}
		if call.Inject.IncludeHeader != nil {
			value := *call.Inject.IncludeHeader
			frozen.Inject.IncludeHeader = &value
		}
		result := &bootstrapPrefetchResult{done: make(chan struct{})}
		batch.calls[key] = result
		go func() {
			defer close(result.done)
			callStarted := time.Now()
			defer func() {
				turn, _ := runtimerequestctx.TurnMetaFromContext(workerCtx)
				debugtrace.Write("agent", "bootstrap_prefetch_stage", map[string]interface{}{"conversationID": snapshot.ConversationID, "turnID": turn.TurnID, "stage": "execute", "bootstrapID": frozen.ID, "elapsedMs": float64(time.Since(callStarted)) / float64(time.Millisecond), "failed": result.err != nil})
			}()
			callCtx := workerCtx
			if timeout > 0 {
				var stop context.CancelFunc
				callCtx, stop = context.WithTimeout(callCtx, timeout)
				defer stop()
			}
			result.doc, result.err = s.bootstrapSystemDocument(callCtx, snapshot, frozen)
		}()
	}
	if len(batch.calls) == 0 {
		cancel()
		return ctx, noop, noop
	}
	join := func() {
		joinStarted := time.Now()
		defer func() {
			turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
			debugtrace.Write("agent", "bootstrap_prefetch_stage", map[string]interface{}{"conversationID": snapshot.ConversationID, "turnID": turn.TurnID, "stage": "join", "calls": len(batch.calls), "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond), "waitMs": float64(time.Since(joinStarted)) / float64(time.Millisecond)})
		}()
		for _, result := range batch.calls {
			select {
			case <-result.done:
			case <-ctx.Done():
				return
			}
		}
	}
	return context.WithValue(ctx, bootstrapPrefetchKey{}, batch), join, cancel
}

func (s *Service) bootstrapPrefetchKey(ctx context.Context, input *QueryInput, call agproto.BootstrapToolCall) (string, error) {
	config, err := bootstrapPrefetchCallKey(call)
	if err != nil {
		return "", err
	}
	key, err := s.bootstrapCacheKey(ctx, input, call)
	// Even conversation-exposed bootstrap futures belong to one turn. The
	// existing success cache retains its original exposure semantics separately.
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	return key + "\x00" + turn.TurnID + "\x00" + config, err
}

// Reuse the JSON-native copier, adding only immutable Go numeric scalars and
// YAML's common []string shape. Custom/typed mutable values remain serial.
func snapshotBootstrapValue(value interface{}, depth int) (interface{}, bool) {
	if depth > 1000 {
		return nil, false
	}
	if cloned, ok := cloneJSONValue(value, depth); ok {
		return cloned, true
	}
	switch value := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, json.Number:
		return value, true
	case []string:
		if value == nil {
			return []string(nil), true
		}
		out := make([]string, len(value))
		copy(out, value)
		return out, true
	case map[string]interface{}:
		out := make(map[string]interface{}, len(value))
		for key, item := range value {
			cloned, ok := snapshotBootstrapValue(item, depth+1)
			if !ok {
				return nil, false
			}
			out[key] = cloned
		}
		return out, true
	case []interface{}:
		out := make([]interface{}, len(value))
		for i, item := range value {
			cloned, ok := snapshotBootstrapValue(item, depth+1)
			if !ok {
				return nil, false
			}
			out[i] = cloned
		}
		return out, true
	}
	return nil, false
}

func bootstrapSystemInjection(call agproto.BootstrapToolCall) bool {
	switch strings.ToLower(strings.TrimSpace(call.Inject.As)) {
	case "", "systemcontext", "system_context", "systemdocument", "system_document":
		return true
	}
	return false
}

func (s *Service) appendBootstrapSystemDocuments(ctx context.Context, input *QueryInput, b *binding.Binding) error {
	if s == nil || input == nil || input.Agent == nil || b == nil || s.registry == nil {
		return nil
	}
	for _, call := range s.effectiveBootstrapCalls(ctx, input) {
		if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Tool) == "" {
			continue
		}
		injectAs := strings.ToLower(strings.TrimSpace(call.Inject.As))
		if injectAs == "" {
			injectAs = "systemcontext"
		}
		if injectAs != "systemcontext" && injectAs != "system_context" && injectAs != "systemdocument" && injectAs != "system_document" {
			if call.Required {
				return fmt.Errorf("bootstrap tool call %q has unsupported inject.as: %s", call.ID, call.Inject.As)
			}
			continue
		}
		doc, err := s.bootstrapSystemDocument(ctx, input, call)
		if err != nil {
			if call.Required {
				return err
			}
			logx.Warnf("conversation", "bootstrap tool call skipped agent_id=%q id=%q tool=%q err=%v", strings.TrimSpace(input.Agent.ID), strings.TrimSpace(call.ID), strings.TrimSpace(call.Tool), err)
			continue
		}
		if doc == nil || strings.TrimSpace(doc.PageContent) == "" {
			continue
		}
		if hasDocumentURI(b.SystemDocuments.Items, doc.SourceURI) {
			continue
		}
		b.SystemDocuments.Items = append(b.SystemDocuments.Items, doc)
	}
	return nil
}

// effectiveBootstrapCalls composes agent and selected-profile metadata for the
// one existing bootstrap executor. Profiles customize behavior without owning
// a second execution path.
func (s *Service) effectiveBootstrapCalls(ctx context.Context, input *QueryInput) []agproto.BootstrapToolCall {
	if input == nil || input.Agent == nil {
		return nil
	}
	result := append([]agproto.BootstrapToolCall(nil), input.Agent.Bootstrap.ToolCalls...)
	profile, err := s.selectedPromptProfileAny(ctx, input)
	if err != nil || profile == nil || len(profile.Bootstrap) == 0 {
		return result
	}
	seen := map[string]bool{}
	for _, call := range result {
		if id := strings.TrimSpace(call.ID); id != "" {
			seen[id] = true
		}
	}
	for _, call := range profile.Bootstrap {
		id := strings.TrimSpace(call.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, call)
	}
	return result
}

func (s *Service) bootstrapSystemDocument(ctx context.Context, input *QueryInput, call agproto.BootstrapToolCall) (*binding.Document, error) {
	if batch, ok := ctx.Value(bootstrapPrefetchKey{}).(*bootstrapPrefetch); ok && batch != nil && input != nil && input.Agent != nil && batch.agentID == input.Agent.ID && batch.userID == input.UserId && batch.scope == namespaceScopeFrom(ctx) {
		if key, err := s.bootstrapPrefetchKey(ctx, input, call); err == nil {
			if result := batch.calls[key]; result != nil {
				select {
				case <-result.done:
					return result.doc, result.err
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
	}
	args := expandBootstrapArgs(ctx, input, call.Args)
	key, err := s.bootstrapCacheKey(ctx, input, call)
	if err != nil {
		return nil, err
	}
	if cached, ok := s.bootstrapCache.Load(key); ok {
		if entry, ok := cached.(bootstrapCacheEntry); ok {
			return s.newBootstrapDocument(call, entry.content), nil
		}
	}

	toolName := strings.TrimSpace(call.Tool)
	toolCallID := "bootstrap:" + strings.TrimSpace(call.ID)
	s.publishBootstrapToolEvent(ctx, input, streaming.EventTypeToolCallStarted, toolCallID, toolName, args, "")
	result, err := s.registry.Execute(ctx, toolName, args)
	if err != nil {
		s.publishBootstrapToolEvent(ctx, input, streaming.EventTypeToolCallFailed, toolCallID, toolName, args, err.Error())
		return nil, fmt.Errorf("bootstrap tool call %q (%s) failed: %w", strings.TrimSpace(call.ID), toolName, err)
	}
	s.publishBootstrapToolEvent(ctx, input, streaming.EventTypeToolCallCompleted, toolCallID, toolName, args, "")

	content := renderBootstrapToolContext(call, args, result)
	s.bootstrapCache.Store(key, bootstrapCacheEntry{content: content})
	return s.newBootstrapDocument(call, content), nil
}

func (s *Service) newBootstrapDocument(call agproto.BootstrapToolCall, content string) *binding.Document {
	title := strings.TrimSpace(call.Inject.Title)
	if title == "" {
		title = "bootstrap/" + strings.TrimSpace(call.ID)
	}
	sourceURI := strings.TrimSpace(call.Inject.SourceURI)
	if sourceURI == "" {
		sourceURI = "internal://bootstrap/" + strings.TrimSpace(call.ID)
	}
	if budget := call.Inject.BudgetChars; budget > 0 && len(content) > budget {
		content = strings.TrimSpace(content[:budget]) + "\n\n<!-- bootstrap context truncated -->"
	}
	return &binding.Document{
		Title:       title,
		PageContent: content,
		SourceURI:   sourceURI,
		MimeType:    "text/markdown",
		Metadata: map[string]string{
			"kind": "bootstrap_tool_result",
			"id":   strings.TrimSpace(call.ID),
			"tool": strings.TrimSpace(call.Tool),
		},
	}
}

func renderBootstrapToolContext(call agproto.BootstrapToolCall, args map[string]interface{}, result string) string {
	argsJSON := "{}"
	if len(args) > 0 {
		if data, err := json.MarshalIndent(args, "", "  "); err == nil {
			argsJSON = string(data)
		}
	}
	result = strings.TrimSpace(result)
	if result == "" {
		result = "(empty result)"
	}
	var b strings.Builder
	if call.Inject.IncludeHeader == nil || *call.Inject.IncludeHeader {
		header := strings.TrimSpace(call.Inject.Header)
		if header == "" {
			header = defaultBootstrapHeader(call, argsJSON)
		} else {
			header = expandBootstrapHeader(header, call, argsJSON)
		}
		if header != "" {
			b.WriteString(header)
			b.WriteString("\n\n")
		}
	}
	b.WriteString("## Result\n\n")
	b.WriteString(result)
	return strings.TrimSpace(b.String())
}

func defaultBootstrapHeader(call agproto.BootstrapToolCall, argsJSON string) string {
	var b strings.Builder
	b.WriteString("# Runtime Bootstrap Tool Result\n\n")
	b.WriteString("This system context was produced by a runtime-owned bootstrap tool call before model reasoning. It is not transcript history and was not emitted by the assistant.\n\n")
	b.WriteString("- Bootstrap ID: `")
	b.WriteString(strings.TrimSpace(call.ID))
	b.WriteString("`\n")
	b.WriteString("- Tool: `")
	b.WriteString(strings.TrimSpace(call.Tool))
	b.WriteString("`\n")
	b.WriteString("- Args:\n\n```json\n")
	b.WriteString(argsJSON)
	b.WriteString("\n```")
	return strings.TrimSpace(b.String())
}

func expandBootstrapHeader(header string, call agproto.BootstrapToolCall, argsJSON string) string {
	replacer := strings.NewReplacer(
		"{{id}}", strings.TrimSpace(call.ID),
		"{{tool}}", strings.TrimSpace(call.Tool),
		"{{args}}", argsJSON,
	)
	return strings.TrimSpace(replacer.Replace(header))
}

func (s *Service) bootstrapCacheKey(ctx context.Context, input *QueryInput, call agproto.BootstrapToolCall) (string, error) {
	argsData, err := json.Marshal(expandBootstrapArgs(ctx, input, call.Args))
	if err != nil {
		return "", fmt.Errorf("bootstrap tool call %q args are not JSON-serializable: %w", strings.TrimSpace(call.ID), err)
	}
	exposure := resolveToolCallExposure(input)
	conversationID := ""
	if input != nil {
		conversationID = strings.TrimSpace(input.ConversationID)
	}
	turnID := ""
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = strings.TrimSpace(tm.TurnID)
	}
	agentID := ""
	if input != nil && input.Agent != nil {
		agentID = strings.TrimSpace(input.Agent.ID)
	}
	identity := namespaceScopeFrom(ctx)
	parts := []string{
		"bootstrap",
		identity.user, identity.canonical, identity.provider,
		strings.ToLower(strings.TrimSpace(string(exposure))),
		conversationID,
		agentID,
		strings.TrimSpace(call.ID),
		strings.TrimSpace(call.Tool),
		string(argsData),
	}
	if !strings.EqualFold(strings.TrimSpace(string(exposure)), "conversation") {
		parts = append(parts, turnID)
	}
	return strings.Join(parts, "\x00"), nil
}

func cloneBootstrapArgs(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return map[string]interface{}{}
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func expandBootstrapArgs(ctx context.Context, input *QueryInput, src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return map[string]interface{}{}
	}
	replacements := bootstrapArgReplacements(ctx, input)
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = expandBootstrapValue(v, replacements)
	}
	return dst
}

func bootstrapArgReplacements(ctx context.Context, input *QueryInput) map[string]string {
	replacements := map[string]string{}
	if input != nil {
		replacements["query"] = input.Query
		replacements["conversationId"] = strings.TrimSpace(input.ConversationID)
		replacements["userId"] = strings.TrimSpace(input.UserId)
		if input.Agent != nil {
			replacements["agentId"] = strings.TrimSpace(input.Agent.ID)
		}
	}
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		replacements["turnId"] = strings.TrimSpace(tm.TurnID)
		if replacements["conversationId"] == "" {
			replacements["conversationId"] = strings.TrimSpace(tm.ConversationID)
		}
		if replacements["agentId"] == "" {
			replacements["agentId"] = strings.TrimSpace(tm.Assistant)
		}
	}
	return replacements
}

func expandBootstrapValue(value interface{}, replacements map[string]string) interface{} {
	switch actual := value.(type) {
	case string:
		return expandBootstrapTemplate(actual, replacements)
	case []interface{}:
		out := make([]interface{}, 0, len(actual))
		for _, item := range actual {
			out = append(out, expandBootstrapValue(item, replacements))
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(actual))
		for k, v := range actual {
			out[k] = expandBootstrapValue(v, replacements)
		}
		return out
	default:
		return value
	}
}

func expandBootstrapTemplate(value string, replacements map[string]string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(replacements) == 0 {
		return value
	}
	pairs := make([]string, 0, len(replacements)*2)
	for key, replacement := range replacements {
		pairs = append(pairs, "{{"+key+"}}", replacement)
	}
	return strings.NewReplacer(pairs...).Replace(value)
}

func (s *Service) publishBootstrapToolEvent(ctx context.Context, input *QueryInput, eventType streaming.EventType, toolCallID, toolName string, args map[string]interface{}, errText string) {
	if s == nil || s.streamPub == nil {
		return
	}
	conversationID := ""
	turnID := ""
	agentID := ""
	if input != nil {
		conversationID = strings.TrimSpace(input.ConversationID)
		if input.Agent != nil {
			agentID = strings.TrimSpace(input.Agent.ID)
		}
	}
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = strings.TrimSpace(tm.TurnID)
	}
	pageID := "bootstrap"
	if turnID != "" {
		pageID = turnID + ":bootstrap"
	}
	event := &streaming.Event{
		Type:           eventType,
		StreamID:       conversationID,
		ConversationID: conversationID,
		TurnID:         turnID,
		PageID:         pageID,
		ToolCallID:     toolCallID,
		ToolName:       toolName,
		Arguments:      args,
		ExecutionRole:  "bootstrap",
		Phase:          "bootstrap",
		Mode:           "systemContext",
		AgentIDUsed:    agentID,
		Status:         bootstrapToolEventStatus(eventType),
		Error:          strings.TrimSpace(errText),
		CreatedAt:      time.Now(),
	}
	event.NormalizeIdentity(conversationID, turnID)
	if err := s.streamPub.Publish(ctx, event); err != nil {
		logx.Warnf("conversation", "bootstrap tool event publish error convo=%q turn=%q tool=%q err=%v", conversationID, turnID, toolName, err)
	}
}

func bootstrapToolEventStatus(eventType streaming.EventType) string {
	switch eventType {
	case streaming.EventTypeToolCallStarted, streaming.EventTypeToolCallWaiting:
		return "running"
	case streaming.EventTypeToolCallCompleted:
		return "completed"
	case streaming.EventTypeToolCallFailed:
		return "failed"
	case streaming.EventTypeToolCallCanceled:
		return "canceled"
	default:
		return ""
	}
}
