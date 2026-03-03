package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
)

// Client calls an external A2A agent over JSON-RPC HTTP.
type Client struct {
	url     string
	headers map[string]string
	http    *http.Client
}

// NewClient creates an A2A client for the given JSON-RPC endpoint URL.
func NewClient(url string, opts ...ClientOption) *Client {
	c := &Client{
		url:     url,
		headers: make(map[string]string),
		http:    &http.Client{Timeout: 120 * time.Second},
	}
	for _, o := range opts {
		o(c)
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

// SendMessage sends messages to an external A2A agent and returns the task.
// contextID is optional — if provided, the remote agent reuses the conversation.
func (c *Client) SendMessage(ctx context.Context, messages []Message, contextID *string) (*Task, error) {
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

	// The result can be either a Task directly or a SendMessageResponse wrapper.
	var task Task
	if err := json.Unmarshal(result, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	// If task.ID is empty, try unwrapping from SendMessageResponse.
	if task.ID == "" {
		var resp SendMessageResponse
		if err := json.Unmarshal(result, &resp); err == nil && resp.Task.ID != "" {
			return &resp.Task, nil
		}
	}
	return &task, nil
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
