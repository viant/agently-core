package toolexec

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	queueread "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queuew "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// stubQueueConv captures the approval-queue row that enqueueToolApproval writes.
type stubQueueConv struct {
	stubConv
	rec *queuew.ToolApprovalQueue
}

func (s *stubQueueConv) PatchToolApprovalQueue(_ context.Context, rec *queuew.ToolApprovalQueue) error {
	s.rec = rec
	return nil
}

func (s *stubQueueConv) ListToolApprovalQueues(context.Context, *queueread.QueueRowsInput) ([]*queueread.QueueRowView, error) {
	return nil, nil
}

var _ apiconv.Client = (*stubQueueConv)(nil)

// TestEnqueueToolApproval_SourceLabeling exercises the canonical guest-UI
// labeling rule end to end against the approval-queue write path:
//
//   - when toolapprovalqueue.MarkSource(ctx, "guest_ui") is called (the MCP UI
//     host-mediated path), the persisted metadata MUST carry source=guest_ui.
//   - when no source is marked (every other tool-execution path), the metadata
//     MUST NOT carry a source key — the non-MCP-UI contract stays unchanged.
func TestEnqueueToolApproval_SourceLabeling(t *testing.T) {
	cases := []struct {
		name          string
		markSource    string
		wantSourceKey bool
		wantSourceVal string
	}{
		{
			name:          "guest_ui path is labeled",
			markSource:    "guest_ui",
			wantSourceKey: true,
			wantSourceVal: "guest_ui",
		},
		{
			name:          "non guest path is unchanged",
			markSource:    "",
			wantSourceKey: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := toolapprovalqueue.WithState(context.Background())
			toolapprovalqueue.MarkTool(ctx, "system/os:getEnv", &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue})
			if tc.markSource != "" {
				toolapprovalqueue.MarkSource(ctx, tc.markSource)
			}
			ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
				ConversationID:  "conv-source-label",
				TurnID:          "turn-source-label",
				ParentMessageID: "msg-source-label",
			})
			conv := &stubQueueConv{}
			step := StepInfo{
				ID:         "op-source-label",
				Name:       "system/os:getEnv",
				Args:       map[string]interface{}{"names": []string{"PATH"}},
				ResponseID: "resp-source-label",
			}
			result, err := enqueueToolApproval(ctx, conv, step)
			require.NoError(t, err)
			require.Equal(t, "queued for user approval", result)
			require.NotNil(t, conv.rec)
			require.NotNil(t, conv.rec.Metadata)

			var metadata map[string]interface{}
			require.NoError(t, json.Unmarshal(*conv.rec.Metadata, &metadata))
			if tc.wantSourceKey {
				got, ok := metadata["source"]
				require.True(t, ok, "expected source key in approval metadata for guest path")
				require.Equal(t, tc.wantSourceVal, got)
			} else {
				_, ok := metadata["source"]
				require.False(t, ok, "non-MCP-UI path must not introduce a source key in approval metadata")
			}
		})
	}
}
