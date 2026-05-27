package agent

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	queueread "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
)

func seedConversation(t *testing.T, client *convmem.Client, conversationID string) {
	t.Helper()
	conv := apiconv.NewConversation()
	conv.SetId(conversationID)
	conv.SetStatus("running")
	require.NoError(t, client.PatchConversations(context.Background(), conv))
}

// recordingExecRegistry remembers whether Execute was invoked. The MCP UI
// guest tool call must short-circuit on the canonical approval queue path
// when the resolved bundle marks the tool as queue approval — Execute must
// not be reached.
type recordingExecRegistry struct {
	fakeRegistry
	executed bool
	result   string
	err      error
}

func (r *recordingExecRegistry) Execute(_ context.Context, _ string, _ map[string]interface{}) (string, error) {
	r.executed = true
	if r.err != nil {
		return "", r.err
	}
	return r.result, nil
}
func (r *recordingExecRegistry) SetDebugLogger(io.Writer)                 {}
func (r *recordingExecRegistry) Initialize(context.Context)               {}
func (r *recordingExecRegistry) ToolTimeout(string) (time.Duration, bool) { return 0, false }

func TestRunGuestToolCall_TableDriven(t *testing.T) {
	const conversationID = "conv-mcpui-guest"

	approvalCfg := func(mode llm.ApprovalMode, behavior llm.ApprovalQueueBehavior) *llm.ApprovalConfig {
		return &llm.ApprovalConfig{Mode: mode, QueueBehavior: behavior}
	}

	testCases := []struct {
		name           string
		input          *GuestToolCallInput
		registryResult string
		registryErr    error
		bundles        []*toolbundle.Bundle
		expectStatus   string
		expectResult   string
		expectExecuted bool
		expectErr      string
		expectQueueRow bool
	}{
		{
			name: "no_bundle_no_approval_executes_tool_directly",
			input: &GuestToolCallInput{
				ConversationID: conversationID,
				ToolName:       "demo:show_widget",
				Arguments:      map[string]interface{}{"title": "Hi"},
			},
			registryResult: `{"ok":true}`,
			expectStatus:   GuestToolStatusOK,
			expectResult:   `{"ok":true}`,
			expectExecuted: true,
		},
		{
			name: "bundle_with_queue_approval_queues_without_execution",
			input: &GuestToolCallInput{
				ConversationID: conversationID,
				ToolName:       "system/os:getEnv",
				Arguments:      map[string]interface{}{"names": []string{"PATH"}},
				ToolBundles:    []string{"mcp_ui_preview_queue"},
			},
			bundles: []*toolbundle.Bundle{{
				ID: "mcp_ui_preview_queue",
				Match: []llm.Tool{{
					Name:     "system/os:*",
					Approval: approvalCfg(llm.ApprovalModeQueue, llm.ApprovalQueueBehaviorDetach),
				}},
			}},
			registryResult: `unreachable`,
			expectStatus:   GuestToolStatusQueued,
			expectResult:   "queued for user approval",
			expectExecuted: false,
			expectQueueRow: true,
		},
		{
			name: "bundle_without_approval_executes_through_canonical_path",
			input: &GuestToolCallInput{
				ConversationID: conversationID,
				ToolName:       "system/os:getEnv",
				Arguments:      map[string]interface{}{"names": []string{"PATH"}},
				ToolBundles:    []string{"mcp_ui_preview_open"},
			},
			bundles: []*toolbundle.Bundle{{
				ID:    "mcp_ui_preview_open",
				Match: []llm.Tool{{Name: "system/os:*"}},
			}},
			registryResult: `{"values":{"PATH":"/usr/bin"}}`,
			expectStatus:   GuestToolStatusOK,
			expectResult:   `{"values":{"PATH":"/usr/bin"}}`,
			expectExecuted: true,
		},
		{
			name: "tool_not_in_selected_bundle_returns_error",
			input: &GuestToolCallInput{
				ConversationID: conversationID,
				ToolName:       "system/exec:execute",
				ToolBundles:    []string{"mcp_ui_preview_open"},
			},
			bundles: []*toolbundle.Bundle{{
				ID:    "mcp_ui_preview_open",
				Match: []llm.Tool{{Name: "system/os:*"}},
			}},
			expectErr: "not exposed by bundle",
		},
		{
			name:      "missing_input_returns_error",
			input:     nil,
			expectErr: "input is required",
		},
		{
			name: "missing_conversation_id_returns_error",
			input: &GuestToolCallInput{
				ToolName: "demo:show_widget",
			},
			expectErr: "conversation id is required",
		},
		{
			name: "missing_tool_name_returns_error",
			input: &GuestToolCallInput{
				ConversationID: conversationID,
			},
			expectErr: "tool name is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registry := &recordingExecRegistry{
				fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{
					{Name: "demo:show_widget"},
					{Name: "system/os:getEnv"},
					{Name: "system/exec:execute"},
				}},
				result: tc.registryResult,
				err:    tc.registryErr,
			}
			convClient := convmem.New()
			if tc.input != nil && tc.input.ConversationID != "" {
				seedConversation(t, convClient, tc.input.ConversationID)
			}
			svc := &Service{
				registry:     registry,
				conversation: convClient,
				toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
					return tc.bundles, nil
				},
			}

			out, err := svc.RunGuestToolCall(context.Background(), tc.input)
			if tc.expectErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, out)
			require.Equal(t, tc.expectStatus, out.Status, "status")
			require.Equal(t, GuestToolSourceUI, out.Source, "guest source label")
			require.Equal(t, tc.expectExecuted, registry.executed, "registry.Execute invocation expectation")
			require.NotEmpty(t, out.TurnID, "guest turn must be persisted")
			if tc.expectResult != "" {
				require.Equal(t, tc.expectResult, out.Result, "result text")
			}
			if tc.expectQueueRow {
				rows, listErr := convClient.ListToolApprovalQueues(context.Background(), &queueread.QueueRowsInput{
					ConversationId: conversationID,
					Has:            &queueread.QueueRowsInputHas{ConversationId: true},
				})
				require.NoError(t, listErr)
				require.Len(t, rows, 1, "queue row must be written through real approval queue path")
				require.Equal(t, "pending", rows[0].Status)
				require.NotNil(t, rows[0].Metadata)
				var metadata map[string]interface{}
				require.NoError(t, json.Unmarshal(*rows[0].Metadata, &metadata))
				require.Equal(t, GuestToolSourceUI, metadata["source"])
			}
		})
	}
}

