package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/viant/afs/option/content"
	"github.com/viant/afs/storage"
	"github.com/viant/afsc/openai/assets"
	"github.com/viant/agently-core/internal/shared"

	pdf "github.com/ledongthuc/pdf"
	openai "github.com/openai/openai-go/v3"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
)

var modelTemperature = map[string]float64{
	"o4-mini": 1.0,
	"o1-mini": 1.0,
	"o3-mini": 1.0,
	"o3":      1.0,
}

const defaultToolResultPreviewLimit = 8 * 1024

func supportsExplicitTemperature(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gpt-5", "gpt-5-mini", "gpt-5.2", "gpt-5.4":
		return false
	}
	return true
}

func toolResultPreviewLimit(request *llm.GenerateRequest) int {
	if request != nil && request.Options != nil && request.Options.Metadata != nil {
		switch actual := request.Options.Metadata["toolResultPreviewLimit"].(type) {
		case int:
			if actual > 0 {
				return actual
			}
		case int64:
			if actual > 0 {
				return int(actual)
			}
		case float64:
			if actual > 0 {
				return int(actual)
			}
		case string:
			actual = strings.TrimSpace(actual)
			if actual != "" {
				if parsed, err := json.Number(actual).Int64(); err == nil && parsed > 0 {
					return int(parsed)
				}
			}
		}
	}
	return defaultToolResultPreviewLimit
}

func sanitizeToolReplayMessage(msg llm.Message, threshold int) llm.Message {
	if threshold <= 0 {
		return msg
	}
	if msg.Role == llm.RoleTool {
		refID := strings.TrimSpace(msg.ID)
		if refID == "" {
			refID = strings.TrimSpace(msg.ToolCallId)
		}
		if body := strings.TrimSpace(msg.Content); body != "" {
			msg.Content = buildToolResultPreview(body, refID, threshold)
		}
		if len(msg.Items) > 0 {
			items := make([]llm.ContentItem, len(msg.Items))
			copy(items, msg.Items)
			for i := range items {
				if items[i].Type != llm.ContentTypeText {
					continue
				}
				text := strings.TrimSpace(items[i].Data)
				if text == "" {
					text = strings.TrimSpace(items[i].Text)
				}
				if text == "" {
					continue
				}
				preview := buildToolResultPreview(text, refID, threshold)
				items[i].Data = preview
				items[i].Text = preview
				break
			}
			msg.Items = items
		}
	}
	if len(msg.ToolCalls) > 0 {
		calls := make([]llm.ToolCall, len(msg.ToolCalls))
		copy(calls, msg.ToolCalls)
		for i := range calls {
			if body := strings.TrimSpace(calls[i].Result); body != "" {
				refID := strings.TrimSpace(calls[i].ResultMessageID)
				if refID == "" {
					refID = strings.TrimSpace(calls[i].ID)
				}
				calls[i].Result = buildToolResultPreview(body, refID, threshold)
			}
		}
		msg.ToolCalls = calls
	}
	return msg
}

func buildToolResultPreview(body, ref string, threshold int) string {
	body = strings.TrimSpace(body)
	if threshold <= 0 || len(body) <= threshold {
		return body
	}
	if jsonPreview, ok := truncateContinuationJSON(body, threshold); ok {
		return jsonPreview
	}
	limit := int(0.9 * float64(threshold))
	if limit <= 0 || limit > len(body) {
		limit = threshold
	}
	if limit > len(body) {
		limit = len(body)
	}
	returned := limit
	remaining := len(body) - returned
	chunk := middleTruncate(body, returned)
	chunk += fmt.Sprintf("[... omitted from %d to %d]", returned, len(body))
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return chunk
	}
	return fmt.Sprintf(`overflow: true
messageId: %s
nextRange:
  bytes:
    offset: %d
    length: %d
hasMore: true
remaining: %d
returned: %d
useToolToSeeMore: message-show
content: |
%s`, ref, returned, returned, remaining, returned, chunk)
}

