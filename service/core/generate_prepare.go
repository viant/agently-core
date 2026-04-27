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
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
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
	WriteLLMRequestDebugPayload(ctx, strings.TrimSpace(input.Model), request, nil, "")
	return request, model, nil
}

func WriteLLMRequestDebugPayload(ctx context.Context, modelName string, request *llm.GenerateRequest, extraDebugContext map[string]interface{}, traceSuffix string) {
	if strings.TrimSpace(debugtrace.PayloadDir()) == "" || request == nil {
		return
	}
	debugContext := map[string]interface{}{
		"conversationId": strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)),
	}
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		if v := strings.TrimSpace(turn.TurnID); v != "" {
			debugContext["turnId"] = v
		}
	}
	if runMeta, ok := runtimerequestctx.RunMetaFromContext(ctx); ok {
		if v := strings.TrimSpace(runMeta.RunID); v != "" {
			debugContext["runId"] = v
		}
		if runMeta.Iteration > 0 {
			debugContext["iteration"] = runMeta.Iteration
		}
	}
	if workdir, ok := toolexec.WorkdirFromContext(ctx); ok && strings.TrimSpace(workdir) != "" {
		debugContext["workdir"] = strings.TrimSpace(workdir)
	}
	if request.Options != nil && request.Options.Metadata != nil {
		if v, ok := request.Options.Metadata["modelSource"].(string); ok && strings.TrimSpace(v) != "" {
			debugContext["modelSource"] = strings.TrimSpace(v)
		}
		if v, ok := request.Options.Metadata["asyncNarrator"].(bool); ok && v {
			debugContext["asyncNarrator"] = true
		}
		if v, ok := request.Options.Metadata["asyncNarrationMode"].(string); ok && strings.TrimSpace(v) != "" {
			debugContext["asyncNarrationMode"] = strings.TrimSpace(v)
		}
		if v, ok := request.Options.Metadata["asyncNarratorOpID"].(string); ok && strings.TrimSpace(v) != "" {
			debugContext["asyncNarratorOpID"] = strings.TrimSpace(v)
		}
		if v, ok := request.Options.Metadata["asyncNarratorUserAsk"].(string); ok && strings.TrimSpace(v) != "" {
			debugContext["asyncNarratorUserAsk"] = strings.TrimSpace(v)
		}
		if v, ok := request.Options.Metadata["asyncNarratorIntent"].(string); ok && strings.TrimSpace(v) != "" {
			debugContext["asyncNarratorIntent"] = strings.TrimSpace(v)
		}
		if v, ok := request.Options.Metadata["asyncNarratorSummary"].(string); ok && strings.TrimSpace(v) != "" {
			debugContext["asyncNarratorSummary"] = strings.TrimSpace(v)
		}
		if v, ok := request.Options.Metadata["asyncNarratorTool"].(string); ok && strings.TrimSpace(v) != "" {
			debugContext["asyncNarratorTool"] = strings.TrimSpace(v)
		}
		if v, ok := request.Options.Metadata["asyncNarratorStatus"].(string); ok && strings.TrimSpace(v) != "" {
			debugContext["asyncNarratorStatus"] = strings.TrimSpace(v)
		}
		if v, ok := request.Options.Metadata["activeSkillNames"]; ok {
			switch names := v.(type) {
			case []interface{}:
				if len(names) > 0 {
					debugContext["activeSkillNames"] = names
				}
			case []string:
				if len(names) > 0 {
					debugContext["activeSkillNames"] = names
				}
			}
		}
		if v, ok := request.Options.Metadata["activeSkillModel"].(string); ok && strings.TrimSpace(v) != "" {
			debugContext["activeSkillModel"] = strings.TrimSpace(v)
		}
		if v, ok := request.Options.Metadata["activeSkillSourceName"].(string); ok && strings.TrimSpace(v) != "" {
			debugContext["activeSkillSourceName"] = strings.TrimSpace(v)
		}
		if v, ok := request.Options.Metadata["activeSkillPreprocess"].(bool); ok && v {
			debugContext["activeSkillPreprocess"] = true
		}
		if v, ok := request.Options.Metadata["activeSkillPreprocessTimeoutSeconds"]; ok {
			debugContext["activeSkillPreprocessTimeoutSeconds"] = v
		}
	}
	for k, v := range extraDebugContext {
		debugContext[k] = v
	}
	payload := map[string]interface{}{
		"model":        strings.TrimSpace(modelName),
		"instructions": request.Instructions,
		"messages":     request.Messages,
		"options":      request.Options,
		"debugContext": debugContext,
	}
	if data, mErr := json.MarshalIndent(payload, "", "  "); mErr == nil {
		traceID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
		if traceID == "" {
			traceID = strings.TrimSpace(modelName)
		}
		if strings.TrimSpace(traceSuffix) != "" {
			traceID = traceID + "-" + strings.TrimSpace(traceSuffix)
		}
		_ = debugtrace.WritePayload("llm-request", traceID, data)
		if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok && strings.TrimSpace(turn.TurnID) != "" {
			traceID = strings.TrimSpace(turn.TurnID)
			if runMeta, ok := runtimerequestctx.RunMetaFromContext(ctx); ok && runMeta.Iteration > 0 {
				traceID = fmt.Sprintf("%s-iter_%d", traceID, runMeta.Iteration)
			}
			if strings.TrimSpace(traceSuffix) != "" {
				traceID = traceID + "-" + strings.TrimSpace(traceSuffix)
			}
			_ = debugtrace.WritePayload("llm-request", traceID, data)
		}
	}
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
