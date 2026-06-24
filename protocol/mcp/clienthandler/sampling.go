package clienthandler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/jsonrpc"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

const samplingAuditSubtype = "mcp_sampling"

type Option func(*Handler)

type Sampler struct {
	modelFinder  llm.Finder
	defaultModel string
}

func WithModelFinder(finder llm.Finder) Option {
	return func(h *Handler) {
		if h.sampler == nil {
			h.sampler = &Sampler{}
		}
		h.sampler.modelFinder = finder
	}
}

func WithDefaultModel(model string) Option {
	return func(h *Handler) {
		if h.sampler == nil {
			h.sampler = &Sampler{}
		}
		h.sampler.defaultModel = strings.TrimSpace(model)
	}
}

func (h *Handler) canSample() bool {
	return h != nil && h.sampler != nil && h.sampler.modelFinder != nil && strings.TrimSpace(h.sampler.defaultModel) != ""
}

func (h *Handler) CreateMessage(ctx context.Context, request *jsonrpc.TypedRequest[*mcpschema.CreateMessageRequest]) (*mcpschema.CreateMessageResult, *jsonrpc.Error) {
	if !h.canSample() {
		return nil, jsonrpc.NewInternalError("sampling/createMessage model is not configured", nil)
	}
	if request == nil || request.Request == nil {
		return nil, jsonrpc.NewInvalidParamsError("missing sampling request", nil)
	}
	params := request.Request.Params
	messages := samplingMessagesToLLM(params.Messages)
	if len(messages) == 0 {
		return nil, jsonrpc.NewInvalidParamsError("sampling request has no text messages", nil)
	}
	modelID := h.sampler.selectModel(ctx, params.ModelPreferences)
	model, err := h.sampler.modelFinder.Find(ctx, modelID)
	if err != nil {
		return nil, jsonrpc.NewInternalError(fmt.Sprintf("find model %s: %v", modelID, err), nil)
	}
	options := &llm.Options{
		MaxTokens: params.MaxTokens,
		StopWords: append([]string(nil), params.StopSequences...),
	}
	if params.Temperature != nil {
		options.Temperature = *params.Temperature
	}
	generateRequest := &llm.GenerateRequest{
		Messages:     messages,
		Instructions: stringOrEmpty(params.SystemPrompt),
		Options:      options,
	}
	auditID := uuid.NewString()
	h.auditSamplingPayload(ctx, auditID, "model_request", modelID, generateRequest)
	response, err := model.Generate(ctx, generateRequest)
	if err != nil {
		h.auditSamplingPayload(ctx, auditID, "model_response", modelID, map[string]interface{}{
			"status": "failed",
			"error":  err.Error(),
		})
		return nil, jsonrpc.NewInternalError(fmt.Sprintf("sampling/createMessage generate: %v", err), nil)
	}
	h.auditSamplingPayload(ctx, auditID, "model_response", modelID, response)
	if response == nil || len(response.Choices) == 0 {
		return nil, jsonrpc.NewInternalError("sampling/createMessage returned no choices", nil)
	}
	choice := response.Choices[0]
	text := llm.MessageText(choice.Message)
	stopReason := strings.TrimSpace(choice.FinishReason)
	if stopReason == "" {
		stopReason = "endTurn"
	}
	resultModel := strings.TrimSpace(response.Model)
	if resultModel == "" {
		resultModel = modelID
	}
	return &mcpschema.CreateMessageResult{
		Role: mcpschema.RoleAssistant,
		Content: mcpschema.CreateMessageResultContent{
			Type: "text",
			Text: text,
		},
		Model:      resultModel,
		StopReason: &stopReason,
	}, nil
}

type samplingAuditPayload struct {
	TraceID   string      `json:"traceId"`
	Kind      string      `json:"kind"`
	Subtype   string      `json:"subtype"`
	ModelID   string      `json:"modelId,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
	Payload   interface{} `json:"payload"`
}

func (h *Handler) auditSamplingPayload(ctx context.Context, traceID, kind, modelID string, payload interface{}) string {
	if h == nil || h.conversations == nil || payload == nil {
		return ""
	}
	envelope := &samplingAuditPayload{
		TraceID:   strings.TrimSpace(traceID),
		Kind:      strings.TrimSpace(kind),
		Subtype:   samplingAuditSubtype,
		ModelID:   strings.TrimSpace(modelID),
		CreatedAt: time.Now(),
		Payload:   payload,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		logx.Warnf("mcp", "sampling audit marshal failed kind=%q model=%q err=%v", strings.TrimSpace(kind), strings.TrimSpace(modelID), err)
		return ""
	}
	record := apiconv.NewPayload()
	record.SetId(uuid.NewString())
	record.SetKind(strings.TrimSpace(kind))
	record.Subtype = stringPtr(samplingAuditSubtype)
	record.Has.Subtype = true
	record.SetMimeType("application/json")
	record.SetSizeBytes(len(data))
	record.SetStorage("inline")
	record.SetInlineBody(data)
	if err := h.conversations.PatchPayload(ctx, record); err != nil {
		logx.Warnf("mcp", "sampling audit persist failed kind=%q model=%q err=%v", strings.TrimSpace(kind), strings.TrimSpace(modelID), err)
		return ""
	}
	return record.Id
}

type preferenceSelector interface {
	Best(*llm.ModelPreferences) string
}

func (s *Sampler) selectModel(ctx context.Context, preferences *mcpschema.ModelPreferences) string {
	if s == nil {
		return ""
	}
	if preferences != nil {
		for _, candidate := range samplingPreferenceCandidates(preferences) {
			if candidate == "" {
				continue
			}
			if _, err := s.modelFinder.Find(ctx, candidate); err == nil {
				return candidate
			}
		}
		if selector, ok := s.modelFinder.(preferenceSelector); ok {
			selected := strings.TrimSpace(selector.Best(llm.NewModelPreferences(llm.WithPreferences(preferences))))
			if selected != "" {
				return selected
			}
		}
	}
	return strings.TrimSpace(s.defaultModel)
}

func samplingPreferenceCandidates(preferences *mcpschema.ModelPreferences) []string {
	if preferences == nil || len(preferences.Hints) == 0 {
		return nil
	}
	var result []string
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		result = append(result, value)
	}
	for _, hint := range preferences.Hints {
		if hint.Name == nil {
			continue
		}
		name := strings.TrimSpace(*hint.Name)
		add(name)
		if !strings.Contains(name, "_") {
			add("openai_" + name)
		}
	}
	return result
}

func samplingMessagesToLLM(messages []mcpschema.SamplingMessage) []llm.Message {
	if len(messages) == 0 {
		return nil
	}
	result := make([]llm.Message, 0, len(messages))
	for _, message := range messages {
		text := samplingContentText(message.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		role := llm.RoleUser
		if message.Role == mcpschema.RoleAssistant {
			role = llm.RoleAssistant
		}
		result = append(result, llm.NewTextMessage(role, text))
	}
	return result
}

func samplingContentText(content mcpschema.SamplingMessageContent) string {
	if text := strings.TrimSpace(content.Text); text != "" {
		return text
	}
	if strings.TrimSpace(content.Type) == "text" {
		return ""
	}
	data, err := json.Marshal(content)
	if err != nil || string(data) == "{}" || string(data) == "null" {
		return ""
	}
	return string(data)
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringPtr(value string) *string {
	return &value
}
