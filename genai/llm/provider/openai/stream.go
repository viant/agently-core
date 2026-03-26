package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/memory"
	mcbuf "github.com/viant/agently-core/service/core/modelcall"
)

type streamState struct {
	lastUsage      *llm.Usage
	lastModel      string
	lastLR         *llm.GenerateResponse
	lastProvider   []byte
	ended          bool
	publishedUsage bool
	lastResponseID string
	// Track whether assistant text was already emitted as streaming deltas so the
	// terminal full response does not replay the same text into downstream accumulators.
	emittedAssistantText bool
	// Track tool call IDs that were already emitted to avoid duplicates
	emittedToolCallIDs map[string]struct{}
	// Track arguments for emitted tool calls (for duplicate diagnostics)
	emittedToolCallArgs map[string]string // id -> raw args
}

type streamProcessor struct {
	client   *Client
	ctx      context.Context
	observer mcbuf.Observer
	events   chan<- llm.StreamEvent
	agg      *streamAggregator
	state    *streamState
	respBody []byte

	// Pending function calls keyed by provider item_id
	fcPending map[string]*pendingFuncCall

	// For fallback on continuation errors
	req  *Request
	orig *llm.GenerateRequest
}

type pendingFuncCall struct {
	ItemID  string
	CallID  string
	Name    string
	ArgsBuf strings.Builder
}

func firstResponseText(items ...string) string {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return item
		}
	}
	return ""
}

func extractOutputTextFromContentItems(items []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Type), "output_text") && strings.TrimSpace(item.Text) != "" {
			return item.Text
		}
	}
	return ""
}

func (p *streamProcessor) emitAssistantTextDelta(itemID, delta string) bool {
	if strings.TrimSpace(delta) == "" {
		return true
	}
	if p.observer != nil {
		if err := p.observer.OnStreamDelta(p.ctx, []byte(delta)); err != nil {
			p.events <- llm.StreamEvent{Err: fmt.Errorf("observer OnStreamDelta failed: %w", err)}
			return false
		}
	}
	p.markAssistantTextEmitted(delta)
	p.events <- llm.StreamEvent{
		Kind:       llm.StreamEventTextDelta,
		ResponseID: p.state.lastResponseID,
		ItemID:     strings.TrimSpace(itemID),
		Delta:      delta,
		Role:       llm.RoleAssistant,
	}
	ch := StreamChoice{Index: 0, Delta: DeltaMessage{Content: &delta}}
	p.agg.updateDelta(ch)
	return true
}

// recordEmittedToolCall marks a tool call as emitted.
func (p *streamProcessor) recordEmittedToolCall(callID string) {
	id := strings.TrimSpace(callID)
	if id == "" {
		return
	}
	if p.state.emittedToolCallIDs == nil {
		p.state.emittedToolCallIDs = map[string]struct{}{}
	}
	p.state.emittedToolCallIDs[id] = struct{}{}
}

// recordEmittedToolCallWith stores id and arguments for later duplicate diagnostics.
func (p *streamProcessor) recordEmittedToolCallWith(callID, name, args string) {
	p.recordEmittedToolCall(callID)
	id := strings.TrimSpace(callID)
	if id == "" {
		return
	}
	if p.state.emittedToolCallArgs == nil {
		p.state.emittedToolCallArgs = map[string]string{}
	}
	p.state.emittedToolCallArgs[id] = args
}

// hasEmittedToolCall checks if a tool call ID was already emitted.
func (p *streamProcessor) hasEmittedToolCall(callID string) bool {
	if p.state.emittedToolCallIDs == nil {
		return false
	}
	_, ok := p.state.emittedToolCallIDs[strings.TrimSpace(callID)]
	return ok
}

