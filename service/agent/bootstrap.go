package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/logx"
	agproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

type bootstrapCacheEntry struct {
	content string
}

func (s *Service) appendBootstrapSystemDocuments(ctx context.Context, input *QueryInput, b *binding.Binding) error {
	if s == nil || input == nil || input.Agent == nil || b == nil || s.registry == nil {
		return nil
	}
	for _, call := range input.Agent.Bootstrap.ToolCalls {
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

func (s *Service) bootstrapSystemDocument(ctx context.Context, input *QueryInput, call agproto.BootstrapToolCall) (*binding.Document, error) {
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
	args := cloneBootstrapArgs(call.Args)
	toolCallID := "bootstrap:" + strings.TrimSpace(call.ID)
	s.publishBootstrapToolEvent(ctx, input, streaming.EventTypeToolCallStarted, toolCallID, toolName, args, "")
	result, err := s.registry.Execute(ctx, toolName, args)
	if err != nil {
		s.publishBootstrapToolEvent(ctx, input, streaming.EventTypeToolCallFailed, toolCallID, toolName, args, err.Error())
		return nil, fmt.Errorf("bootstrap tool call %q (%s) failed: %w", strings.TrimSpace(call.ID), toolName, err)
	}
	s.publishBootstrapToolEvent(ctx, input, streaming.EventTypeToolCallCompleted, toolCallID, toolName, args, "")

	content := renderBootstrapToolContext(call, result)
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

func renderBootstrapToolContext(call agproto.BootstrapToolCall, result string) string {
	argsJSON := "{}"
	if len(call.Args) > 0 {
		if data, err := json.MarshalIndent(call.Args, "", "  "); err == nil {
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
	argsData, err := json.Marshal(cloneBootstrapArgs(call.Args))
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
	parts := []string{
		"bootstrap",
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
		Error:          strings.TrimSpace(errText),
		CreatedAt:      time.Now(),
	}
	event.NormalizeIdentity(conversationID, turnID)
	if err := s.streamPub.Publish(ctx, event); err != nil {
		logx.Warnf("conversation", "bootstrap tool event publish error convo=%q turn=%q tool=%q err=%v", conversationID, turnID, toolName, err)
	}
}
