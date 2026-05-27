package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	mcpschema "github.com/viant/mcp-protocol/schema"
)

// MCPUIResourceReader resolves a host-owned MCP UI resource by exact uri.
// It is the same-origin, narrow read path the browser host uses; the host
// still owns the MCP transport, so the browser never speaks MCP directly.
type MCPUIResourceReader func(ctx context.Context, uri string) (*mcpschema.ReadResourceResult, error)

// MCPUIToolCaller executes a guest-originated MCP UI tool call through a
// host-owned, approval-aware backend path.
type MCPUIToolCaller func(ctx context.Context, input *MCPUIToolCallInput) (*MCPUIToolCallOutput, error)

// WithMCPUIResourceReader mounts GET /v1/api/mcp-ui/resources/read?uri=...
// when the reader is non-nil. The reader resolves UI resources through
// host-owned runtime code (no browser-side MCP client).
func WithMCPUIResourceReader(reader MCPUIResourceReader) HandlerOption {
	return func(c *handlerConfig) { c.mcpUIResourceReader = reader }
}

// WithMCPUIToolCaller mounts POST /v1/api/mcp-ui/tools/call when the caller is
// non-nil. The caller owns the canonical server-side execution path, including
// any approval and tool bundle behavior.
func WithMCPUIToolCaller(caller MCPUIToolCaller) HandlerOption {
	return func(c *handlerConfig) { c.mcpUIToolCaller = caller }
}

// MCPUIResourceReadResponse is the JSON envelope returned by the host-owned
// browser read endpoint. It is intentionally minimal: only the fields the
// browser host needs to assemble an iframe srcDoc and validate the resource.
type MCPUIResourceReadResponse struct {
	Uri      string                 `json:"uri"`
	MimeType string                 `json:"mimeType"`
	Text     string                 `json:"text"`
	Meta     map[string]interface{} `json:"_meta,omitempty"`
}

func handleMCPUIToolCall(caller MCPUIToolCaller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if caller == nil {
			httpError(w, http.StatusNotFound, errors.New("mcp ui tool caller not configured"))
			return
		}
		var input MCPUIToolCallInput
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				httpError(w, http.StatusBadRequest, err)
				return
			}
		}
		input.ToolName = strings.TrimSpace(input.ToolName)
		if input.ToolName == "" {
			httpError(w, http.StatusBadRequest, errors.New("toolName is required"))
			return
		}
		out, err := caller(r.Context(), &input)
		if err != nil {
			httpError(w, statusForToolExecuteError(err), err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleMCPUIResourceRead(reader MCPUIResourceReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uri := strings.TrimSpace(r.URL.Query().Get("uri"))
		if uri == "" {
			httpError(w, http.StatusBadRequest, errors.New("uri is required"))
			return
		}
		result, err := reader(r.Context(), uri)
		if err != nil {
			httpError(w, http.StatusNotFound, err)
			return
		}
		if result == nil || len(result.Contents) == 0 {
			httpError(w, http.StatusNotFound, fmt.Errorf("no contents for uri: %s", uri))
			return
		}
		content := result.Contents[0]
		mimeType := ""
		if content.MimeType != nil {
			mimeType = *content.MimeType
		}
		httpJSON(w, http.StatusOK, &MCPUIResourceReadResponse{
			Uri:      content.Uri,
			MimeType: mimeType,
			Text:     content.Text,
			Meta:     content.Meta,
		})
	}
}
