package data

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	agmessagewrite "github.com/viant/agently-core/pkg/agently/message/write"
	agmodelcallwrite "github.com/viant/agently-core/pkg/agently/modelcall/write"
	agpayloadwrite "github.com/viant/agently-core/pkg/agently/payload/write"
	agtoolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
)

const payloadDiagnosticsEnv = "AGENTLY_PAYLOAD_DIAGNOSTICS"

var payloadDiagnosticsOnce struct {
	sync.Once
	enabled bool
}

func payloadDiagnosticsEnabled() bool {
	payloadDiagnosticsOnce.Do(func() {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(payloadDiagnosticsEnv))) {
		case "1", "true", "yes", "on":
			payloadDiagnosticsOnce.enabled = true
		}
	})
	return payloadDiagnosticsOnce.enabled
}

func payloadDiagf(format string, args ...interface{}) {
	if !payloadDiagnosticsEnabled() {
		return
	}
	log.Printf("[payload-diagnostics] "+format, args...)
}

func payloadDiagPatchPayloads(phase string, rows []*agpayloadwrite.MutablePayloadView, err error) {
	if !payloadDiagnosticsEnabled() {
		return
	}
	if err != nil {
		payloadDiagf("patch_payloads phase=%s rows=%d err=%v caller=%s", phase, len(rows), err, payloadDiagCaller())
		return
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		bodyLen, bodySha, preview := payloadDiagBody(row)
		payloadDiagf("patch_payload phase=%s id=%q kind=%q subtype=%q mime=%q size=%d storage=%q compression=%q body_len=%d body_sha=%q preview=%q caller=%s",
			phase,
			row.Id,
			row.Kind,
			ptrString(row.Subtype),
			row.MimeType,
			row.SizeBytes,
			row.Storage,
			row.Compression,
			bodyLen,
			bodySha,
			preview,
			payloadDiagCaller(),
		)
	}
}

func payloadDiagPatchMessages(phase string, rows []*agmessagewrite.MutableMessageView, err error) {
	if !payloadDiagnosticsEnabled() {
		return
	}
	if err != nil {
		payloadDiagf("patch_messages phase=%s rows=%d err=%v caller=%s", phase, len(rows), err, payloadDiagCaller())
		return
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		hasAttachment := row.Has != nil && row.Has.AttachmentPayloadID
		hasElicitation := row.Has != nil && row.Has.ElicitationPayloadID
		if !hasAttachment && !hasElicitation && row.AttachmentPayloadID == nil && row.ElicitationPayloadID == nil {
			continue
		}
		payloadDiagf("patch_message phase=%s id=%q conversation=%q turn=%q role=%q type=%q status=%q attachment_payload_id=%q attachment_has=%t elicitation_payload_id=%q elicitation_has=%t content_preview=%q caller=%s",
			phase,
			row.Id,
			row.ConversationID,
			ptrString(row.TurnID),
			row.Role,
			row.Type,
			ptrString(row.Status),
			ptrString(row.AttachmentPayloadID),
			hasAttachment,
			ptrString(row.ElicitationPayloadID),
			hasElicitation,
			payloadDiagPreviewString(ptrString(row.Content), 220),
			payloadDiagCaller(),
		)
	}
}

func payloadDiagPatchToolCalls(phase string, rows []*agtoolcallwrite.MutableToolCallView, err error) {
	if !payloadDiagnosticsEnabled() {
		return
	}
	if err != nil {
		payloadDiagf("patch_tool_calls phase=%s rows=%d err=%v caller=%s", phase, len(rows), err, payloadDiagCaller())
		return
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		hasRequest := row.Has != nil && row.Has.RequestPayloadID
		hasResponse := row.Has != nil && row.Has.ResponsePayloadID
		if !hasRequest && !hasResponse && row.RequestPayloadID == nil && row.ResponsePayloadID == nil {
			continue
		}
		payloadDiagf("patch_tool_call phase=%s message=%q turn=%q op=%q tool=%q status=%q request_payload_id=%q request_has=%t response_payload_id=%q response_has=%t caller=%s",
			phase,
			row.MessageID,
			ptrString(row.TurnID),
			row.OpID,
			row.ToolName,
			row.Status,
			ptrString(row.RequestPayloadID),
			hasRequest,
			ptrString(row.ResponsePayloadID),
			hasResponse,
			payloadDiagCaller(),
		)
	}
}

