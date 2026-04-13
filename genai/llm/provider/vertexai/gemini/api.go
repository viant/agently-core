package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/viant/agently-core/genai/llm/provider/base"

	"time"

	"github.com/viant/agently-core/genai/llm"
	mcbuf "github.com/viant/agently-core/service/core/modelcall"
)

func (c *Client) Implements(feature string) bool {
	switch feature {
	case base.CanUseTools:
		return true
	case base.CanStream:
		return c.canStream()
	case base.IsMultimodal:
		return true
	case base.SupportsContextContinuation:
		return false
	case base.SupportsInstructions:
		return true
	}
	return false
}

func (c *Client) canStream() bool {
	m := strings.ToLower(c.Model)
	// Gemini embedding endpoints do not stream
	if strings.Contains(m, "embed") || strings.Contains(m, "embedding") {
		return false
	}
	return true
}

// Generate generates a response using the Gemini API
func (c *Client) Generate(ctx context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if c.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	if c.Model == "" {
		return nil, fmt.Errorf("model is required")
	}

	// Convert llms.ChatRequest to Request
	req, err := ToRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	// client defaults
	if req.GenerationConfig != nil {
		if req.GenerationConfig.MaxOutputTokens == 0 && c.MaxTokens > 0 {
			req.GenerationConfig.MaxOutputTokens = c.MaxTokens
		}
		if req.GenerationConfig.Temperature == 0 && c.Temperature != nil {
			req.GenerationConfig.Temperature = *c.Temperature
		}
	} else {
		gc := &GenerationConfig{}
		if c.MaxTokens > 0 {
			gc.MaxOutputTokens = c.MaxTokens
		}
		if c.Temperature != nil {
			gc.Temperature = *c.Temperature
		}
		if gc.MaxOutputTokens > 0 || gc.Temperature != 0 {
			req.GenerationConfig = gc
		}
	}

	// apply client defaults
	if req.GenerationConfig != nil {
		if req.GenerationConfig.MaxOutputTokens == 0 && c.MaxTokens > 0 {
			req.GenerationConfig.MaxOutputTokens = c.MaxTokens
		}
		if req.GenerationConfig.Temperature == 0 && c.Temperature != nil {
			req.GenerationConfig.Temperature = *c.Temperature
		}
	} else {
		gc := &GenerationConfig{}
		if c.MaxTokens > 0 {
			gc.MaxOutputTokens = c.MaxTokens
		}
		if c.Temperature != nil {
			gc.Temperature = *c.Temperature
		}
		if gc.MaxOutputTokens > 0 || gc.Temperature != 0 {
			req.GenerationConfig = gc
		}
	}

	// Marshal the request to JSON
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create the URL with API key as query parameter
	// c.Find should be the full resource name, e.g., "projects/{project}/locations/{location}/models/{model}"
	apiURL := fmt.Sprintf("%s/%s:generateContent?key=%s", c.BaseURL, c.Model, c.APIKey)

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Observer start
	observer := mcbuf.ObserverFromContext(ctx)
	if observer != nil {
		var genReqJSON []byte
		if request != nil {
			genReqJSON, _ = json.Marshal(request)
		}
		if newCtx, obErr := observer.OnCallStart(ctx, mcbuf.Info{Provider: "gemini", LLMRequest: request, Model: c.Model, ModelKind: "chat", RequestJSON: data, Payload: genReqJSON, StartedAt: time.Now()}); obErr == nil {
			ctx = newCtx
		} else {
			return nil, fmt.Errorf("observer OnCallStart failed: %w", obErr)
		}
	}
	// Send the request
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for non-200 status code
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("gemini API error (status %d): %s", resp.StatusCode, respBytes)
	}

	// Unmarshal the response
	var apiResp Response
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Convert Response to llms.ChatResponse
	llmsResp := ToLLMSResponse(&apiResp)
	var usage *llm.Usage
	if llmsResp != nil {
		usage = llmsResp.Usage
	}
	if c.UsageListener != nil && usage != nil && usage.TotalTokens > 0 {
		c.UsageListener.OnUsage(c.Model, usage)
	}
	if observer != nil {
		info := mcbuf.Info{Provider: "gemini", Model: c.Model, ModelKind: "chat", ResponseJSON: respBytes, CompletedAt: time.Now(), Usage: usage, LLMResponse: llmsResp}
		if llmsResp != nil && len(llmsResp.Choices) > 0 {
			info.FinishReason = llmsResp.Choices[0].FinishReason
		}
		if obErr := observer.OnCallEnd(ctx, info); obErr != nil {
			return nil, fmt.Errorf("observer OnCallEnd failed: %w", obErr)
		}
	}
	return llmsResp, nil
}

