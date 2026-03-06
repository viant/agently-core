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
