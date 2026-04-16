package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

func (s *Service) prepareGenerateRequest(ctx context.Context, input *GenerateInput) (*llm.GenerateRequest, llm.Model, error) {
	input.MatchModelIfNeeded(s.modelMatcher)
	if input.Binding == nil {
		input.Binding = &binding.Binding{}
	}
	model, err := s.llmFinder.Find(ctx, input.Model)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find model: %w", err)
	}
	normalizeModelNativeCapabilities(input.Options, model, input.Model)
	s.updateFlags(input, model)
	if err := input.Init(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to init generate input: %w", err)
	}
	if err := input.Validate(ctx); err != nil {
		return nil, nil, err
	}
	if err := s.enforceAttachmentPolicy(ctx, input, model); err != nil {
		return nil, nil, err
	}

	request := &llm.GenerateRequest{
		Messages:     input.Message,
		Options:      input.Options,
		Instructions: input.Instructions,
	}
	if logx.Enabled() {
		toolNames := make([]string, 0)
		if request.Options != nil {
			toolNames = make([]string, 0, len(request.Options.Tools))
			for _, item := range request.Options.Tools {
				if name := strings.TrimSpace(item.Definition.Name); name != "" {
					toolNames = append(toolNames, name)
				}
			}
		}
		logx.Debugf("core", "request model=%q tool_choice=%+v tools=%q messages=%d",
			strings.TrimSpace(input.Model),
			func() interface{} {
				if request.Options == nil {
					return nil
				}
				return request.Options.ToolChoice
			}(),
			strings.Join(toolNames, ","),
			len(request.Messages),
		)
	}
	if convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)); convID != "" {
		request.PromptCacheKey = convID
	}
	applyInstructionsDefaults(request, model)
	{
		toolNames := make([]string, 0)
		if request.Options != nil {
			toolNames = make([]string, 0, len(request.Options.Tools))
			for _, item := range request.Options.Tools {
				if name := strings.TrimSpace(item.Definition.Name); name != "" {
					toolNames = append(toolNames, name)
				}
			}
		}
		msgs := make([]map[string]string, 0, len(request.Messages))
		for _, m := range request.Messages {
			entry := map[string]string{
				"role":       string(m.Role),
				"contentLen": fmt.Sprintf("%d", len(m.Content)),
			}
			if len(m.Content) > 0 {
				head := m.Content
				if len(head) > 120 {
					head = head[:120] + "..."
				}
				entry["head"] = head
			}
			if m.ToolCallId != "" {
				entry["tool_call_id"] = m.ToolCallId
			}
			if len(m.ToolCalls) > 0 {
				names := make([]string, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					names = append(names, tc.Name)
				}
				entry["tool_calls"] = strings.Join(names, ",")
			}
			msgs = append(msgs, entry)
		}
		debugtrace.LogToFile("llm", "request", map[string]interface{}{
			"model":        strings.TrimSpace(input.Model),
			"msgCount":     len(request.Messages),
			"toolCount":    len(toolNames),
			"tools":        strings.Join(toolNames, ","),
			"messages":     msgs,
			"fullMessages": request.Messages,
			"instructions": request.Instructions,
		})
	}
	if debugtrace.Enabled() {
		payload := map[string]interface{}{
			"model":        strings.TrimSpace(input.Model),
			"instructions": request.Instructions,
			"messages":     request.Messages,
			"options":      request.Options,
		}
		if data, mErr := json.MarshalIndent(payload, "", "  "); mErr == nil {
			traceID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
			if traceID == "" {
				traceID = strings.TrimSpace(input.Model)
			}
			_ = debugtrace.WritePayload("llm-request", traceID, data)
		}
	}
	return request, model, nil
}

func normalizeModelNativeCapabilities(options *llm.Options, model llm.Model, modelName string) {
	if options == nil || options.Metadata == nil {
		return
	}
	if v, ok := options.Metadata["modelArtifactGeneration"].(bool); ok && v {
		if model == nil || !model.Implements(base.SupportsModelArtifactGeneration) {
			delete(options.Metadata, "modelArtifactGeneration")
			logx.Warnf("core", "model=%q does not support modelArtifactGeneration; continuing without native artifact generation", strings.TrimSpace(modelName))
		}
	}
}

func applyInstructionsDefaults(request *llm.GenerateRequest, model llm.Model) {
	if request == nil {
		return
	}
	supportsInstructions := model != nil && model.Implements(base.SupportsInstructions)
	if !supportsInstructions && strings.TrimSpace(request.Instructions) != "" {
		for _, msg := range request.Messages {
			if msg.Role == llm.RoleSystem {
				return
			}
		}
		request.Messages = append([]llm.Message{llm.NewSystemMessage(request.Instructions)}, request.Messages...)
	}
}

func (s *Service) updateFlags(input *GenerateInput, model llm.Model) {
	input.Binding.Flags.CanUseTool = model.Implements(base.CanUseTools)
	input.Binding.Flags.CanStream = model.Implements(base.CanStream)
	input.Binding.Flags.IsMultimodal = model.Implements(base.IsMultimodal)
	if input.Options != nil && input.Options.ParallelToolCalls && !model.Implements(base.CanExecToolsInParallel) {
		input.Options.ParallelToolCalls = false
	}
}