func TestRunGuestToolCall_RequiresWiredService(t *testing.T) {
	svc := &Service{}
	_, err := svc.RunGuestToolCall(context.Background(), &GuestToolCallInput{
		ConversationID: "conv",
		ToolName:       "demo:show_widget",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires registry and conversation client")
}

func TestRunGuestToolCall_ContextSnapshotPopulatesApprovalState(t *testing.T) {
	registry := &recordingExecRegistry{
		fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "system/os:getEnv"}}},
		result:       `{"values":{}}`,
	}
	convClient := convmem.New()
	seedConversation(t, convClient, "conv-snap")
	svc := &Service{
		registry:     registry,
		conversation: convClient,
		toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{{
				ID: "mcp_ui_preview_queue",
				Match: []llm.Tool{{
					Name: "system/os:*",
					Approval: &llm.ApprovalConfig{
						Mode: llm.ApprovalModeQueue,
					},
				}},
			}}, nil
		},
	}

	out, err := svc.RunGuestToolCall(context.Background(), &GuestToolCallInput{
		ConversationID: "conv-snap",
		ToolName:       "system/os:getEnv",
		Arguments:      map[string]interface{}{"names": []string{"PATH"}},
		ToolBundles:    []string{"mcp_ui_preview_queue"},
	})

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, GuestToolStatusQueued, out.Status)
	require.False(t, registry.executed, "approval-gated tool must not call Execute on the registry")
}

// TestRunGuestToolCall_BundleApprovalIsAppliedFromContextState exercises the
// guarantee that bundle approval metadata reaches the approval queue state
// in the same context the canonical execution path reads. If this ever
// regresses, ExecuteToolStep would silently bypass the queue path.
func TestRunGuestToolCall_BundleApprovalIsAppliedFromContextState(t *testing.T) {
	svc := &Service{
		registry: &fakeRegistry{defs: []llm.ToolDefinition{{Name: "system/os:getEnv"}}},
		toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{{
				ID: "mcp_ui_preview_queue",
				Match: []llm.Tool{{
					Name:     "system/os:*",
					Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue},
				}},
			}}, nil
		},
	}
	ctx := toolapprovalqueue.WithState(context.Background())
	entry, err := svc.resolveBundleResult(ctx, []string{"mcp_ui_preview_queue"})
	require.NoError(t, err)
	svc.applyResolvedToolSurfaceMetadata(ctx, entry)

	cfg, ok := toolapprovalqueue.ConfigFor(ctx, "system/os:getEnv")
	require.True(t, ok, "expected bundle approval config to be applied to ctx state")
	require.NotNil(t, cfg)
	require.True(t, cfg.IsQueue(), "tool must be marked as queue approval")
}