func payloadDiagPatchModelCalls(phase string, rows []*agmodelcallwrite.MutableModelCallView, err error) {
	if !payloadDiagnosticsEnabled() {
		return
	}
	if err != nil {
		payloadDiagf("patch_model_calls phase=%s rows=%d err=%v caller=%s", phase, len(rows), err, payloadDiagCaller())
		return
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		hasRequest := row.Has != nil && row.Has.RequestPayloadID
		hasResponse := row.Has != nil && row.Has.ResponsePayloadID
		hasProviderRequest := row.Has != nil && row.Has.ProviderRequestPayloadID
		hasProviderResponse := row.Has != nil && row.Has.ProviderResponsePayloadID
		hasStream := row.Has != nil && row.Has.StreamPayloadID
		if !hasRequest && !hasResponse && !hasProviderRequest && !hasProviderResponse && !hasStream &&
			row.RequestPayloadID == nil && row.ResponsePayloadID == nil && row.ProviderRequestPayloadID == nil && row.ProviderResponsePayloadID == nil && row.StreamPayloadID == nil {
			continue
		}
		payloadDiagf("patch_model_call phase=%s message=%q turn=%q provider=%q model=%q status=%q request_payload_id=%q request_has=%t response_payload_id=%q response_has=%t provider_request_payload_id=%q provider_request_has=%t provider_response_payload_id=%q provider_response_has=%t stream_payload_id=%q stream_has=%t caller=%s",
			phase,
			row.MessageID,
			ptrString(row.TurnID),
			row.Provider,
			row.Model,
			row.Status,
			ptrString(row.RequestPayloadID),
			hasRequest,
			ptrString(row.ResponsePayloadID),
			hasResponse,
			ptrString(row.ProviderRequestPayloadID),
			hasProviderRequest,
			ptrString(row.ProviderResponsePayloadID),
			hasProviderResponse,
			ptrString(row.StreamPayloadID),
			hasStream,
			payloadDiagCaller(),
		)
	}
}

func payloadDiagBody(row *agpayloadwrite.MutablePayloadView) (int, string, string) {
	if row == nil || row.InlineBody == nil {
		return 0, "", ""
	}
	body := *row.InlineBody
	hash := sha256.Sum256(body)
	previewBytes := body
	if strings.EqualFold(strings.TrimSpace(row.Compression), "gzip") {
		if decoded, err := gunzipBytes(body); err == nil {
			previewBytes = decoded
		}
	}
	return len(body), hex.EncodeToString(hash[:8]), payloadDiagPreviewString(string(previewBytes), 360)
}

func gunzipBytes(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func payloadDiagCaller() string {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	parts := make([]string, 0, 4)
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.Function, "app/store/data.") &&
			!strings.Contains(frame.Function, "runtime.") &&
			!strings.Contains(frame.Function, "log.") {
			parts = append(parts, fmt.Sprintf("%s:%d", shortPath(frame.File), frame.Line))
			if len(parts) == 4 {
				break
			}
		}
		if !more {
			break
		}
	}
	return strings.Join(parts, " <- ")
}

func shortPath(path string) string {
	const marker = "github.com/viant/agently-core/"
	if idx := strings.Index(path, marker); idx >= 0 {
		return path[idx+len(marker):]
	}
	parts := strings.Split(path, "/")
	if len(parts) <= 4 {
		return path
	}
	return strings.Join(parts[len(parts)-4:], "/")
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func payloadDiagPreviewString(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
	if value == "" {
		return ""
	}
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "...(truncated)"
}
