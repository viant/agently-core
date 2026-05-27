package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	queueRead "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	toolreg "github.com/viant/agently-core/protocol/tool"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	agentsvc "github.com/viant/agently-core/service/agent"
	mcpschema "github.com/viant/mcp-protocol/schema"
	metaui "github.com/viant/mcp-ui/meta"
	mcpuiresource "github.com/viant/mcp-ui/resource"
)

const testMCPUIResourceURI = "ui://agently.wk_test/demo/show_widget"

func testMCPUIResourceReader(_ context.Context, uri string) (*mcpschema.ReadResourceResult, error) {
	if strings.TrimSpace(uri) != testMCPUIResourceURI {
		return nil, fmt.Errorf("unknown ui resource uri: %s", uri)
	}
	contents, err := mcpuiresource.NewReadResultHTMLContents(
		testMCPUIResourceURI,
		"<html><body>fixture</body></html>",
		metaui.ResourceUI{
			ContentHash:     mcpuiresource.ContentHash("<html><body>fixture</body></html>"),
			ProtocolVersion: "1.0.0",
		},
	)
	if err != nil {
		return nil, err
	}
	return &mcpschema.ReadResourceResult{Contents: []mcpschema.ReadResourceResultContentsElem{*contents}}, nil
}

func TestHandler_MCPUI_Read_ReturnsFixtureResource(t *testing.T) {
	handler := NewHandler(nil, WithMCPUIResourceReader(testMCPUIResourceReader))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/mcp-ui/resources/read?uri="+testMCPUIResourceURI, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got MCPUIResourceReadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got.Uri != testMCPUIResourceURI {
		t.Fatalf("unexpected uri: %s", got.Uri)
	}
	if got.MimeType != "text/html;profile=mcp-app" {
		t.Fatalf("unexpected mime type: %s", got.MimeType)
	}
	if got.Text == "" {
		t.Fatalf("expected non-empty text payload")
	}
	if got.Meta == nil {
		t.Fatalf("expected resource _meta to be returned, got nil")
	}
}

func TestHandler_MCPUI_Read_MissingURIReturnsBadRequest(t *testing.T) {
	handler := NewHandler(nil, WithMCPUIResourceReader(testMCPUIResourceReader))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/mcp-ui/resources/read", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing uri, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_MCPUI_Read_UnknownURIReturnsNotFound(t *testing.T) {
	handler := NewHandler(nil, WithMCPUIResourceReader(testMCPUIResourceReader))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/mcp-ui/resources/read?uri=ui://does/not/exist", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown uri, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_MCPUI_Read_RouteAbsentWhenReaderNotConfigured(t *testing.T) {
	handler := NewHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/api/mcp-ui/resources/read?uri="+testMCPUIResourceURI, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("expected route to be unmounted when reader not configured, got 200")
	}
}

func TestHandler_MCPUI_Read_EmptyContentsReturnsNotFound(t *testing.T) {
	reader := MCPUIResourceReader(func(_ context.Context, _ string) (*mcpschema.ReadResourceResult, error) {
		return &mcpschema.ReadResourceResult{}, nil
	})
	handler := NewHandler(nil, WithMCPUIResourceReader(reader))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/mcp-ui/resources/read?uri=ui://empty", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for empty contents, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_MCPUI_Read_LargeHTMLPassesThrough(t *testing.T) {
	largeHTML := strings.Repeat("a", 256*1024)
	reader := MCPUIResourceReader(func(_ context.Context, uri string) (*mcpschema.ReadResourceResult, error) {
		contents, err := mcpuiresource.NewReadResultHTMLContents(
			uri,
			largeHTML,
			metaui.ResourceUI{},
		)
		if err != nil {
			return nil, err
		}
		return &mcpschema.ReadResourceResult{Contents: []mcpschema.ReadResourceResultContentsElem{*contents}}, nil
	})
	handler := NewHandler(nil, WithMCPUIResourceReader(reader))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/mcp-ui/resources/read?uri=ui://agently.wk_ab12cd34ef56ab78/demo/show_widget", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for large html payload, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got MCPUIResourceReadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got.Text != largeHTML {
		t.Fatalf("expected html payload to pass through unchanged")
	}
}

