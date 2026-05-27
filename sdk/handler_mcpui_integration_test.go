package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	queueread "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	"github.com/viant/agently-core/protocol/tool"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	agentsvc "github.com/viant/agently-core/service/agent"
)

// integrationToolRegistry is the minimal tool.Registry surface the MCP UI
// guest-tool path needs in order to discover the requested tool and decide
// whether to enqueue an approval. Execute must never be invoked for a tool
// the bundle marks as queue approval — when it is, that proves the canonical
// approval routing has been bypassed and the test must fail.
type integrationToolRegistry struct {
	defs        []llm.ToolDefinition
	executeHits int
}

func (r *integrationToolRegistry) Definitions() []llm.ToolDefinition { return r.defs }

func (r *integrationToolRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	out := make([]*llm.ToolDefinition, 0, len(r.defs))
	for i := range r.defs {
		def := r.defs[i]
		if matchToolPattern(pattern, def.Name) {
			out = append(out, &def)
		}
	}
	return out
}

func (r *integrationToolRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	for i := range r.defs {
		if r.defs[i].Name == name {
			def := r.defs[i]
			return &def, true
		}
	}
	return nil, false
}

func (r *integrationToolRegistry) MustHaveTools([]string) ([]llm.Tool, error) { return nil, nil }

func (r *integrationToolRegistry) Execute(context.Context, string, map[string]interface{}) (string, error) {
	r.executeHits++
	return "", nil
}

func (r *integrationToolRegistry) SetDebugLogger(io.Writer)   {}
func (r *integrationToolRegistry) Initialize(context.Context) {}

func matchToolPattern(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	if pattern == name {
		return true
	}
	const wildcard = ":*"
	if len(pattern) > len(wildcard) && pattern[len(pattern)-len(wildcard):] == wildcard {
		root := pattern[:len(pattern)-len(wildcard)]
		return len(name) > len(root) && name[:len(root)] == root && name[len(root)] == ':'
	}
	return false
}

// TestHandler_MCPUI_ToolCall_HTTPIntegrationQueuesApproval drives the full
// host-owned guest-tool path:
//
//   - HTTP route POST /v1/api/mcp-ui/tools/call is mounted from the SDK
//     handler's auto-discovery of ExecuteMCPUIToolCall on the backend
//   - JSON decoding of MCPUIToolCallInput happens inside handleMCPUIToolCall
//   - backendClient.ExecuteMCPUIToolCall dispatches into the real
//     agent.Service.RunGuestToolCall canonical path
//   - the canonical toolexec.ExecuteToolStep persists a real approval-queue
//     row through the memory conversation client (no mocks)
//   - the queued response payload carries the canonical approval id pulled
//     back from that same queue table
//
// If any of those links break, the test fails. The registry's Execute is
// asserted to stay untouched, proving the queue-gated tool was not silently
// executed.
func TestHandler_MCPUI_ToolCall_HTTPIntegrationQueuesApproval(t *testing.T) {
	ctx := context.Background()

	const (
		conversationID = "conv-mcpui-http-integration"
		bundleID       = "mcp_ui_preview_queue"
		toolName       = "system/os:getEnv"
		displayName    = "system/os/getEnv"
	)

	convClient := convmem.New()
	require.NoError(t, convClient.EnsureConversation(conversationID, func(c *apiconv.MutableConversation) {
		c.SetStatus("running")
	}))

	registry := &integrationToolRegistry{
		defs: []llm.ToolDefinition{{Name: toolName}},
	}
	var _ tool.Registry = registry

	bundles := []*toolbundle.Bundle{{
		ID: bundleID,
		Match: []llm.Tool{{
			Name:     "system/os:*",
			Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue},
		}},
	}}

	svc := agentsvc.New(
		nil, nil, nil,
		registry,
		nil,
		convClient,
		agentsvc.WithToolBundles(func(context.Context) ([]*toolbundle.Bundle, error) {
			return bundles, nil
		}),
	)

	backend, err := NewEmbedded(svc, convClient)
	require.NoError(t, err)

	handler := NewHandler(backend)

	body, err := json.Marshal(map[string]interface{}{
		"conversationId": conversationID,
		"toolName":       toolName,
		"arguments":      map[string]interface{}{"names": []string{"LOGNAME"}},
		"toolBundles":    []string{bundleID},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/api/mcp-ui/tools/call", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equalf(t, http.StatusOK, rec.Code, "unexpected HTTP status: body=%s", rec.Body.String())

	var out MCPUIToolCallOutput
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, conversationID, out.ConversationID)
	require.Equal(t, agentsvc.GuestToolStatusQueued, out.Status, "queued status proves the canonical approval queue path fired")
	require.NotEmpty(t, out.TurnID, "guest turn must be persisted by the canonical path")
	require.NotNil(t, out.Approval, "queued payload must carry the canonical PendingToolApproval row")
	require.NotEmpty(t, out.Approval.ID, "approval id must come from the persisted queue row")
	require.Equal(t, displayName, out.Approval.ToolName)
	require.Equal(t, "pending", out.Approval.Status)
	require.Equal(t, conversationID, out.Approval.ConversationID)
	require.Equal(t, out.TurnID, out.Approval.TurnID)
	require.Equal(t, 0, registry.executeHits, "queue-gated tool must not be executed by the registry")

	rows, err := convClient.ListToolApprovalQueues(ctx, &queueread.QueueRowsInput{
		ConversationId: conversationID,
		Has:            &queueread.QueueRowsInputHas{ConversationId: true},
	})
	require.NoError(t, err)
	require.Lenf(t, rows, 1, "queue writer must persist exactly one row through the real approval queue path")
	require.Equal(t, "pending", rows[0].Status, "persisted row must reflect pending state")
	require.Equal(t, out.Approval.ID, rows[0].Id, "queued payload approval id must match the persisted queue row id")
	require.NotNil(t, rows[0].ConversationId)
	require.Equal(t, conversationID, *rows[0].ConversationId)
	require.NotNil(t, rows[0].TurnId)
	require.Equal(t, out.TurnID, *rows[0].TurnId)
	require.Equal(t, displayName, rows[0].ToolName)
}