func middleTruncate(body string, limit int) string {
	body = strings.TrimSpace(body)
	if limit <= 0 || len(body) <= limit {
		return body
	}
	head := limit / 2
	tail := limit - head
	if head <= 0 {
		head = limit
		tail = 0
	}
	start := strings.TrimSpace(body[:head])
	if tail <= 0 || tail >= len(body) {
		return start
	}
	end := strings.TrimSpace(body[len(body)-tail:])
	if end == "" {
		return start
	}
	return start + "\n[... omitted middle ...]\n" + end
}

func truncateContinuationJSON(body string, threshold int) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(body), "{") {
		return "", false
	}
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(body), &root); err != nil || root == nil {
		return "", false
	}
	cont, _ := root["continuation"].(map[string]interface{})
	if cont == nil {
		return "", false
	}
	hasMore, _ := cont["hasMore"].(bool)
	if !hasMore && intFromAny(cont["remaining"]) <= 0 {
		return "", false
	}
	if _, ok := cont["nextRange"].(map[string]interface{}); !ok {
		return "", false
	}
	parent, key, idx, value := findLargestString(root)
	if parent == nil || value == "" {
		out, err := json.Marshal(root)
		return string(out), err == nil
	}
	limit := int(0.9 * float64(threshold))
	if limit <= 0 || limit >= len(value) {
		limit = threshold
	}
	if limit <= 0 || limit >= len(value) {
		out, err := json.Marshal(root)
		return string(out), err == nil
	}
	truncated := middleTruncate(value, limit)
	switch container := parent.(type) {
	case map[string]interface{}:
		container[key] = truncated
	case []interface{}:
		if idx >= 0 && idx < len(container) {
			container[idx] = truncated
		}
	default:
		return "", false
	}
	out, err := json.Marshal(root)
	return string(out), err == nil
}

func findLargestString(v interface{}) (parent interface{}, key string, idx int, val string) {
	idx = -1
	var visit func(node interface{}, p interface{}, k string, i int)
	visit = func(node interface{}, p interface{}, k string, i int) {
		switch n := node.(type) {
		case string:
			if len(n) > len(val) {
				parent, key, idx, val = p, k, i, n
			}
		case map[string]interface{}:
			for mk, mv := range n {
				visit(mv, n, mk, -1)
			}
		case []interface{}:
			for si, sv := range n {
				visit(sv, n, "", si)
			}
		}
	}
	visit(v, nil, "", -1)
	return parent, key, idx, val
}

func intFromAny(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i)
		}
	}
	return 0
}

