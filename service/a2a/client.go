package a2a

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

	iauth "github.com/viant/agently-core/internal/auth"
)

// Client calls an external A2A agent over JSON-RPC HTTP.
type Client struct {
	url       string
	streamURL string // optional: SSE streaming endpoint (message/stream)
	headers   map[string]string
	http      *http.Client
	useStream bool // prefer message/stream to keep connection alive
}

// NewClient creates an A2A client for the given JSON-RPC endpoint URL.
// By default it uses message/stream (SSE) to keep long-running connections
// alive through load-balancer idle timeouts. Set WithStream(false) to
// disable and fall back to message/send.
func NewClient(url string, opts ...ClientOption) *Client {
	c := &Client{
		url:       url,
		headers:   make(map[string]string),
		http:      &http.Client{Timeout: 15 * time.Minute},
		useStream: true,
	}
	for _, o := range opts {
		o(c)
	}
	// Derive streaming URL from send URL when not explicitly set.
	if c.streamURL == "" && c.useStream {
		c.streamURL = strings.Replace(c.url, "message:send", "message:stream", 1)
		c.streamURL = strings.Replace(c.streamURL, "message/send", "message/stream", 1)
	}
	return c
}

// ClientOption configures an A2A client.
type ClientOption func(*Client)

// WithHeaders sets custom HTTP headers on all requests.
func WithHeaders(h map[string]string) ClientOption {
	return func(c *Client) {
		for k, v := range h {
			c.headers[k] = v
		}
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.http.Timeout = d }
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) { c.http = hc }
}

// WithStream controls whether the client uses the SSE message/stream endpoint
// (default: true) to keep the connection alive through load-balancer timeouts.
func WithStream(enabled bool) ClientOption {
	return func(c *Client) { c.useStream = enabled }
}

// WithStreamURL sets an explicit SSE streaming endpoint URL.
func WithStreamURL(url string) ClientOption {
	return func(c *Client) { c.streamURL = strings.TrimSpace(url) }
}

// SendMessage sends messages to an external A2A agent and returns the task.
// When streaming is enabled (default), it uses the SSE message/stream endpoint
// so the connection stays alive through load-balancer idle timeouts.
// contextID is optional — if provided, the remote agent reuses the conversation.
func (c *Client) SendMessage(ctx context.Context, messages []Message, contextID *string) (*Task, error) {
	if c.useStream && strings.TrimSpace(c.streamURL) != "" && c.streamURL != c.url {
		task, err := c.sendMessageStream(ctx, messages, contextID)
		if err == nil {
			return task, nil
		}
		// Fall back to synchronous send on stream error.
	}
	return c.sendMessageSync(ctx, messages, contextID)
}

// sendMessageSync sends via JSON-RPC message/send (synchronous, one response).
func (c *Client) sendMessageSync(ctx context.Context, messages []Message, contextID *string) (*Task, error) {
	params := map[string]interface{}{
		"messages": messages,
	}
	if contextID != nil && *contextID != "" {
		params["contextId"] = *contextID
	}
	result, err := c.call(ctx, "message/send", params)
	if err != nil {
		return nil, err
	}
	var task Task
	if err := json.Unmarshal(result, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	if task.ID == "" {
		var resp SendMessageResponse
		if err := json.Unmarshal(result, &resp); err == nil && resp.Task.ID != "" {
			return &resp.Task, nil
		}
	}
	return &task, nil
}

// sendMessageStream sends via SSE message/stream. The SSE connection keeps
// the HTTP channel open with periodic events, bypassing proxy idle timeouts.
// It returns the final task from the last status-update event.
func (c *Client) sendMessageStream(ctx context.Context, messages []Message, contextID *string) (*Task, error) {
	params := map[string]interface{}{
		"messages": messages,
	}
	if contextID != nil && *contextID != "" {
		params["contextId"] = *contextID
	}
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "message/stream",
		ID:      1,
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal stream params: %w", err)
	}
	rpcReq.Params = raw
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal stream request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.streamURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	if httpReq.Header.Get("Authorization") == "" {
		if idTok := iauth.IDToken(ctx); idTok != "" {
			httpReq.Header.Set("Authorization", "Bearer "+idTok)
		} else if bearer := iauth.Bearer(ctx); bearer != "" {
			httpReq.Header.Set("Authorization", "Bearer "+bearer)
		}
	}
	// Use a client without timeout for SSE — context controls cancellation.
	streamClient := &http.Client{Transport: c.http.Transport}
	httpResp, err := streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("stream request: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode >= 400 {
		b, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("stream HTTP %d: %s", httpResp.StatusCode, string(b))
	}
	// Read SSE events; return the last task seen.
	var lastTask *Task
	scanner := bufio.NewScanner(httpResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			continue
		}
		// Try unwrapping JSON-RPC envelope.
		var rpcResp JSONRPCResponse
		if err := json.Unmarshal([]byte(data), &rpcResp); err == nil && rpcResp.Result != nil {
			data = string(rpcResp.Result)
		}
		var event struct {
			Task *Task `json:"task"`
		}
		if err := json.Unmarshal([]byte(data), &event); err == nil && event.Task != nil {
			lastTask = event.Task
			continue
		}
		// The event may be the task directly.
		var t Task
		if err := json.Unmarshal([]byte(data), &t); err == nil && t.ID != "" {
			lastTask = &t
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	if lastTask == nil {
		return nil, fmt.Errorf("stream completed with no task event")
	}
	return lastTask, nil
}

// GetAgentCard fetches the agent card from the well-known endpoint.
func (c *Client) GetAgentCard(ctx context.Context) (*AgentCard, error) {
	result, err := c.call(ctx, "agent/getAuthenticatedExtendedCard", nil)
	if err != nil {
		return nil, err
	}
	var card AgentCard
	if err := json.Unmarshal(result, &card); err != nil {
		return nil, fmt.Errorf("unmarshal agent card: %w", err)
	}
	return &card, nil
}

// GetTask retrieves a task by ID from the remote agent.
func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	result, err := c.call(ctx, "tasks/get", map[string]interface{}{"id": taskID})
	if err != nil {
		return nil, err
	}
	var task Task
	if err := json.Unmarshal(result, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	return &task, nil
}

// CancelTask cancels a task on the remote agent.
func (c *Client) CancelTask(ctx context.Context, taskID string) (*Task, error) {
	result, err := c.call(ctx, "tasks/cancel", map[string]interface{}{"id": taskID})
	if err != nil {
		return nil, err
	}
	var task Task
	if err := json.Unmarshal(result, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	return &task, nil
}

// call makes a JSON-RPC 2.0 request.
func (c *Client) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		ID:      1,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		rpcReq.Params = raw
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	// Pass the caller's IdToken as Authorization for outbound A2A calls.
	// This enables token passthrough without us owning or refreshing the token.
	if httpReq.Header.Get("Authorization") == "" {
		if idTok := iauth.IDToken(ctx); idTok != "" {
			httpReq.Header.Set("Authorization", "Bearer "+idTok)
		} else if bearer := iauth.Bearer(ctx); bearer != "" {
			httpReq.Header.Set("Authorization", "Bearer "+bearer)
		}
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("JSON-RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}