// removeAlreadyEmittedToolCalls filters duplicate tool calls from a final response.
func (p *streamProcessor) removeAlreadyEmittedToolCalls(lr *llm.GenerateResponse) *llm.GenerateResponse {
	if lr == nil || len(lr.Choices) == 0 {
		return lr
	}
	out := make([]llm.Choice, 0, len(lr.Choices))
	for _, ch := range lr.Choices {
		msg := ch.Message
		if len(msg.ToolCalls) == 0 {
			out = append(out, ch)
			continue
		}
		// Keep only tool calls that were not already emitted.
		kept := make([]llm.ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if !p.hasEmittedToolCall(tc.ID) {
				kept = append(kept, tc)
				// Mark as emitted to avoid any later duplicates in the same stream
				p.recordEmittedToolCallWith(tc.ID, tc.Name, tc.Function.Arguments)
			}
		}
		msg.ToolCalls = kept
		// If a choice becomes empty (no tool calls and no text), drop it.
		hasText := strings.TrimSpace(msg.Content) != ""
		if hasText || len(msg.ToolCalls) > 0 {
			ch.Message = msg
			out = append(out, ch)
		}
	}
	lr.Choices = out
	return lr
}

func cloneGenerateResponse(lr *llm.GenerateResponse) *llm.GenerateResponse {
	if lr == nil {
		return nil
	}
	cp := *lr
	if lr.Usage != nil {
		usage := *lr.Usage
		cp.Usage = &usage
	}
	if len(lr.Choices) > 0 {
		cp.Choices = make([]llm.Choice, len(lr.Choices))
		for i, ch := range lr.Choices {
			cp.Choices[i] = ch
			if len(ch.Message.ToolCalls) > 0 {
				cp.Choices[i].Message.ToolCalls = make([]llm.ToolCall, len(ch.Message.ToolCalls))
				copy(cp.Choices[i].Message.ToolCalls, ch.Message.ToolCalls)
			}
			if len(ch.Message.Items) > 0 {
				cp.Choices[i].Message.Items = make([]llm.ContentItem, len(ch.Message.Items))
				copy(cp.Choices[i].Message.Items, ch.Message.Items)
			}
			if len(ch.Message.ContentItems) > 0 {
				cp.Choices[i].Message.ContentItems = make([]llm.ContentItem, len(ch.Message.ContentItems))
				copy(cp.Choices[i].Message.ContentItems, ch.Message.ContentItems)
			}
			if ch.Message.FunctionCall != nil {
				fn := *ch.Message.FunctionCall
				cp.Choices[i].Message.FunctionCall = &fn
			}
		}
	}
	return &cp
}

func (p *streamProcessor) markAssistantTextEmitted(txt string) {
	if strings.TrimSpace(txt) == "" {
		return
	}
	p.state.emittedAssistantText = true
}

func (p *streamProcessor) shouldEmitTerminalResponse(lr *llm.GenerateResponse) bool {
	if lr == nil || !p.state.emittedAssistantText || len(lr.Choices) == 0 {
		return false
	}
	choice := lr.Choices[0]
	if len(choice.Message.ToolCalls) > 0 {
		return false
	}
	return strings.TrimSpace(llm.MessageText(choice.Message)) != ""
}

func (p *streamProcessor) emitFinalResponse(lr *llm.GenerateResponse) {
	if p.shouldEmitTerminalResponse(lr) {
		emitTerminalResponse(p.events, lr)
		return
	}
	emitResponse(p.events, lr)
}

