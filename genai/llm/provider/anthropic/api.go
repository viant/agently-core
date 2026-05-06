package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/base"
	vclaude "github.com/viant/agently-core/genai/llm/provider/vertexai/claude"
	mcbuf "github.com/viant/agently-core/service/core/modelcall"
)

func (c *Client) Implements(feature string) bool {
	switch feature {
	case base.CanUseTools:
		return true
	case base.CanStream:
		return true
	case base.IsMultimodal:
		return true
	case base.SupportsInstructions:
		return true
	}
	return false
}

func (c *Client) Generate(ctx context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if c.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	req, err := ToRequest(ctx, c.Model, request)
	if err != nil {
		return nil, err
	}
	if req.MaxTokens == 0 && c.MaxTokens > 0 {
		req.MaxTokens = c.MaxTokens
	}
	if req.Temperature == 0 && c.Temperature != nil {
		req.Temperature = *c.Temperature
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	observer := mcbuf.ObserverFromContext(ctx)
	if observer != nil {
		var genReqJSON []byte
		if request != nil {
			genReqJSON, _ = json.Marshal(request)
		}
		if newCtx, obErr := observer.OnCallStart(ctx, mcbuf.Info{Provider: "anthropic", Model: c.Model, ModelKind: "chat", LLMRequest: request, RequestJSON: data, Payload: genReqJSON, StartedAt: time.Now()}); obErr == nil {
			ctx = newCtx
		} else {
			return nil, fmt.Errorf("observer OnCallStart failed: %w", obErr)
		}
	}

	resp, err := c.sendMessagesRequest(ctx, data)
	if err != nil {
		if observer != nil {
			if obErr := observer.OnCallEnd(ctx, mcbuf.Info{Provider: "anthropic", Model: c.Model, ModelKind: "chat", CompletedAt: time.Now(), Err: err.Error()}); obErr != nil {
				return nil, fmt.Errorf("failed to send request: %w (observer OnCallEnd failed: %v)", err, obErr)
			}
		}
		return nil, err
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if observer != nil {
			if obErr := observer.OnCallEnd(ctx, mcbuf.Info{Provider: "anthropic", Model: c.Model, ModelKind: "chat", ResponseJSON: respBytes, CompletedAt: time.Now(), Err: fmt.Sprintf("status %d", resp.StatusCode)}); obErr != nil {
				return nil, fmt.Errorf("anthropic API error (status %d): %s (observer OnCallEnd failed: %v)", resp.StatusCode, string(respBytes), obErr)
			}
		}
		return nil, fmt.Errorf("anthropic API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var apiResp Response
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	llmsResp := vclaude.ToLLMSResponse(&apiResp)
	var usage *llm.Usage
	if llmsResp != nil {
		usage = llmsResp.Usage
	}
	if c.UsageListener != nil && usage != nil && usage.TotalTokens > 0 {
		c.UsageListener.OnUsage(c.Model, usage)
	}
	if observer != nil {
		info := mcbuf.Info{Provider: "anthropic", Model: c.Model, ModelKind: "chat", ResponseJSON: respBytes, CompletedAt: time.Now(), Usage: usage, LLMResponse: llmsResp}
		if llmsResp != nil && len(llmsResp.Choices) > 0 {
			info.FinishReason = llmsResp.Choices[0].FinishReason
		}
		if obErr := observer.OnCallEnd(ctx, info); obErr != nil {
			return nil, fmt.Errorf("observer OnCallEnd failed: %w", obErr)
		}
	}
	return llmsResp, nil
}

func (c *Client) Stream(ctx context.Context, request *llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	if c.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	req, err := ToRequest(ctx, c.Model, request)
	if err != nil {
		return nil, err
	}
	req.Stream = true
	if req.MaxTokens == 0 {
		if c.MaxTokens > 0 {
			req.MaxTokens = c.MaxTokens
		} else {
			req.MaxTokens = 8192
		}
	}
	if req.Temperature == 0 && c.Temperature != nil {
		req.Temperature = *c.Temperature
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	observer := mcbuf.ObserverFromContext(ctx)
	if observer != nil {
		var genReqJSON []byte
		if request != nil {
			genReqJSON, _ = json.Marshal(request)
		}
		if newCtx, obErr := observer.OnCallStart(ctx, mcbuf.Info{Provider: "anthropic", Model: c.Model, ModelKind: "chat", LLMRequest: request, RequestJSON: data, Payload: genReqJSON, StartedAt: time.Now()}); obErr == nil {
			ctx = newCtx
		} else {
			return nil, fmt.Errorf("observer OnCallStart failed: %w", obErr)
		}
	}
	resp, err := c.sendMessagesRequest(ctx, data)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if observer != nil {
			if obErr := observer.OnCallEnd(ctx, mcbuf.Info{Provider: "anthropic", Model: c.Model, ModelKind: "chat", ResponseJSON: body, CompletedAt: time.Now(), Err: fmt.Sprintf("status %d", resp.StatusCode)}); obErr != nil {
				return nil, fmt.Errorf("anthropic API error (status %d): %s (observer OnCallEnd failed: %v)", resp.StatusCode, string(body), obErr)
			}
		}
		return nil, fmt.Errorf("anthropic API error (status %d): %s", resp.StatusCode, string(body))
	}

	events := make(chan llm.StreamEvent)
	go func() {
		defer resp.Body.Close()
		defer close(events)

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
		type toolAgg struct {
			id, name  string
			json      string
			completed bool
			emitted   bool
		}
		aggText := strings.Builder{}
		tools := map[int]*toolAgg{}
		finishReason := ""
		var promptTokens, completionTokens int
		var usage *llm.Usage
		var lastLR *llm.GenerateResponse
		ended := false

		emitToolsIfAny := func() {
			idxs := make([]int, 0, len(tools))
			for i, ta := range tools {
				if ta == nil || ta.emitted || !ta.completed {
					continue
				}
				idxs = append(idxs, i)
			}
			if len(idxs) == 0 {
				return
			}
			for i := 1; i < len(idxs); i++ {
				j := i
				for j > 0 && idxs[j-1] > idxs[j] {
					idxs[j-1], idxs[j] = idxs[j], idxs[j-1]
					j--
				}
			}
			for _, i := range idxs {
				ta := tools[i]
				var args map[string]interface{}
				if err := json.Unmarshal([]byte(ta.json), &args); err != nil {
					args = map[string]interface{}{"raw": ta.json}
				}
				events <- llm.StreamEvent{Kind: llm.StreamEventToolCallCompleted, ToolCallID: ta.id, ToolName: ta.name, Arguments: args}
				ta.emitted = true
			}
		}

		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "event:") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			var evt Response
			if err := json.Unmarshal([]byte(payload), &evt); err != nil {
				events <- llm.StreamEvent{Err: fmt.Errorf("failed to unmarshal stream part: %w", err)}
				return
			}
			switch evt.Type {
			case "ping":
				continue
			case "content_block_start":
				if evt.ContentBlock != nil && evt.ContentBlock.Type == "tool_use" {
					tools[evt.Index] = &toolAgg{id: evt.ContentBlock.ID, name: evt.ContentBlock.Name}
					events <- llm.StreamEvent{Kind: llm.StreamEventToolCallStarted, ToolCallID: evt.ContentBlock.ID, ToolName: evt.ContentBlock.Name}
				}
			case "content_block_delta":
				if evt.Delta != nil {
					if evt.Delta.Text != "" {
						aggText.WriteString(evt.Delta.Text)
						events <- llm.StreamEvent{Kind: llm.StreamEventTextDelta, Delta: evt.Delta.Text}
						if observer != nil {
							if obErr := observer.OnStreamDelta(ctx, []byte(evt.Delta.Text)); obErr != nil {
								events <- llm.StreamEvent{Err: fmt.Errorf("observer OnStreamDelta failed: %w", obErr)}
								return
							}
						}
					}
					if evt.Delta.PartialJSON != "" {
						if ta, ok := tools[evt.Index]; ok {
							ta.json += evt.Delta.PartialJSON
							events <- llm.StreamEvent{Kind: llm.StreamEventToolCallDelta, ToolCallID: ta.id, ToolName: ta.name, Delta: evt.Delta.PartialJSON}
						}
					}
				}
			case "content_block_stop":
				if ta, ok := tools[evt.Index]; ok && ta != nil {
					ta.completed = true
					emitToolsIfAny()
				}
			case "message_delta":
				if evt.Delta != nil && evt.Delta.StopReason != "" {
					finishReason = evt.Delta.StopReason
				}
				if evt.Usage != nil {
					completionTokens += evt.Usage.OutputTokens
				}
			case "message_stop":
				usage = &llm.Usage{PromptTokens: promptTokens, CompletionTokens: completionTokens, TotalTokens: promptTokens + completionTokens}
				if c.UsageListener != nil && usage.TotalTokens > 0 {
					c.UsageListener.OnUsage(c.Model, usage)
				}
				emitToolsIfAny()
				events <- llm.StreamEvent{Kind: llm.StreamEventUsage, Usage: usage}
				msg := llm.Message{Role: llm.RoleAssistant, Content: aggText.String()}
				if len(tools) > 0 {
					idxs := make([]int, 0, len(tools))
					for i := range tools {
						idxs = append(idxs, i)
					}
					for i := 1; i < len(idxs); i++ {
						j := i
						for j > 0 && idxs[j-1] > idxs[j] {
							idxs[j-1], idxs[j] = idxs[j], idxs[j-1]
							j--
						}
					}
					calls := make([]llm.ToolCall, 0, len(idxs))
					for _, i := range idxs {
						ta := tools[i]
						var args map[string]interface{}
						if err := json.Unmarshal([]byte(ta.json), &args); err != nil {
							args = map[string]interface{}{"raw": ta.json}
						}
						calls = append(calls, llm.ToolCall{ID: ta.id, Name: ta.name, Arguments: args})
					}
					msg.ToolCalls = calls
				}
				lr := &llm.GenerateResponse{Choices: []llm.Choice{{Index: 0, Message: msg, FinishReason: finishReason}}, Model: c.Model, Usage: usage}
				if observer != nil {
					respJSON, _ := json.Marshal(lr)
					if obErr := observer.OnCallEnd(ctx, mcbuf.Info{Provider: "anthropic", Model: c.Model, ModelKind: "chat", ResponseJSON: respJSON, CompletedAt: time.Now(), Usage: usage, FinishReason: finishReason, LLMResponse: lr}); obErr != nil {
						events <- llm.StreamEvent{Err: fmt.Errorf("observer OnCallEnd failed: %w", obErr)}
						return
					}
					ended = true
				}
				events <- llm.StreamEvent{Kind: llm.StreamEventTurnCompleted, FinishReason: finishReason, Usage: usage}
				lastLR = lr
			case "message_start":
				type msgStart struct {
					Message struct {
						Usage *Usage `json:"usage"`
					} `json:"message"`
				}
				var ms msgStart
				if err := json.Unmarshal([]byte(payload), &ms); err == nil && ms.Message.Usage != nil {
					promptTokens = ms.Message.Usage.InputTokens
					completionTokens += ms.Message.Usage.OutputTokens
				}
			case "error":
				if evt.Error != nil && evt.Error.Message != "" {
					events <- llm.StreamEvent{Err: fmt.Errorf("anthropic stream error: %s", evt.Error.Message)}
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			events <- llm.StreamEvent{Err: fmt.Errorf("stream read error: %w", err)}
		}
		if !ended && observer != nil {
			var respJSON []byte
			var finalReason string
			if lastLR != nil {
				respJSON, _ = json.Marshal(lastLR)
				if len(lastLR.Choices) > 0 {
					finalReason = lastLR.Choices[0].FinishReason
				}
			}
			if obErr := observer.OnCallEnd(ctx, mcbuf.Info{Provider: "anthropic", Model: c.Model, ModelKind: "chat", ResponseJSON: respJSON, CompletedAt: time.Now(), Usage: usage, FinishReason: finalReason, LLMResponse: lastLR}); obErr != nil {
				events <- llm.StreamEvent{Err: fmt.Errorf("observer OnCallEnd failed: %w", obErr)}
			}
		}
	}()
	return events, nil
}

func (c *Client) sendMessagesRequest(ctx context.Context, data []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", c.APIVersion)
	if token, err := c.authToken(ctx); err == nil && strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		req.Header.Set("anthropic-beta", c.OAuthBeta)
	} else {
		key, keyErr := c.apiKey(ctx)
		if keyErr != nil {
			if err != nil {
				return nil, fmt.Errorf("failed to resolve Anthropic auth token: %w (api key fallback also failed: %v)", err, keyErr)
			}
			return nil, keyErr
		}
		req.Header.Set("x-api-key", key)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	return resp, nil
}