// ToRequest converts an llm.ChatRequest to a Request
func (c *Client) ToRequest(request *llm.GenerateRequest) (*Request, error) {
	// Create the request with defaults
	req := &Request{}

	// Set options if provided
	if request.Options != nil {
		// Set model if provided
		if request.Options.Model != "" {
			req.Model = request.Options.Model
		}
		if req.Model == "" {
			req.Model = c.Model
		}

		// Set max tokens if provided
		if request.Options.MaxTokens > 0 {
			req.MaxTokens = request.Options.MaxTokens
		}

		// Set top_p if provided
		if request.Options.TopP > 0 {
			req.TopP = request.Options.TopP
		}

		// Some GPT-5 reasoning models reject explicit temperature entirely.
		if request.Options.Temperature > 0 && supportsExplicitTemperature(req.Model) {
			req.Temperature = &request.Options.Temperature
		}

		// Set n if provided
		if request.Options.N > 0 {
			req.N = request.Options.N
		}
		// Enable streaming if requested
		req.Stream = request.Options.Stream
		// Propagate reasoning summary if requested on supported models
		if r := request.Options.Reasoning; r != nil {
			switch req.Model {
			case "o3", "o4-mini", "codex-mini-latest",
				"gpt-4.1", "gpt-4.1-mini", "gpt-5", "o3-mini":
				req.Reasoning = r
			}
		}

		// Convert tools if provided
		if len(request.Options.Tools) > 0 {
			req.Tools = make([]Tool, len(request.Options.Tools))
			for i, tool := range request.Options.Tools {
				def := tool.Definition
				def.Normalize() // ensure provider-agnostic, valid JSON schema shapes
				req.Tools[i] = Tool{
					Type: "function",
					Function: ToolDefinition{
						Name:        mcpname.Canonical(def.Name),
						Description: def.Description,
						Parameters:  def.Parameters,
						Required:    def.Required,
						Strict:      def.Strict,
					},
				}
			}
		}

		// Honor parallel tool calls only when tools are present.
		// OpenAI chat.completions rejects parallel_tool_calls without tools.
		if request.Options.ParallelToolCalls && len(req.Tools) > 0 {
			req.ParallelToolCalls = true
		}

		// Convert tool choice if provided and tools are present
		if len(request.Options.Tools) > 0 && request.Options.ToolChoice.Type != "" {
			switch request.Options.ToolChoice.Type {
			case "auto":
				req.ToolChoice = "auto"
			case "none":
				req.ToolChoice = "none"
			case "function":
				if request.Options.ToolChoice.Function != nil {
					req.ToolChoice = map[string]interface{}{
						"type": "function",
						"function": map[string]string{
							"name": request.Options.ToolChoice.Function.Name,
						},
					}
				}
			}
		}
	}
	if req.Model == "" {
		req.Model = c.Model
	}
	if req.ToolChoice == nil && len(req.Tools) > 0 {
		req.ToolChoice = "auto"
	}
	if len(req.Tools) == 0 {
		if m, ok := req.ToolChoice.(map[string]interface{}); ok {
			if strings.EqualFold(strings.TrimSpace(fmt.Sprint(m["type"])), "allowed_tools") ||
				strings.EqualFold(strings.TrimSpace(fmt.Sprint(m["type"])), "code_interpreter") {
				// Keep explicit built-in tool choices even before later tool injection.
			} else {
				req.ToolChoice = nil
			}
		} else {
			req.ToolChoice = nil
		}
	}

	// Attachment preferences and limits
	attachMode := "upload" // prefer upload for tool-result PDFs
	if request != nil && request.Options != nil && request.Options.Metadata != nil {
		if v, ok := request.Options.Metadata["attachMode"].(string); ok && strings.TrimSpace(v) != "" {
			attachMode = strings.ToLower(strings.TrimSpace(v))
		}
		if v, ok := request.Options.Metadata["forceCodeInterpreter"].(bool); ok && v {
			req.EnableCodeInterpreter = true
			req.ToolChoice = map[string]interface{}{"type": "code_interpreter"}
		}
		if v, ok := request.Options.Metadata["modelArtifactGeneration"].(bool); ok && v {
			req.EnableCodeInterpreter = true
			req.ToolChoice = map[string]interface{}{"type": "code_interpreter"}
		}
		if v, ok := request.Options.Metadata["forceImageGeneration"].(bool); ok && v {
			req.ToolChoice = nil
			req.EnableImageGeneration = true
		}
	}
	ToolCallIdToReplaceContent := map[string]struct{}{}

	// Continue previous Responses API call when requested
	if request != nil && strings.TrimSpace(request.PreviousResponseID) != "" {
		req.PreviousResponseID = strings.TrimSpace(request.PreviousResponseID)
	}
	if request != nil && strings.TrimSpace(request.Instructions) != "" {
		req.Instructions = strings.TrimSpace(request.Instructions)
	}
	if request != nil && strings.TrimSpace(request.PromptCacheKey) != "" {
		req.PromptCacheKey = strings.TrimSpace(request.PromptCacheKey)
	}
	if request != nil && request.Options != nil {
		verbosity := strings.TrimSpace(request.Options.ResponseVerbosity)
		schema := request.Options.OutputSchema
		if verbosity != "" || schema != nil {
			tc := &TextControls{}
			if verbosity != "" {
				tc.Verbosity = verbosity
			}
			if schema != nil {
				tc.Format = &TextFormat{
					Type:   "json_schema",
					Strict: true,
					Schema: schema,
					Name:   "codex_output_schema",
				}
			}
			req.Text = tc
		}
	}

	// Convert messages
	previewLimit := toolResultPreviewLimit(request)
	req.Messages = make([]Message, 0) //len(request.Messages))
	for _, originalMsg := range request.Messages {
		// Work on a local copy so we can transform tool messages if needed.
		msg := sanitizeToolReplayMessage(originalMsg, previewLimit)
		message := Message{
			Role: string(msg.Role),
		}
		isAssistant := msg.Role == llm.RoleAssistant
		// Propagate speaker name only for user/assistant roles
		if msg.Role == llm.RoleUser || msg.Role == llm.RoleAssistant {
			message.Name = msg.Name
		}

		// Optionally convert large tool results into PDF attachments
		if msg.Role == llm.RoleTool {
			// no-op placeholder for future tool-result attachment handling
		}

		// Handle content based on priority: Items > ContentItems > Result
		if len(msg.Items) > 0 {
			// Convert Items to OpenAI format
			contentItems := make([]ContentItem, len(msg.Items))
			for j, item := range msg.Items {
				contentItem := ContentItem{
					Type: string(item.Type),
				}

				// Handle different content types
				switch item.Type {
				case llm.ContentTypeText:
					// Use Data field first, fall back to Text field
					if item.Data != "" {
						contentItem.Text = item.Data
					} else {
						contentItem.Text = item.Text
					}
				case llm.ContentTypeImage, llm.ContentTypeImageURL:
					// OpenAI expects "image_url" as the type
					contentItem.Type = "image_url"

					// Preferred approach: Use Source=SourceURL and Data field
					if item.Source == llm.SourceURL && item.Data != "" {
						contentItem.ImageURL = &ImageURL{
							URL: item.Data,
						}

						// Add detail if available in metadata
						if item.Metadata != nil {
							if detail, ok := item.Metadata["detail"].(string); ok {
								contentItem.ImageURL.Detail = detail
							}
						}
					}
				case llm.ContentTypeBinary:
					if attachMode == "inline" {
						if strings.HasPrefix(item.MimeType, "image/") && item.Data != "" {
							// For images, inline as data URL to support vision (OpenAI expects image_url)
							contentItem.Type = "image_url"
							dataURL := "data:" + item.MimeType + ";base64," + item.Data
							contentItem.ImageURL = &ImageURL{URL: dataURL}
						} else if strings.EqualFold(item.MimeType, "application/pdf") && item.Data != "" {
							text, err := extractPDFContentItemText(item.Data, item.Name)
							if err != nil {
								return nil, fmt.Errorf("failed to extract PDF content item: %w", err)
							}
							if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_PDF_UPLOAD")) == "1" {
								preview := text
								if len(preview) > 120 {
									preview = preview[:120]
								}
								_, _ = fmt.Fprintf(os.Stderr, "[pdf-inline-convert] type=%q preview=%q\n", contentItem.Type, preview)
							}
							contentItem.Type = "input_text"
							if isAssistant {
								contentItem.Type = "output_text"
							}
							contentItem.Text = text
						} else {
							return nil, fmt.Errorf("unsupported inline binary content item mime type: %q", item.MimeType)
						}
					} else {
						if strings.HasPrefix(item.MimeType, "image/") && item.Data != "" {
							// For images, inline as data URL to support vision (OpenAI expects image_url)
							contentItem.Type = "image_url"
							dataURL := "data:" + item.MimeType + ";base64," + item.Data
							contentItem.ImageURL = &ImageURL{URL: dataURL}
						} else if strings.EqualFold(item.MimeType, "application/pdf") {
							text, err := extractPDFContentItemText(item.Data, item.Name)
							if err != nil {
								return nil, fmt.Errorf("failed to extract PDF content item: %w", err)
							}
							if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_PDF_UPLOAD")) == "1" {
								preview := text
								if len(preview) > 120 {
									preview = preview[:120]
								}
								_, _ = fmt.Fprintf(os.Stderr, "[pdf-ref-convert] type=%q preview=%q\n", contentItem.Type, preview)
							}
							contentItem.Type = "input_text"
							if isAssistant {
								contentItem.Type = "output_text"
							}
							contentItem.Text = text
						} else {
							return nil, fmt.Errorf("unsupported uploaded binary content item mime type: %q", item.MimeType)
						}
					}
				}
				contentItems[j] = contentItem
			}
			if _, ok := ToolCallIdToReplaceContent[msg.ToolCallId]; !ok {
				message.Content = contentItems
			} else {
				message.Content = msg.Content
			}
		} else if len(msg.ContentItems) > 0 {
			// Legacy: Convert ContentItems to OpenAI format
			contentItems := make([]ContentItem, len(msg.ContentItems))
			for j, item := range msg.ContentItems {
				contentItem := ContentItem{
					Type: string(item.Type),
				}

				if item.Type == llm.ContentTypeText {
					// Use Data field first, fall back to Text field
					if item.Data != "" {
						contentItem.Text = item.Data
					} else {
						contentItem.Text = item.Text
					}
				} else if item.Type == llm.ContentTypeImage || item.Type == llm.ContentTypeImageURL {
					// OpenAI expects "image_url" as the type
					contentItem.Type = "image_url"

					// Preferred approach: Use Source=SourceURL and Data field
					if item.Source == llm.SourceURL && item.Data != "" {
						contentItem.ImageURL = &ImageURL{
							URL: item.Data,
						}

						// Add detail if available in metadata
						if item.Metadata != nil {
							if detail, ok := item.Metadata["detail"].(string); ok {
								contentItem.ImageURL.Detail = detail
							}
						}
					}
				}

				contentItems[j] = contentItem
			}
			message.Content = contentItems
		} else if msg.Content != "" {
			// Use simple string content for backward compatibility
			message.Content = msg.Content
		}

		// Convert function call if present
		if msg.FunctionCall != nil {
			message.FunctionCall = &FunctionCall{
				Name:      msg.FunctionCall.Name,
				Arguments: msg.FunctionCall.Arguments,
			}
		}

		message.ToolCallId = msg.ToolCallId
		// Convert tool calls if present
		if len(msg.ToolCalls) > 0 {
			message.ToolCalls = make([]ToolCall, len(msg.ToolCalls))
			for j, toolCall := range msg.ToolCalls {
				message.ToolCalls[j] = ToolCall{
					ID:   toolCall.ID,
					Type: "function",

					Function: FunctionCall{
						Name:      toolCall.Name,
						Arguments: toolCall.Function.Arguments,
					},
				}
			}
		}

		req.Messages = append(req.Messages, message)
		//req.Messages[i] = message
	}

	return req, nil
}