func TestHandler_MCPUI_ToolCall_ReturnsQueuedApprovalPayload(t *testing.T) {
	caller := MCPUIToolCaller(func(_ context.Context, input *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) {
		return &MCPUIToolCallOutput{
			ConversationID: input.ConversationID,
			TurnID:         "turn-1",
			Status:         "queued",
			Result:         "queued for user approval",
			Source:         "guest_ui",
			Approval:       &PendingToolApproval{ID: "approval-1", ToolName: input.ToolName, Status: "pending"},
		}, nil
	})
	handler := NewHandler(nil, WithMCPUIToolCaller(caller))

	body := []byte(`{"conversationId":"conv-1","toolName":"system/os:getEnv","arguments":{"names":["LOGNAME"]},"toolBundles":["mcp_ui_preview_queue"]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/api/mcp-ui/tools/call", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got MCPUIToolCallOutput
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got.Status != "queued" || got.Approval == nil || got.Approval.ID != "approval-1" {
		t.Fatalf("unexpected tool call payload: %#v", got)
	}
	if got.Source != "guest_ui" {
		t.Fatalf("expected guest_ui source, got %#v", got)
	}
}

func TestHandler_MCPUI_ToolCall_MissingToolNameReturnsBadRequest(t *testing.T) {
	handler := NewHandler(nil, WithMCPUIToolCaller(func(_ context.Context, _ *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) {
		t.Fatal("caller should not be invoked for invalid payload")
		return nil, nil
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/api/mcp-ui/tools/call", bytes.NewReader([]byte(`{"toolName":""}`)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing toolName, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_MCPUI_ToolCall_RouteAbsentWhenCallerNotConfigured(t *testing.T) {
	handler := NewHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/api/mcp-ui/tools/call", bytes.NewReader([]byte(`{"toolName":"system/os:getEnv"}`)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("expected route to be unmounted when caller not configured, got 200")
	}
}

func TestHandler_MCPUI_ToolCall_HTTPIntegration_QueuedApprovalPath(t *testing.T) {
	ctx := context.Background()
	convClient := convmem.New()

	conv := conversation.NewConversation()
	conv.SetId("conv-http-mcpui")
	conv.SetStatus("running")
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	registry := &mcpuiHTTPTestRegistry{
		defs: []llm.ToolDefinition{
			{Name: "system/os:getEnv"},
		},
	}
	svc := agentsvc.New(nil, nil, nil, registry, nil, convClient,
		agentsvc.WithToolBundles(func(context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{{
				ID: "mcpuiverify_queue",
				Match: []llm.Tool{{
					Name: "system/os:*",
					Approval: &llm.ApprovalConfig{
						Mode:          llm.ApprovalModeQueue,
						QueueBehavior: llm.ApprovalQueueBehaviorDetach,
					},
				}},
			}}, nil
		}),
	)
	backend := &backendClient{
		agent:    svc,
		conv:     convClient,
		registry: registry,
	}

	handler := NewHandler(backend)
	body := []byte(`{"conversationId":"conv-http-mcpui","toolName":"system/os:getEnv","arguments":{"names":["HOME","PATH"]},"toolBundles":["mcpuiverify_queue"]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/api/mcp-ui/tools/call", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "unexpected status body=%s", rec.Body.String())
	var got MCPUIToolCallOutput
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got), "decode response body=%s", rec.Body.String())
	require.Equal(t, "conv-http-mcpui", got.ConversationID)
	require.Equal(t, "queued", got.Status)
	require.Equal(t, "queued for user approval", got.Result)
	require.Equal(t, "guest_ui", got.Source)
	require.NotNil(t, got.Approval)
	require.Equal(t, "pending", got.Approval.Status)
	require.Equal(t, "system/os/getEnv", got.Approval.ToolName)
	require.False(t, registry.executed, "approval-gated tool must not execute registry")

	rows, err := convClient.ListToolApprovalQueues(ctx, &queueRead.QueueRowsInput{
		ConversationId: "conv-http-mcpui",
		QueueStatus:    "pending",
		Has: &queueRead.QueueRowsInputHas{
			ConversationId: true,
			QueueStatus:    true,
		},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1, "queue row must be persisted through the real queue writer")
	require.Equal(t, got.Approval.ID, rows[0].Id)
	require.Equal(t, "pending", rows[0].Status)
	require.Equal(t, "system/os/getEnv", rows[0].ToolName)
	require.NotNil(t, rows[0].Metadata)
	var metadata map[string]interface{}
	require.NoError(t, json.Unmarshal(*rows[0].Metadata, &metadata))
	require.Equal(t, "guest_ui", metadata["source"])
}

type mcpuiHTTPTestRegistry struct {
	defs     []llm.ToolDefinition
	executed bool
}

func (r *mcpuiHTTPTestRegistry) Definitions() []llm.ToolDefinition { return r.defs }
func (r *mcpuiHTTPTestRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	pattern = strings.TrimSpace(pattern)
	var out []*llm.ToolDefinition
	for i := range r.defs {
		name := r.defs[i].Name
		if mcpuiHTTPTestMatchPattern(pattern, name) {
			d := r.defs[i]
			out = append(out, &d)
		}
	}
	return out
}
func (r *mcpuiHTTPTestRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	for i := range r.defs {
		if mcpuiHTTPTestCanon(r.defs[i].Name) == mcpuiHTTPTestCanon(name) {
			return &r.defs[i], true
		}
	}
	return nil, false
}
func (r *mcpuiHTTPTestRegistry) MustHaveTools(patterns []string) ([]llm.Tool, error) { return nil, nil }
func (r *mcpuiHTTPTestRegistry) Execute(context.Context, string, map[string]interface{}) (string, error) {
	r.executed = true
	return `{"values":{"HOME":"/tmp"}}`, nil
}
func (r *mcpuiHTTPTestRegistry) SetDebugLogger(io.Writer)   {}
func (r *mcpuiHTTPTestRegistry) Initialize(context.Context) {}

var _ toolreg.Registry = (*mcpuiHTTPTestRegistry)(nil)

func mcpuiHTTPTestMatchPattern(pattern, name string) bool {
	if strings.TrimSpace(pattern) == "" {
		return false
	}
	raw := strings.TrimSpace(pattern)
	switch {
	case strings.HasSuffix(raw, "/*"):
		root := strings.TrimSuffix(raw, "/*")
		service := mcpname.Name(mcpname.Canonical(name)).Service()
		return service == root || strings.HasPrefix(service, root+"/")
	case strings.HasSuffix(raw, ":*"):
		root := strings.TrimSuffix(raw, ":*")
		service := mcpname.Name(mcpname.Canonical(name)).Service()
		return service == root
	}
	pcanon := mcpuiHTTPTestCanon(pattern)
	ncanon := mcpuiHTTPTestCanon(name)
	if pcanon == "*" {
		return true
	}
	if pcanon == ncanon {
		return true
	}
	if strings.Contains(pcanon, "*") {
		prefix := strings.TrimSuffix(pcanon, "*")
		return strings.HasPrefix(ncanon, prefix)
	}
	if raw != "" && !strings.Contains(raw, ":") && !strings.Contains(raw, "*") {
		return strings.HasPrefix(ncanon, pcanon)
	}
	return false
}

func mcpuiHTTPTestCanon(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return mcpname.Canonical(value)
}