// Stream sends a chat request to the Gemini API with streaming enabled and returns a channel of partial responses.
func (c *Client) Stream(ctx context.Context, request *llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	if c.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	if c.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	// Convert llm.GenerateRequest to wire request; for streaming we must use the
	// streamGenerateContent endpoint (no "stream" field in the request body).
	req, err := ToRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	// Ensure we do not send an unsupported field
	req.Stream = false

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	apiURL := fmt.Sprintf("%s/%s:streamGenerateContent?key=%s", c.BaseURL, c.Model, c.APIKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// Vertex Gemini stream returns application/json; request JSON explicitly.
	httpReq.Header.Set("Accept", "application/json")

	// Observer start
	observer := mcbuf.ObserverFromContext(ctx)
	if observer != nil {
		var genReqJSON []byte
		if request != nil {
			genReqJSON, _ = json.Marshal(request)
		}
		if newCtx, obErr := observer.OnCallStart(ctx, mcbuf.Info{Provider: "gemini", Model: c.Model, LLMRequest: request, ModelKind: "chat", RequestJSON: data, Payload: genReqJSON, StartedAt: time.Now()}); obErr == nil {
			ctx = newCtx
		} else {
			return nil, fmt.Errorf("observer OnCallStart failed: %w", obErr)
		}
	}
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		if observer != nil {
			if obErr := observer.OnCallEnd(ctx, mcbuf.Info{Provider: "gemini", Model: c.Model, ModelKind: "chat", CompletedAt: time.Now(), Err: err.Error()}); obErr != nil {
				return nil, fmt.Errorf("failed to send request: %w (observer OnCallEnd failed: %v)", err, obErr)
			}
		}
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if observer != nil {
			if obErr := observer.OnCallEnd(ctx, mcbuf.Info{Provider: "gemini", Model: c.Model, ModelKind: "chat", CompletedAt: time.Now(), Err: fmt.Sprintf("status %d", resp.StatusCode)}); obErr != nil {
				return nil, fmt.Errorf("gemini stream error (status %d): %s (observer OnCallEnd failed: %v)", resp.StatusCode, strings.TrimSpace(string(body)), obErr)
			}
		}
		return nil, fmt.Errorf("gemini stream error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	out := make(chan llm.StreamEvent)
	var wg sync.WaitGroup
	go func() {
		defer resp.Body.Close()
		defer close(out)
		_ = resp.Header.Get("Content-Type")
		agg := newGeminiAggregator(c.Model, c.UsageListener)
		// buffer events so we can track the last response for observer before publishing
		bufCh := make(chan llm.StreamEvent, 10)
		var lastLR *llm.GenerateResponse
		endObserver := func(final *llm.GenerateResponse) {
			if observer != nil {
				var respJSON []byte
				var finishReason string
				if final != nil {
					respJSON, _ = json.Marshal(final)
					if len(final.Choices) > 0 {
						finishReason = final.Choices[0].FinishReason
					}
				} else if lastLR != nil {
					respJSON, _ = json.Marshal(lastLR)
					if len(lastLR.Choices) > 0 {
						finishReason = lastLR.Choices[0].FinishReason
					}
				}
				if obErr := observer.OnCallEnd(ctx, mcbuf.Info{Provider: "gemini", Model: c.Model, ModelKind: "chat", ResponseJSON: respJSON, CompletedAt: time.Now(), Usage: agg.usage, FinishReason: finishReason, LLMResponse: func() *llm.GenerateResponse {
					if final != nil {
						return final
					}
					return lastLR
				}()}); obErr != nil {
					out <- llm.StreamEvent{Err: fmt.Errorf("observer OnCallEnd failed: %w", obErr)}
					return
				}
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ev := range bufCh {
				if ev.Kind == llm.StreamEventTurnCompleted {
					// Build a GenerateResponse for observer tracking
					lastLR = agg.buildResponse(ev.FinishReason)
				}
				out <- ev
			}
		}()
		// Gemini uses application/json streams; decode with JSON decoder.
		c.streamJSON(resp.Body, bufCh, agg, observer, ctx)
		close(bufCh)
		wg.Wait()
		// Emit remainder for non-finished candidates on stream end
		final := agg.emitRemainderEvents(out)
		endObserver(final)
	}()
	return out, nil
}

// geminiAggregator accumulates per-candidate content/tool calls and emits only when finished.
type geminiAggregator struct {
	model     string
	text      map[int]*strings.Builder
	tools     map[int][]llm.ToolCall
	finish    map[int]string
	usage     *llm.Usage
	listener  base.UsageListener
	published bool
}

func newGeminiAggregator(model string, listener base.UsageListener) *geminiAggregator {
	return &geminiAggregator{
		model:    model,
		text:     map[int]*strings.Builder{},
		tools:    map[int][]llm.ToolCall{},
		finish:   map[int]string{},
		listener: listener,
	}
}

func (a *geminiAggregator) addResponse(resp *Response) {
	// capture usage if provided in this chunk
	if resp.UsageMetadata != nil {
		u := &llm.Usage{
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
		}
		// Record only when meaningful
		if u.TotalTokens > 0 || u.PromptTokens > 0 || u.CompletionTokens > 0 {
			a.usage = u
			if a.listener != nil && !a.published && a.model != "" {
				a.listener.OnUsage(a.model, a.usage)
				a.published = true
			}
		}
	}
	for _, cand := range resp.Candidates {
		idx := cand.Index
		if _, ok := a.text[idx]; !ok {
			a.text[idx] = &strings.Builder{}
		}
		for _, p := range cand.Content.Parts {
			if p.Text != "" {
				a.text[idx].WriteString(p.Text)
			}
			if p.FunctionCall != nil {
				var args map[string]interface{}
				if p.FunctionCall.Args != nil {
					if m, ok := p.FunctionCall.Args.(map[string]interface{}); ok {
						args = m
					}
				} else if p.FunctionCall.Arguments != "" {
					_ = json.Unmarshal([]byte(p.FunctionCall.Arguments), &args)
				}
				ts := strings.TrimSpace(p.ThoughtSignature)
				if ts == "" {
					ts = fmt.Sprintf("%s%d", EMPTY_THOUGHT_SIGNATURE, time.Now().UnixNano())
				}
				tc := llm.ToolCall{ID: ts, Name: p.FunctionCall.Name, Arguments: args}
				a.tools[idx] = append(a.tools[idx], tc)
			}
		}
		if cand.FinishReason != "" {
			a.finish[idx] = cand.FinishReason
		}
	}
}

// emitFinished emits typed delta events for completed candidates, then clears them.
func (a *geminiAggregator) emitFinished(events chan<- llm.StreamEvent) {
	if len(a.finish) == 0 {
		return
	}
	for idx, reason := range a.finish {
		// Note: inline text deltas are emitted from streamJSON; the aggregator text is for observer/response building.

		// Emit each tool call as completed
		if calls := a.tools[idx]; len(calls) > 0 {
			for _, tc := range calls {
				events <- llm.StreamEvent{
					Kind:       llm.StreamEventToolCallCompleted,
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Arguments:  tc.Arguments,
				}
			}
		}

		// Emit usage if available
		if a.usage != nil && (a.usage.TotalTokens > 0 || a.usage.PromptTokens > 0 || a.usage.CompletionTokens > 0) {
			events <- llm.StreamEvent{Kind: llm.StreamEventUsage, Usage: a.usage}
		}

		// Emit turn completed
		events <- llm.StreamEvent{Kind: llm.StreamEventTurnCompleted, FinishReason: reason}

		delete(a.text, idx)
		delete(a.tools, idx)
	}
	a.finish = map[int]string{}
}

// buildResponse creates a GenerateResponse from current aggregator state (for observer tracking).
func (a *geminiAggregator) buildResponse(finishReason string) *llm.GenerateResponse {
	out := &llm.GenerateResponse{Model: a.model}
	for idx, b := range a.text {
		msg := llm.Message{Role: llm.RoleAssistant, Content: b.String()}
		if calls := a.tools[idx]; len(calls) > 0 {
			msg.ToolCalls = calls
		}
		reason := finishReason
		if r, ok := a.finish[idx]; ok && r != "" {
			reason = r
		}
		out.Choices = append(out.Choices, llm.Choice{Index: idx, Message: msg, FinishReason: reason})
	}
	if a.usage != nil && a.usage.TotalTokens > 0 {
		out.Usage = a.usage
	}
	if len(out.Choices) > 0 {
		return out
	}
	return nil
}

// emitRemainderEvents flushes any non-finished candidates on stream end as typed events, using STOP finish reason.
// It also returns a GenerateResponse for observer use.
func (a *geminiAggregator) emitRemainderEvents(events chan<- llm.StreamEvent) *llm.GenerateResponse {
	if len(a.text) == 0 && len(a.tools) == 0 {
		return nil
	}
	out := &llm.GenerateResponse{Model: a.model}
	for idx, b := range a.text {
		msg := llm.Message{Role: llm.RoleAssistant, Content: b.String()}
		// Emit tool calls
		if calls := a.tools[idx]; len(calls) > 0 {
			msg.ToolCalls = calls
			for _, tc := range calls {
				events <- llm.StreamEvent{
					Kind:       llm.StreamEventToolCallCompleted,
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Arguments:  tc.Arguments,
				}
			}
		}
		out.Choices = append(out.Choices, llm.Choice{Index: idx, Message: msg, FinishReason: "STOP"})
	}
	// Emit usage if available
	if a.usage != nil && (a.usage.TotalTokens > 0 || a.usage.PromptTokens > 0 || a.usage.CompletionTokens > 0) {
		events <- llm.StreamEvent{Kind: llm.StreamEventUsage, Usage: a.usage}
	}
	// Emit turn completed for remainder
	if len(out.Choices) > 0 {
		events <- llm.StreamEvent{Kind: llm.StreamEventTurnCompleted, FinishReason: "STOP"}
	}
	// clear state
	a.text = map[int]*strings.Builder{}
	a.tools = map[int][]llm.ToolCall{}
	a.finish = map[int]string{}
	if a.usage != nil && a.usage.TotalTokens > 0 {
		out.Usage = a.usage
	}
	if len(out.Choices) > 0 {
		return out
	}
	return nil
}

// emitChunkTextDeltas emits typed text delta events for each text part in a Gemini response chunk,
// and forwards them to the observer if present.
func emitChunkTextDeltas(obj *Response, events chan<- llm.StreamEvent, observer mcbuf.Observer, ctx context.Context) error {
	for _, cand := range obj.Candidates {
		for _, p := range cand.Content.Parts {
			if p.Text != "" {
				events <- llm.StreamEvent{Kind: llm.StreamEventTextDelta, Delta: p.Text}
				if observer != nil {
					if obErr := observer.OnStreamDelta(ctx, []byte(p.Text)); obErr != nil {
						return obErr
					}
				}
			}
		}
	}
	return nil
}

// streamJSON handles application/json streams. It supports:
// - a single JSON array where each element is a Response
// - multiple top-level JSON objects (sequential), separated by whitespace
func (c *Client) streamJSON(r io.Reader, events chan<- llm.StreamEvent, agg *geminiAggregator, observer mcbuf.Observer, ctx context.Context) {
	br := bufio.NewReader(r)
	isSpace := func(b byte) bool { return b == ' ' || b == '\n' || b == '\r' || b == '\t' }
	for {
		// skip leading whitespace between top-level values
		for {
			b, err := br.Peek(1)
			if err != nil {
				if err == io.EOF {
					return
				}
				events <- llm.StreamEvent{Err: fmt.Errorf("stream read error: %w", err)}
				return
			}
			if isSpace(b[0]) {
				_, _ = br.ReadByte()
				continue
			}
			break
		}

		b, err := br.Peek(1)
		if err != nil {
			if err == io.EOF {
				return
			}
			events <- llm.StreamEvent{Err: fmt.Errorf("stream read error: %w", err)}
			return
		}

		switch b[0] {
		case '[':
			dec := json.NewDecoder(br)
			// read opening array token
			tok, err := dec.Token()
			if err != nil {
				if err == io.EOF {
					return
				}
				events <- llm.StreamEvent{Err: fmt.Errorf("json decode error: %w", err)}
				return
			}
			if d, ok := tok.(json.Delim); !ok || d != '[' {
				events <- llm.StreamEvent{Err: fmt.Errorf("unexpected JSON, expected array start")}
				return
			}
			for dec.More() {
				var obj Response
				if err := dec.Decode(&obj); err != nil {
					if err == io.EOF {
						break
					}
					events <- llm.StreamEvent{Err: fmt.Errorf("json decode error: %w", err)}
					return
				}
				agg.addResponse(&obj)
				// emit inline text deltas and forward to observer
				if err := emitChunkTextDeltas(&obj, events, observer, ctx); err != nil {
					events <- llm.StreamEvent{Err: fmt.Errorf("observer OnStreamDelta failed: %w", err)}
					return
				}
				agg.emitFinished(events)
			}
			// consume closing ']'
			_, _ = dec.Token()
		case '{':
			dec := json.NewDecoder(br)
			var obj Response
			if err := dec.Decode(&obj); err != nil {
				if err == io.EOF {
					return
				}
				events <- llm.StreamEvent{Err: fmt.Errorf("json decode error: %w", err)}
				return
			}
			agg.addResponse(&obj)
			// emit inline text deltas and forward to observer
			if err := emitChunkTextDeltas(&obj, events, observer, ctx); err != nil {
				events <- llm.StreamEvent{Err: fmt.Errorf("observer OnStreamDelta failed: %w", err)}
				return
			}
			agg.emitFinished(events)
		default:
			// Unexpected leading byte; return an error to surface malformed stream
			events <- llm.StreamEvent{Err: fmt.Errorf("unexpected JSON stream start: %q", string(b[0]))}
			return
		}
	}
}