func (p *streamProcessor) handleEvent(eventName string, data string) bool {
	if eventName == "error" {
		msg, code := parseOpenAIError([]byte(data))
		if msg == "" {
			msg = "openai stream error"
		}
		p.events <- llm.StreamEvent{Err: fmt.Errorf("%s", msg)}
		if p.observer != nil && !p.state.ended {
			if err := endObserverErrorOnce(p.observer, p.ctx, p.state.lastModel, []byte(data), msg, code, &p.state.ended); err != nil {
				p.events <- llm.StreamEvent{Err: fmt.Errorf("observer OnCallEnd failed: %w", err)}
			}
		}
		return false
	}
	// Handle Responses API streaming events
	if strings.HasPrefix(eventName, "response.") {
		switch eventName {
		case "response.created", "response.in_progress":
			// record current response id if present
			var e struct {
				Response struct {
					ID string `json:"id"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil && strings.TrimSpace(e.Response.ID) != "" {
				p.state.lastResponseID = strings.TrimSpace(e.Response.ID)
				if turn, ok := memory.TurnMetaFromContext(p.ctx); ok {
					memory.SetTurnTrace(turn.TurnID, p.state.lastResponseID)
				}
			}
			return true
		case "response.output_item.added":
			// Detect function_call item and start tracking
			var e struct {
				Item struct {
					ID        string `json:"id"`
					Type      string `json:"type"`
					Role      string `json:"role"`
					Name      string `json:"name"`
					CallID    string `json:"call_id"`
					Arguments string `json:"arguments"`
					Status    string `json:"status"`
					Content   []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"item"`
				OutputIndex int `json:"output_index"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil {
				if strings.EqualFold(e.Item.Type, "function_call") {
					if p.fcPending == nil {
						p.fcPending = map[string]*pendingFuncCall{}
					}
					p.fcPending[e.Item.ID] = &pendingFuncCall{ItemID: e.Item.ID, CallID: e.Item.CallID, Name: e.Item.Name}
					// Emit typed tool_call_started event with stable IDs.
					p.events <- llm.StreamEvent{
						Kind:       llm.StreamEventToolCallStarted,
						ResponseID: p.state.lastResponseID,
						ItemID:     e.Item.ID,
						ToolCallID: e.Item.CallID,
						ToolName:   e.Item.Name,
					}
				}
			}
			return true
		case "response.function_call_arguments.delta":
			// Append function_call arguments chunks by item_id
			var e struct {
				ItemID      string `json:"item_id"`
				OutputIndex int    `json:"output_index"`
				Delta       string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil {
				if p.fcPending != nil {
					if fc := p.fcPending[e.ItemID]; fc != nil {
						fc.ArgsBuf.WriteString(e.Delta)
						// Emit typed tool_call_delta with stable IDs.
						p.events <- llm.StreamEvent{
							Kind:       llm.StreamEventToolCallDelta,
							ResponseID: p.state.lastResponseID,
							ItemID:     e.ItemID,
							ToolCallID: fc.CallID,
							ToolName:   fc.Name,
							Delta:      e.Delta,
						}
					}
				}
			}
			return true
		case "response.output_item.done":
			// Finalize a function_call
			var e struct {
				Item struct {
					ID        string `json:"id"`
					Type      string `json:"type"`
					Role      string `json:"role"`
					Name      string `json:"name"`
					CallID    string `json:"call_id"`
					Arguments string `json:"arguments"`
					Status    string `json:"status"`
					Content   []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"item"`
				OutputIndex int `json:"output_index"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil {
				if strings.EqualFold(e.Item.Type, "message") {
					if delta := extractOutputTextFromContentItems(e.Item.Content); delta != "" {
						return p.emitAssistantTextDelta(e.Item.ID, delta)
					}
					return true
				}
				if strings.EqualFold(e.Item.Type, "function_call") {
					var fargs string
					if p.fcPending != nil {
						if fc := p.fcPending[e.Item.ID]; fc != nil {
							fargs = fc.ArgsBuf.String()
							if fargs == "" {
								fargs = e.Item.Arguments
							}
							delete(p.fcPending, e.Item.ID)
							// Emit tool call message now
							var args map[string]interface{}
							_ = json.Unmarshal([]byte(fargs), &args)
							msg := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
								ID:        fc.CallID,
								Name:      fc.Name,
								Arguments: args,
								Type:      "function",
								Function:  llm.FunctionCall{Name: fc.Name, Arguments: fargs},
							}}}
							// Mark as emitted to avoid duplication on response.completed
							p.recordEmittedToolCallWith(fc.CallID, fc.Name, fargs)
							// Do not maintain in-memory anchors; TraceID is persisted via recorder/exec.
							lr := &llm.GenerateResponse{Choices: []llm.Choice{{Index: e.OutputIndex, Message: msg, FinishReason: "tool_calls"}}, Model: p.state.lastModel}
							if rid := strings.TrimSpace(p.state.lastResponseID); rid != "" {
								lr.ResponseID = rid
							}
							emitResponse(p.events, lr)
							p.state.lastLR = lr
							return true
						}
					}
				}
			}
			return true
		case "response.output_text.delta":
			var d struct {
				Delta       string `json:"delta"`
				ItemID      string `json:"item_id"`
				OutputIndex int    `json:"output_index"`
			}
			if err := json.Unmarshal([]byte(data), &d); err == nil && d.Delta != "" {
				return p.emitAssistantTextDelta(d.ItemID, d.Delta)
			}
			// tolerate shape variances
			return true
		case "response.output_item.delta":
			var e struct {
				Item struct {
					ID      string `json:"id"`
					Type    string `json:"type"`
					Role    string `json:"role"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"item"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil {
				if strings.EqualFold(strings.TrimSpace(e.Item.Type), "message") {
					if delta := extractOutputTextFromContentItems(e.Item.Content); delta != "" {
						return p.emitAssistantTextDelta(e.Item.ID, delta)
					}
				}
			}
			return true
		case "response.content_part.added", "response.content_part.done":
			var e struct {
				ItemID string `json:"item_id"`
				Part   *struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"part"`
				ContentPart *struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content_part"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil {
				partType := firstResponseText(
					func() string {
						if e.Part != nil {
							return e.Part.Type
						}
						return ""
					}(),
					func() string {
						if e.ContentPart != nil {
							return e.ContentPart.Type
						}
						return ""
					}(),
				)
				if strings.EqualFold(strings.TrimSpace(partType), "output_text") {
					delta := firstResponseText(
						func() string {
							if e.Part != nil {
								return e.Part.Text
							}
							return ""
						}(),
						func() string {
							if e.ContentPart != nil {
								return e.ContentPart.Text
							}
							return ""
						}(),
					)
					if delta != "" {
						return p.emitAssistantTextDelta(e.ItemID, delta)
					}
				}
			}
			return true
		case "response.refusal.delta":
			var e struct {
				ItemID string `json:"item_id"`
				Delta  string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil && e.Delta != "" {
				return p.emitAssistantTextDelta(e.ItemID, e.Delta)
			}
			return true
		case "response.message.delta":
			var e struct {
				ItemID string `json:"item_id"`
				Delta  struct {
					Content string `json:"content"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil && e.Delta.Content != "" {
				return p.emitAssistantTextDelta(e.ItemID, e.Delta.Content)
			}
			return true
		case "response.message.tool_call.delta", "response.tool_call.delta":
			// Attempt to parse commonly observed tool_call delta shapes
			var d1 struct {
				Index int `json:"index"`
				Delta struct {
					ToolCall struct {
						ID        string `json:"id,omitempty"`
						Type      string `json:"type,omitempty"`
						Name      string `json:"name,omitempty"`
						Arguments string `json:"arguments,omitempty"`
					} `json:"tool_call"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &d1); err == nil {
				idx := d1.Index
				tc := ToolCallDelta{Index: idx, ID: d1.Delta.ToolCall.ID, Type: d1.Delta.ToolCall.Type, Function: FunctionCallDelta{Name: d1.Delta.ToolCall.Name, Arguments: d1.Delta.ToolCall.Arguments}}
				p.agg.updateDelta(StreamChoice{Index: idx, Delta: DeltaMessage{ToolCalls: []ToolCallDelta{tc}}})
				// No-op for anchors; TurnTrace already captures the response.id.
				return true
			}
			// Tolerate alternative shape with top-level fields
			var d2 struct {
				Index     int    `json:"index"`
				ID        string `json:"id,omitempty"`
				Type      string `json:"type,omitempty"`
				Name      string `json:"name,omitempty"`
				Arguments string `json:"arguments,omitempty"`
			}
			if err := json.Unmarshal([]byte(data), &d2); err == nil {
				tc := ToolCallDelta{Index: d2.Index, ID: d2.ID, Type: d2.Type, Function: FunctionCallDelta{Name: d2.Name, Arguments: d2.Arguments}}
				p.agg.updateDelta(StreamChoice{Index: d2.Index, Delta: DeltaMessage{ToolCalls: []ToolCallDelta{tc}}})
				// No-op for anchors; TurnTrace already captures the response.id.
				return true
			}
			return true
		case "response.completed":
			if strings.TrimSpace(data) != "" {
				p.state.lastProvider = []byte(data)
			}
			// Final full response object; either wrapped or direct
			// First try wrapped
			var wrap struct {
				Response *ResponsesResponse `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &wrap); err == nil && wrap.Response != nil {
				lr := ToLLMSFromResponses(wrap.Response)
				observerLR := cloneGenerateResponse(lr)
				lr = p.removeAlreadyEmittedToolCalls(lr)
				if p.state.lastModel == "" {
					p.state.lastModel = lr.Model
				}
				if lr.Usage != nil {
					p.state.lastUsage = lr.Usage
				}
				p.client.publishUsageOnce(p.state.lastModel, p.state.lastUsage, &p.state.publishedUsage)
				if err := endObserverOnce(p.observer, p.ctx, p.state.lastModel, observerLR, p.state.lastUsage, []byte(data), &p.state.ended); err != nil {
					p.events <- llm.StreamEvent{Err: fmt.Errorf("observer OnCallEnd failed: %w", err)}
				}
				p.emitFinalResponse(lr)
				p.state.lastLR = lr
				return true
			}
			// Try direct
			var r2 ResponsesResponse
			if err := json.Unmarshal([]byte(data), &r2); err == nil && (r2.ID != "" || len(r2.Output) > 0) {
				lr := ToLLMSFromResponses(&r2)
				observerLR := cloneGenerateResponse(lr)
				lr = p.removeAlreadyEmittedToolCalls(lr)
				if p.state.lastModel == "" {
					p.state.lastModel = lr.Model
				}
				if lr.Usage != nil {
					p.state.lastUsage = lr.Usage
				}
				p.client.publishUsageOnce(p.state.lastModel, p.state.lastUsage, &p.state.publishedUsage)
				if err := endObserverOnce(p.observer, p.ctx, p.state.lastModel, observerLR, p.state.lastUsage, []byte(data), &p.state.ended); err != nil {
					p.events <- llm.StreamEvent{Err: fmt.Errorf("observer OnCallEnd failed: %w", err)}
				}
				p.emitFinalResponse(lr)
				p.state.lastLR = lr
				return true
			}
			return true
		case "response.failed":
			msg, code := parseOpenAIError([]byte(data))
			if msg == "" {
				msg = "openai response.failed"
			}
			p.events <- llm.StreamEvent{Err: fmt.Errorf("%s", msg)}
			if p.observer != nil && !p.state.ended {
				if err := endObserverErrorOnce(p.observer, p.ctx, p.state.lastModel, []byte(data), msg, code, &p.state.ended); err != nil {
					p.events <- llm.StreamEvent{Err: fmt.Errorf("observer OnCallEnd failed: %w", err)}
				}
			}
			return false
		default:
			// Ignore other response.* events
			return true
		}
	}
	// 1) Aggregated delta chunk
	var sresp StreamResponse
	if err := json.Unmarshal([]byte(data), &sresp); err == nil && len(sresp.Choices) > 0 {
		if sresp.Model != "" {
			p.state.lastModel = sresp.Model
		}
		finalized := make([]llm.Choice, 0)
		for _, ch := range sresp.Choices {
			p.agg.updateDelta(ch)
			// Emit text stream delta to observer when content arrives
			if ch.Delta.Content != nil {
				if txt := *ch.Delta.Content; txt != "" {
					if p.observer != nil {
						if err := p.observer.OnStreamDelta(p.ctx, []byte(txt)); err != nil {
							p.events <- llm.StreamEvent{Err: fmt.Errorf("observer OnStreamDelta failed: %w", err)}
							return false
						}
					}
					// Emit typed text delta for chat/completions path.
					p.markAssistantTextEmitted(txt)
					p.events <- llm.StreamEvent{
						Kind:       llm.StreamEventTextDelta,
						ResponseID: p.state.lastResponseID,
						Delta:      txt,
						Role:       llm.RoleAssistant,
					}
				}
			}
			if ch.FinishReason != nil {
				finalized = append(finalized, p.agg.finalizeChoice(ch.Index, *ch.FinishReason))
			}
		}
		if len(finalized) > 0 {
			lr := &llm.GenerateResponse{Choices: finalized, Model: p.state.lastModel}
			if p.state.lastUsage != nil && p.state.lastUsage.TotalTokens > 0 {
				lr.Usage = p.state.lastUsage
			}
			p.client.publishUsageOnce(p.state.lastModel, p.state.lastUsage, &p.state.publishedUsage)
			emitResponse(p.events, lr)
			p.state.lastLR = lr
		}
		return true
	}

	// 2) Final response object
	var apiResp Response
	if err := json.Unmarshal([]byte(data), &apiResp); err != nil {
		// Tolerate non-standard or intermediary payloads that are not valid
		// JSON responses (e.g., provider diagnostics). Ignore and continue
		// scanning rather than failing the whole stream.
		return true
	}
	lr := ToLLMSResponse(&apiResp)
	// Keep a snapshot of the last provider-level response object for persistence
	if b, err := json.Marshal(apiResp); err == nil {
		p.state.lastProvider = b
	}

	// If this is a usage-only final chunk (OpenAI streams often end with
	// an object whose choices == [] but usage is populated), do NOT emit an
	// empty-choices response. Capture usage and model, but leave final message
	// emission to previously finalized choices or to finalize().
	if lr != nil && len(lr.Choices) == 0 {
		if lr.Usage != nil && lr.Usage.TotalTokens > 0 {
			if p.state.lastModel == "" && lr.Model != "" {
				p.state.lastModel = lr.Model
			}
			p.state.lastUsage = lr.Usage
			p.client.publishUsageOnce(p.state.lastModel, p.state.lastUsage, &p.state.publishedUsage)
			// Re-emit the last aggregated response with usage attached, if available
			if p.state.lastLR != nil {
				// clone shallow and attach usage
				updated := *p.state.lastLR
				updated.Usage = lr.Usage
				updated.Model = p.state.lastModel
				p.emitFinalResponse(&updated)
				p.state.lastLR = &updated
			}
		}
		// Do not end observer here; finalize() will notify with accumulated text
		return true
	}

	if lr != nil && lr.Usage != nil && lr.Usage.TotalTokens > 0 {
		if p.state.lastModel == "" && lr.Model != "" {
			p.state.lastModel = lr.Model
		}
		p.state.lastUsage = lr.Usage
	}
	p.client.publishUsageOnce(p.state.lastModel, p.state.lastUsage, &p.state.publishedUsage)
	if err := endObserverOnce(p.observer, p.ctx, p.state.lastModel, lr, p.state.lastUsage, nil, &p.state.ended); err != nil {
		p.events <- llm.StreamEvent{Err: fmt.Errorf("observer OnCallEnd failed: %w", err)}
	}
	p.emitFinalResponse(lr)
	p.state.lastLR = lr
	return true
}

func (p *streamProcessor) finalize(scannerErr error) {
	if scannerErr != nil {
		p.events <- llm.StreamEvent{Err: fmt.Errorf("stream read error: %w", scannerErr)}
	}

	if p.observer != nil && !p.state.ended {
		var usage *llm.Usage
		if p.state.lastUsage != nil {
			usage = p.state.lastUsage
		}
		var respJSON []byte
		var finishReason string
		if p.state.lastLR != nil {
			// Prefer provider final object snapshot when available; fallback to SSE body
			if len(p.state.lastProvider) > 0 {
				respJSON = p.state.lastProvider
			} else {
				respJSON = p.respBody
			}
			if len(p.state.lastLR.Choices) > 0 {
				finishReason = p.state.lastLR.Choices[0].FinishReason
			}
		} else {
			respJSON = p.respBody
		}
		// Extract plain text content (vanilla stream) for persistence convenience
		var streamTxt string
		if p.state.lastLR != nil {
			for _, ch := range p.state.lastLR.Choices {
				if strings.TrimSpace(ch.Message.Content) != "" {
					streamTxt = strings.TrimSpace(ch.Message.Content)
					break
				}
			}
		}
		if err := p.observer.OnCallEnd(p.ctx, mcbuf.Info{Provider: "openai", Model: p.state.lastModel, ModelKind: "chat", ResponseJSON: respJSON, CompletedAt: time.Now(), Usage: usage, FinishReason: finishReason, LLMResponse: p.state.lastLR, StreamText: streamTxt}); err != nil {
			p.events <- llm.StreamEvent{Err: fmt.Errorf("observer OnCallEnd failed: %w", err)}
		}
	}
}
