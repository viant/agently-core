package recovery

import (
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	model "github.com/viant/agently-core/model/conversation"
	"testing"
	"time"
)

func textPtr(s string) *string { return &s }
func TestEligibleCompactionStateProtectsLatestSummaryAndPending(t *testing.T) {
	conv := &apiconv.Conversation{Id: "conv", Transcript: []*model.TranscriptView{{Message: []*model.MessageView{
		{Id: "old", ConversationId: "conv", Role: "user", Content: textPtr("old facts")},
		{Id: "latest", ConversationId: "conv", Role: "user", Content: textPtr("current request")},
		{Id: "summary", ConversationId: "conv", Role: "assistant", Status: textPtr("summary"), Content: textPtr("summary")},
		{Id: "pending", ConversationId: "conv", Role: "assistant", Type: "tool_op", ToolMessage: []*model.ToolMessageView{{ToolCall: &model.ToolCallView{OpId: "pending", Status: "running"}}}},
		{Id: "recovery", ConversationId: "conv", Role: "assistant", ToolMessage: []*model.ToolMessageView{{ToolCall: &model.ToolCallView{Status: "succeeded", ToolName: "message-remove"}}}},
		{Id: "other", ConversationId: "other", Role: "assistant", Content: textPtr("other")},
	}}}}
	eligible := EligibleMessages(conv)
	require.Len(t, eligible, 1)
	require.Contains(t, eligible, "old")
	signature := EligibleSignature(eligible)
	conv.Transcript[0].Message[1].Content = textPtr("changed latest prompt")
	conv.Transcript[0].Message[2].Content = textPtr("new summary")
	require.Equal(t, signature, EligibleSignature(EligibleMessages(conv)))
	conv.Transcript[0].Message = append(conv.Transcript[0].Message, &model.MessageView{Id: "new-completed", ConversationId: "conv", Role: "assistant", Content: textPtr("new completed facts")})
	require.NotEqual(t, signature, EligibleSignature(EligibleMessages(conv)))
}

func TestDurableCompactionBarrierRequiresSuccessfulNewFullHistory(t *testing.T) {
	now := time.Now()
	marker, full := ProactiveSummaryMarker, FullHistoryResponseMarker
	trace := "fresh-response"
	started := now.Add(time.Second)
	call := &model.ModelCallView{Status: "failed", StartedAt: &started, TraceId: &trace}
	conv := &apiconv.Conversation{Transcript: []*model.TranscriptView{{Message: []*model.MessageView{
		{CreatedAt: now, ContextSummary: &marker},
		{CreatedAt: started, ContextSummary: &full, ModelCall: call},
	}}}}
	require.True(t, NeedsFullHistory(conv))
	call.Status = "completed"
	require.False(t, NeedsFullHistory(conv))
	started = now.Add(-time.Second) // An old call finishing later cannot clear it.
	require.True(t, NeedsFullHistory(conv))
	started = now.Add(time.Second)
	call.TraceId = nil
	require.True(t, NeedsFullHistory(conv))
}