func extractPDFContentItemText(base64Data string, name string) (string, error) {
	raw := strings.TrimSpace(base64Data)
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || !bytes.HasPrefix(data, []byte("%PDF-")) {
		data = []byte(raw)
	}
	if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_PDF_UPLOAD")) == "1" {
		decodedPrefix := ""
		if len(data) > 0 {
			end := len(data)
			if end > 16 {
				end = 16
			}
			decodedPrefix = fmt.Sprintf("%q", string(data[:end]))
		}
		_, _ = fmt.Fprintf(os.Stderr, "[pdf-extract] name=%q raw_len=%d decoded_len=%d prefix=%s\n", name, len(raw), len(data), decodedPrefix)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", fmt.Errorf("not a PDF file: invalid header")
	}
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	plain, err := reader.GetPlainText()
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(plain)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "", fmt.Errorf("pdf text content was empty")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "attachment.pdf"
	}
	return fmt.Sprintf("PDF attachment %s:\n%s", name, text), nil
}

// ToRequest is a convenience wrapper retained for backward-compatible tests.
// It constructs a default client and adapts an llm.GenerateRequest to provider Request.
// Errors are ignored in this wrapper; callers requiring error handling should use Client.ToRequest.
func ToRequest(request *llm.GenerateRequest) *Request {
	c := &Client{}
	out, _ := c.ToRequest(request)
	return out
}

