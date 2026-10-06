package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/viant/agently-core/genai/llm"
)

// CountInputTokens uses the same prepared Responses payload as generation. It
// never records a model call or invokes generation, and does not log its body.
func (c *Client) CountInputTokens(ctx context.Context, request *llm.GenerateRequest) (int, error) {
	if isChatGPTBackendURL(c.BaseURL) {
		return 0, fmt.Errorf("exact input token counting is unavailable for the ChatGPT backend; use an OpenAI Responses API model")
	}
	req, err := c.prepareChatRequestContext(ctx, request)
	if err != nil {
		return 0, err
	}
	if !shouldUseResponsesAPI(c, req) {
		return 0, fmt.Errorf("exact input token counting requires an OpenAI Responses API model")
	}
	payload, err := c.marshalInputTokenCountPayload(req)
	if err != nil {
		return 0, err
	}
	httpReq, err := c.createHTTPResponsesApiRequest(ctx, payload)
	if err != nil {
		return 0, err
	}
	httpReq.URL.Path = strings.TrimRight(httpReq.URL.Path, "/") + "/input_tokens"
	httpReq, cancel := c.cloneWithTimeout(ctx, httpReq)
	defer cancel()
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("OpenAI input token count request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Code  string `json:"code"`
				Param string `json:"param"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&failure)
		return 0, fmt.Errorf("OpenAI input token count endpoint returned HTTP %d (code=%q parameter=%q); check model and endpoint support", resp.StatusCode, failure.Error.Code, failure.Error.Param)
	}
	var result struct {
		InputTokens *int `json:"input_tokens"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return 0, fmt.Errorf("invalid OpenAI input token count response: %w", err)
	}
	if result.InputTokens == nil || *result.InputTokens < 0 {
		return 0, fmt.Errorf("OpenAI input token count response has no valid input_tokens")
	}
	return *result.InputTokens, nil
}

// marshalInputTokenCountPayload preserves the exact serialized input fields.
// The count endpoint shares Responses input formatting, but rejects generation-
// only settings such as max_output_tokens, stream, include and prompt_cache_key.
// See official InputTokenCountParams in openai/openai-python.
func (c *Client) marshalInputTokenCountPayload(req *Request) ([]byte, error) {
	raw, err := c.marshalResponsesApiRequestBody(req)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	allowed := map[string]bool{"model": true, "input": true, "instructions": true, "tools": true, "tool_choice": true, "parallel_tool_calls": true, "reasoning": true, "text": true, "previous_response_id": true, "conversation": true, "personality": true, "truncation": true}
	for name := range fields {
		if !allowed[name] {
			delete(fields, name)
		}
	}
	return json.Marshal(fields)
}
