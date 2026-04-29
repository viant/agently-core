package conversation

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/assert"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

func TestMessage_GetContent(t *testing.T) {
	toolBody := "tool response"
	rawBody := "original user"
	expanded := "expanded user"
	cases := []struct {
		name     string
		message  *Message
		expected string
	}{
		{
			name:     "tool response preferred",
			message:  &Message{ToolMessage: []*agconv.ToolMessageView{{ToolCall: &agconv.ToolCallView{ResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: ptr("tool response")}}}}},
			expected: toolBody,
		},
		{
			name: "gzip tool response preferred",
			message: &Message{ToolMessage: []*agconv.ToolMessageView{{
				ToolCall: &agconv.ToolCallView{
					ResponsePayload: &agconv.ModelCallStreamPayloadView{
						InlineBody:  ptr(gzipString(t, toolBody)),
						Compression: "gzip",
					},
				},
			}}},
			expected: toolBody,
		},
		{
			name:     "raw content fallback",
			message:  &Message{RawContent: ptr(rawBody), Content: ptr(expanded)},
			expected: rawBody,
		},
		{
			name:     "content used when raw missing",
			message:  &Message{Content: ptr(expanded)},
			expected: expanded,
		},
		{
			name:     "empty message returns empty string",
			message:  &Message{},
			expected: "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.message.GetContent())
		})
	}
}

func TestMessage_GetContentPreferContent(t *testing.T) {
	toolBody := "tool response"
	finalBody := "final persisted tool message"
	cases := []struct {
		name     string
		message  *Message
		expected string
	}{
		{
			name: "message content overrides stale tool payload",
			message: &Message{
				Content: ptr(finalBody),
				ToolMessage: []*agconv.ToolMessageView{{
					ToolCall: &agconv.ToolCallView{
						ResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: ptr(toolBody)},
					},
				}},
			},
			expected: finalBody,
		},
		{
			name: "tool payload still used when message content missing",
			message: &Message{
				ToolMessage: []*agconv.ToolMessageView{{
					ToolCall: &agconv.ToolCallView{
						ResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: ptr(toolBody)},
					},
				}},
			},
			expected: toolBody,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.message.GetContentPreferContent())
		})
	}
}

func TestMessage_FirstToolCall_PrefersDirectToolCall(t *testing.T) {
	message := &Message{
		MessageToolCall: &agconv.MessageToolCallView{
			OpId:     "async-status:child-1",
			ToolName: "llm/agents/status",
		},
		ToolMessage: []*agconv.ToolMessageView{
			{
				ToolCall: &agconv.ToolCallView{
					OpId:     "wrapped-op",
					ToolName: "message/add",
				},
			},
		},
	}

	got := message.firstToolCall()
	if got == nil {
		t.Fatalf("expected direct tool call metadata")
	}
	if got.OpId != "async-status:child-1" {
		t.Fatalf("expected direct tool call op id, got %q", got.OpId)
	}
}

func ptr(v string) *string { return &v }

func gzipString(t *testing.T, value string) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, err := writer.Write([]byte(value))
	if err != nil {
		t.Fatalf("gzip write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close failed: %v", err)
	}
	return buffer.String()
}
