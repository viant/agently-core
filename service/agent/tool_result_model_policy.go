package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/viant/agently-core/genai/llm/provider"
)

// This is the historical tool-result presentation bound, used only when an
// explicitly selected model enables continuation-format presentation. It does
// not configure general user/assistant history preview or provider wire output.
const enabledContinuationToolResultLimit = 8 * 1024

type toolResultPreviewConfigFinder interface{ ConfigByIDOrModel(string) *provider.Config }

func (s *Service) selectedModelToolResultLimit(input *QueryInput) int {
	if s == nil || s.llm == nil || input == nil {
		return 0
	}
	modelID := strings.TrimSpace(input.ModelOverride)
	if modelID == "" && input.Agent != nil {
		modelID = strings.TrimSpace(input.Agent.Model)
	}
	if modelID == "" {
		return 0
	}
	finder, ok := s.llm.ModelFinder().(toolResultPreviewConfigFinder)
	if !ok {
		return 0
	}
	cfg := finder.ConfigByIDOrModel(modelID)
	// A provider-model alias must not accidentally opt in through another named
	// configuration with the same backend model and different presentation flags.
	if cfg == nil || !strings.EqualFold(strings.TrimSpace(cfg.ID), modelID) || !cfg.Options.EnableContinuationFormat {
		return 0
	}
	if cfg.Options.ToolResultPreviewLimit > 0 {
		return cfg.Options.ToolResultPreviewLimit
	}
	return enabledContinuationToolResultLimit
}

func stricterToolResultLimit(existing, modelLimit int) int {
	if modelLimit > 0 && (existing <= 0 || modelLimit < existing) {
		return modelLimit
	}
	return existing
}

// presentRecordedToolResult operates on the result already captured in the
// transcript. Prepared native pages and artifact descriptors are immutable
// references, not raw results to expand or truncate a second time.
func (s *Service) presentRecordedToolResult(ctx context.Context, input *QueryInput, body, messageID string, limit int, allowContinuation bool) (string, bool) {
	if limit <= 0 || body == "" {
		return body, false
	}
	if capturedArtifactDescriptors(body) {
		return body, false
	}
	if page, ok := strictNativeToolPage(body); ok {
		if len(page.body) <= limit {
			return body, allowContinuation
		}
		returned := boundedUTF8Prefix(page.body, limit)
		count := int64(len(returned))
		sourceOffset := page.nextOffset - page.returned
		remaining := page.remaining + page.returned - count
		if !allowContinuation {
			return returned, false
		}
		return nativeToolPage(returned, page.messageID, sourceOffset, count, remaining, limit), true
	}
	if len(body) <= limit {
		return body, false
	}
	// Arbitrary MCP result fields are data, not trusted presentation provenance.
	// Wrap bounded bytes of the actual recorded result with its actual source ID.
	returned := boundedUTF8Prefix(body, limit)
	if !allowContinuation {
		return returned, false
	}
	return nativeToolPage(returned, messageID, 0, int64(len(returned)), int64(len(body)-len(returned)), limit), true
}

// The exact metadata-only shape is emitted by configured binary-output capture
// before persistence. Unknown fields (including data/blob/body) cannot exempt a
// raw tool result from its configured presentation limit.
func capturedArtifactDescriptors(body string) bool {
	if !uniqueJSONKeys(body) {
		return false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &root) != nil || len(root) != 1 {
		return false
	}
	raw, ok := root["resources"]
	if !ok {
		return false
	}
	var values []map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 || len(values) > 32 {
		return false
	}
	allowed := map[string]bool{"id": true, "uri": true, "name": true, "mimeType": true, "sizeBytes": true, "sha256": true, "downloadURI": true}
	for _, value := range values {
		for key := range value {
			if !allowed[key] {
				return false
			}
		}
		var id, uri, name, mime, digest, download string
		var size int64
		if json.Unmarshal(value["id"], &id) != nil || id == "" || len(id) > 128 || strings.ContainsAny(id, "/\\?#") {
			return false
		}
		if json.Unmarshal(value["uri"], &uri) != nil || uri != "scratchpad://artifact/"+id {
			return false
		}
		if json.Unmarshal(value["name"], &name) != nil || len(name) > 255 {
			return false
		}
		if json.Unmarshal(value["mimeType"], &mime) != nil || mime == "" || len(mime) > 256 {
			return false
		}
		if json.Unmarshal(value["sizeBytes"], &size) != nil || size <= 0 {
			return false
		}
		if json.Unmarshal(value["sha256"], &digest) != nil || len(digest) != 64 {
			return false
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return false
		}
		if json.Unmarshal(value["downloadURI"], &download) != nil || download != "/v1/artifacts/"+id {
			return false
		}
	}
	return true
}