// TestHandler_MCPUI_ToolCall_HTTPIntegrationMissingConversationCreatesOne
// exercises the auto-conversation branch of ExecuteMCPUIToolCall: when the
// guest payload omits conversationId, the backend creates a conversation
// before running the canonical guest-tool path. This guards against a
// regression where the empty-conversation branch silently dropped the
// approval queue write.
func TestHandler_MCPUI_ToolCall_HTTPIntegrationMissingConversationCreatesOne(t *testing.T) {
	ctx := context.Background()

	const (
		bundleID    = "mcp_ui_preview_queue"
		toolName    = "system/os:getEnv"
		displayName = "system/os/getEnv"
	)

	convClient := convmem.New()

	registry := &integrationToolRegistry{
		defs: []llm.ToolDefinition{{Name: toolName}},
	}

	bundles := []*toolbundle.Bundle{{
		ID: bundleID,
		Match: []llm.Tool{{
			Name:     "system/os:*",
			Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue},
		}},
	}}

	svc := agentsvc.New(
		nil, nil, nil,
		registry,
		nil,
		convClient,
		agentsvc.WithToolBundles(func(context.Context) ([]*toolbundle.Bundle, error) {
			return bundles, nil
		}),
	)

	backend, err := NewEmbedded(svc, convClient)
	require.NoError(t, err)

	handler := NewHandler(backend)

	body, err := json.Marshal(map[string]interface{}{
		"toolName":    toolName,
		"arguments":   map[string]interface{}{"names": []string{"LOGNAME"}},
		"toolBundles": []string{bundleID},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/api/mcp-ui/tools/call", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equalf(t, http.StatusOK, rec.Code, "unexpected HTTP status: body=%s", rec.Body.String())

	var out MCPUIToolCallOutput
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.NotEmpty(t, out.ConversationID, "backend must auto-create a conversation when input omits one")
	require.Equal(t, agentsvc.GuestToolStatusQueued, out.Status)
	require.NotNil(t, out.Approval)
	require.Equal(t, displayName, out.Approval.ToolName)
	require.Equal(t, 0, registry.executeHits)

	rows, err := convClient.ListToolApprovalQueues(ctx, &queueread.QueueRowsInput{
		ConversationId: out.ConversationID,
		Has:            &queueread.QueueRowsInputHas{ConversationId: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, out.Approval.ID, rows[0].Id)
}
