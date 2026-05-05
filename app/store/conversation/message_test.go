package conversation

import (
	"bytes"
	"compress/gzip"
	"testing"

	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

func gzipString(t *testing.T, value string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(value)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.String()
}

func TestMessage_ToolCallArguments_DecodesCompressedRequestPayload(t *testing.T) {
	payload := gzipString(t, `{"name":"capacity-review","args":"WorkspaceId=7268995"}`)
	msg := &Message{
		ToolMessage: []*agconv.ToolMessageView{{
			ToolCall: &agconv.ToolCallView{
				RequestPayload: &agconv.ModelCallStreamPayloadView{
					InlineBody:  &payload,
					Compression: "gzip",
				},
			},
		}},
	}

	got := msg.ToolCallArguments()
	if got["name"] != "capacity-review" {
		t.Fatalf("name = %#v, want capacity-review", got["name"])
	}
	if got["args"] != "WorkspaceId=7268995" {
		t.Fatalf("args = %#v, want WorkspaceId=7268995", got["args"])
	}
}