type nativeRecordedToolPage struct {
	body, messageID                 string
	returned, remaining, nextOffset int64
}

func boundedUTF8Prefix(body string, limit int) string {
	if len(body) <= limit {
		return body
	}
	prefix := body[:limit]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}

func nativeToolPage(body, messageID string, sourceOffset, returned, remaining int64, limit int) string {
	length := int64(limit)
	if remaining < length {
		length = remaining
	}
	offset := sourceOffset + returned
	frame := map[string]any{"body": body, "messageId": messageID, "returned": returned, "remaining": remaining, "continuation": map[string]any{"hasMore": remaining > 0, "returned": returned, "remaining": remaining, "mode": "head", "nextRange": map[string]any{"bytes": map[string]any{"offset": offset, "length": length}}}, "nextArgs": map[string]any{"messageId": messageID, "byteRange": map[string]any{"from": offset, "to": offset + length}}, "continuationHint": fmt.Sprintf("Call message-show with messageId=%s and byteRange.from=%d, byteRange.to=%d.", messageID, offset, offset+length)}
	raw, _ := json.Marshal(frame)
	return string(raw)
}

func strictNativeToolPage(body string) (nativeRecordedToolPage, bool) {
	var page nativeRecordedToolPage
	if !uniqueJSONKeys(body) {
		return page, false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &root) != nil {
		return page, false
	}
	allowed := map[string]bool{"body": true, "messageId": true, "returned": true, "remaining": true, "continuation": true, "nextArgs": true, "continuationHint": true}
	for key := range root {
		if !allowed[key] {
			return page, false
		}
	}
	if json.Unmarshal(root["body"], &page.body) != nil || json.Unmarshal(root["messageId"], &page.messageID) != nil || page.messageID == "" || len(page.messageID) > 128 {
		return page, false
	}
	if json.Unmarshal(root["returned"], &page.returned) != nil || page.returned != int64(len(page.body)) || page.returned <= 0 {
		return page, false
	}
	if json.Unmarshal(root["remaining"], &page.remaining) != nil || page.remaining <= 0 || page.remaining > math.MaxInt64-page.returned {
		return page, false
	}
	if raw, ok := root["continuationHint"]; ok {
		var hint string
		if json.Unmarshal(raw, &hint) != nil || len(hint) > 1024 {
			return page, false
		}
	}
	var cont map[string]json.RawMessage
	if json.Unmarshal(root["continuation"], &cont) != nil {
		return page, false
	}
	allowed = map[string]bool{"hasMore": true, "returned": true, "remaining": true, "nextRange": true, "mode": true}
	for key := range cont {
		if !allowed[key] {
			return page, false
		}
	}
	var hasMore bool
	if json.Unmarshal(cont["hasMore"], &hasMore) != nil || !hasMore {
		return page, false
	}
	for key, want := range map[string]int64{"returned": page.returned, "remaining": page.remaining} {
		if raw, ok := cont[key]; ok {
			var v int64
			if json.Unmarshal(raw, &v) != nil || v != want {
				return page, false
			}
		}
	}
	if raw, ok := cont["mode"]; ok {
		var mode string
		if json.Unmarshal(raw, &mode) != nil || mode != "head" {
			return page, false
		}
	}
	var ranges map[string]json.RawMessage
	if json.Unmarshal(cont["nextRange"], &ranges) != nil || len(ranges) != 1 {
		return page, false
	}
	var interval struct {
		Offset int64 `json:"offset"`
		Length int64 `json:"length"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(ranges["bytes"])))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&interval) != nil || interval.Offset < page.returned || interval.Length <= 0 || interval.Length > page.remaining || interval.Offset > math.MaxInt64-interval.Length {
		return page, false
	}
	if interval.Offset > math.MaxInt64-page.remaining {
		return page, false
	}
	page.nextOffset = interval.Offset
	var next map[string]json.RawMessage
	if json.Unmarshal(root["nextArgs"], &next) != nil || len(next) != 2 {
		return page, false
	}
	var id string
	if json.Unmarshal(next["messageId"], &id) != nil || id != page.messageID {
		return page, false
	}
	var nextRange struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
	}
	decoder = json.NewDecoder(strings.NewReader(string(next["byteRange"])))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&nextRange) != nil || nextRange.From != interval.Offset || nextRange.To != interval.Offset+interval.Length {
		return page, false
	}
	return page, true
}

func uniqueJSONKeys(body string) bool {
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var read func() bool
	read = func() bool {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !read() {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for decoder.More() {
				if !read() {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !read() {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}