// uploadFiledAndGetID uploads a base64-encoded PDF to OpenAI assets and returns its file_id.
func (c *Client) uploadFiledAndGetID(ctx context.Context, base64Data string, name string, agentID string, ttlSec int64) (string, error) {
	var attachmentTTLSec int64 = ttlSec
	// Apply provider default TTL (86400 sec = 1 day) when not specified
	if attachmentTTLSec <= 0 {
		attachmentTTLSec = 86400
	}

	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", err

	}

	user := ""
	if ui := authctx.User(ctx); ui != nil {
		user = strings.TrimSpace(ui.Subject)
		if user == "" {
			user = strings.TrimSpace(ui.Email)
		}
	}

	if user == "" {
		user, err = shared.PrefixHostIP()
	}
	if err != nil {
		return "", fmt.Errorf("failed to determine host ip prefix: %w", err)
	}

	baseName := path.Base(strings.TrimSpace(name))
	if baseName == "" || baseName == "." || baseName == "/" || !strings.HasSuffix(strings.ToLower(baseName), ".pdf") {
		baseName = "attachment.pdf"
	}
	sanitize := strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_")
	filename := fmt.Sprintf("agently_%s_%s_%s_%s",
		sanitize.Replace(strings.TrimSpace(user)),
		sanitize.Replace(strings.TrimSpace(agentID)),
		sanitize.Replace(strings.TrimSpace(c.Model)),
		sanitize.Replace(baseName),
	)
	dest := "openai://assets/" + filename
	if err := c.ensureStorageManager(ctx); err != nil {
		return "", err
	}
	// Build options with optional TTL
	var opts []storage.Option
	opts = append(opts, &content.Meta{Values: map[string]string{"purpose": string(openai.FilePurposeUserData)}})
	// Always include TTL (provider default baked above)
	opts = append(opts, &openai.FileNewParamsExpiresAfter{Seconds: attachmentTTLSec})

	if err := c.storageMgr.Upload(ctx, dest, 0644, bytes.NewReader(data), opts...); err != nil {
		return "", err
	}

	// Find created file id by listing (with small retries)
	var fileID string
	for attempt := 0; attempt < 2 && fileID == ""; attempt++ {
		files, err := c.storageMgr.List(ctx, "openai://assets/")
		if err != nil {
			return "", err
		}
		for _, f := range files {
			if f.Name() == filename {
				if af, ok := f.Sys().(assets.File); ok {
					fileID = af.ID
					break
				}
			}
		}
		if fileID == "" {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if fileID == "" {
		return "", fmt.Errorf("uploaded file id not found")
	}
	return fileID, nil
}

// ToLLMSResponse converts a Response to an llm.ChatResponse
func ToLLMSResponse(resp *Response) *llm.GenerateResponse {
	// Create the LLMS response
	llmsResp := &llm.GenerateResponse{
		Choices: make([]llm.Choice, len(resp.Choices)),
	}
	// Preserve provider response identifiers for trace grouping/continuation.
	llmsResp.Model = resp.Model
	llmsResp.ResponseID = resp.ID

	// Convert choices
	for i, choice := range resp.Choices {
		llmsChoice := llm.Choice{
			Index:        choice.Index,
			FinishReason: choice.FinishReason,
		}

		// Create the message with basic fields
		message := llm.Message{
			Role: llm.MessageRole(choice.Message.Role),
			Name: choice.Message.Name,
		}

		// Handle content based on its type
		switch content := choice.Message.Content.(type) {
		case string:
			// Simple string content
			message.Content = content
		case []ContentItem:
			// Convert content items to internal format
			message.ContentItems = make([]llm.ContentItem, len(content))
			for j, item := range content {
				contentItem := llm.ContentItem{
					Type: llm.ContentType(item.Type),
				}

				if item.Type == "text" {
					contentItem.Text = item.Text
					contentItem.Source = llm.SourceRaw
					contentItem.Data = item.Text
				} else if item.Type == "image_url" && item.ImageURL != nil {
					// Set the proper content type
					contentItem.Type = llm.ContentTypeImage

					// Use the preferred approach: Source=SourceURL and Data field
					contentItem.Source = llm.SourceURL
					contentItem.Data = item.ImageURL.URL

					// Add detail to metadata if present
					if item.ImageURL.Detail != "" {
						if contentItem.Metadata == nil {
							contentItem.Metadata = make(map[string]interface{})
						}
						contentItem.Metadata["detail"] = item.ImageURL.Detail
					}

				}

				message.ContentItems[j] = contentItem
			}
		case []interface{}:
			// Handle case where content is a generic slice
			message.ContentItems = make([]llm.ContentItem, 0, len(content))
			for _, item := range content {
				if itemMap, ok := item.(map[string]interface{}); ok {
					contentType, _ := itemMap["type"].(string)
					contentItem := llm.ContentItem{
						Type: llm.ContentType(contentType),
					}

					if contentType == "text" {
						text, _ := itemMap["text"].(string)
						contentItem.Text = text
						contentItem.Source = llm.SourceRaw
						contentItem.Data = text
					} else if contentType == "image_url" {
						if imageURL, ok := itemMap["image_url"].(map[string]interface{}); ok {
							url, _ := imageURL["url"].(string)
							detail, _ := imageURL["detail"].(string)

							// Set the proper content type
							contentItem.Type = llm.ContentTypeImage

							// Use the preferred approach: Source=SourceURL and Data field
							contentItem.Source = llm.SourceURL
							contentItem.Data = url

							// Add detail to metadata if present
							if detail != "" {
								if contentItem.Metadata == nil {
									contentItem.Metadata = make(map[string]interface{})
								}
								contentItem.Metadata["detail"] = detail
							}
						}
					}

					message.ContentItems = append(message.ContentItems, contentItem)
				}
			}
		}

		// Convert function call if present
		if choice.Message.FunctionCall != nil {
			message.FunctionCall = &llm.FunctionCall{
				Name:      choice.Message.FunctionCall.Name,
				Arguments: choice.Message.FunctionCall.Arguments,
			}
		}

		// Convert tool calls if present
		if len(choice.Message.ToolCalls) > 0 {
			message.ToolCalls = make([]llm.ToolCall, len(choice.Message.ToolCalls))
			for j, toolCall := range choice.Message.ToolCalls {
				message.ToolCalls[j] = llm.ToolCall{
					ID:   toolCall.ID,
					Type: toolCall.Type,
					Function: llm.FunctionCall{
						Name:      toolCall.Function.Name,
						Arguments: toolCall.Function.Arguments,
					},
				}
			}
		}
		// Preserve tool call result ID if present
		message.ToolCallId = choice.Message.ToolCallId

		llmsChoice.Message = message
		llmsResp.Choices[i] = llmsChoice
	}

	// Convert usage including detailed fields when available
	u := &llm.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	// Map prompt details (prefer details; fallback to flattened field)
	u.PromptCachedTokens = resp.Usage.PromptTokensDetails.CachedTokens
	if u.PromptCachedTokens == 0 && resp.Usage.PromptCachedTokens > 0 {
		u.PromptCachedTokens = resp.Usage.PromptCachedTokens
	}
	if resp.Usage.PromptTokensDetails.AudioTokens > 0 {
		u.PromptAudioTokens = resp.Usage.PromptTokensDetails.AudioTokens
	}
	// Map completion details
	if resp.Usage.CompletionTokensDetails.ReasoningTokens > 0 {
		u.ReasoningTokens = resp.Usage.CompletionTokensDetails.ReasoningTokens
		u.CompletionReasoningTokens = resp.Usage.CompletionTokensDetails.ReasoningTokens
	} else {
		// Fallback to flattened fields when details are absent
		if resp.Usage.CompletionReasoningTokens > 0 {
			u.CompletionReasoningTokens = resp.Usage.CompletionReasoningTokens
			if u.ReasoningTokens == 0 {
				u.ReasoningTokens = resp.Usage.CompletionReasoningTokens
			}
		} else if resp.Usage.ReasoningTokens > 0 && u.ReasoningTokens == 0 {
			u.ReasoningTokens = resp.Usage.ReasoningTokens
		}
	}
	if resp.Usage.CompletionTokensDetails.AudioTokens > 0 {
		u.CompletionAudioTokens = resp.Usage.CompletionTokensDetails.AudioTokens
		// Keep legacy aggregate when single source available
		if u.AudioTokens == 0 {
			u.AudioTokens = resp.Usage.CompletionTokensDetails.AudioTokens
		}
	}
	u.AcceptedPredictionTokens = resp.Usage.CompletionTokensDetails.AcceptedPredictionTokens
	u.RejectedPredictionTokens = resp.Usage.CompletionTokensDetails.RejectedPredictionTokens
	llmsResp.Usage = u

	return llmsResp
}
