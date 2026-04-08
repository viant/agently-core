package toolexec

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
)

// persistRequestPayload stores tool request arguments and links them to the tool call.
func persistRequestPayload(ctx context.Context, conv apiconv.Client, toolMsgID string, args map[string]interface{}) (string, error) {
	b, mErr := json.Marshal(args)
	if mErr != nil {
		return "", mErr
	}
	reqID, pErr := createInlinePayload(ctx, conv, "tool_request", "application/json", b)
	if pErr != nil {
		return "", pErr
	}
	upd := apiconv.NewToolCall()
	upd.SetMessageID(toolMsgID)
	upd.RequestPayloadID = &reqID
	upd.Has.RequestPayloadID = true
	_ = conv.PatchToolCall(ctx, upd)
	return reqID, nil
}

// persistResponsePayload stores tool response content and returns the payload ID.
func persistResponsePayload(ctx context.Context, conv apiconv.Client, result string) (string, error) {
	rb := []byte(normalizeToolResponseBody(result))
	return createInlinePayload(ctx, conv, "tool_response", "text/plain", rb)
}

type toolPayloadWrapper struct {
	InlineBody  string `json:"InlineBody"`
	Compression string `json:"Compression"`
}

func normalizeToolResponseBody(result string) string {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return ""
	}
	if decoded, ok := decodeWrappedToolResponse(trimmed); ok {
		if decoded != "" {
			return decoded
		}
		return "tool response payload could not be decoded"
	}
	return trimmed
}

func decodeWrappedToolResponse(result string) (string, bool) {
	var wrapper toolPayloadWrapper
	if err := json.Unmarshal([]byte(result), &wrapper); err != nil {
		return "", false
	}
	if strings.TrimSpace(wrapper.InlineBody) == "" && strings.TrimSpace(wrapper.Compression) == "" {
		return "", false
	}
	if strings.TrimSpace(wrapper.InlineBody) == "" {
		return "", true
	}
	return decodeInlineToolBody(wrapper.InlineBody, wrapper.Compression), true
}

func decodeInlineToolBody(body string, compression string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	compressed := strings.EqualFold(strings.TrimSpace(compression), "gzip") || looksLikeGzipBytes(body)
	if compressed {
		reader, err := gzip.NewReader(bytes.NewReader([]byte(body)))
		if err != nil {
			return ""
		}
		defer reader.Close()
		decoded, err := io.ReadAll(reader)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(decoded))
	}
	return strings.TrimSpace(body)
}

func looksLikeGzipBytes(body string) bool {
	if len(body) < 2 {
		return false
	}
	return body[0] == 0x1f && body[1] == 0x8b
}

// createInlinePayload creates and persists an inline payload and returns its ID.
func createInlinePayload(ctx context.Context, conv apiconv.Client, kind, mime string, body []byte) (string, error) {
	pid := uuid.New().String()
	p := apiconv.NewPayload()
	p.SetId(pid)
	p.SetKind(kind)
	p.SetMimeType(mime)
	p.SetSizeBytes(len(body))
	p.SetStorage("inline")
	p.SetInlineBody(body)
	if err := conv.PatchPayload(ctx, p); err != nil {
		return "", err
	}
	return pid, nil
}
