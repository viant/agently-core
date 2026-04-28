package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

type nativeContinuationEnvelope struct {
	MessageID        string                      `json:"messageId,omitempty"`
	ContinuationHint string                      `json:"continuationHint,omitempty"`
	NextArgs         map[string]interface{}      `json:"nextArgs,omitempty"`
	Continuation     nativeContinuationEnvelopeC `json:"continuation"`
}

type nativeContinuationEnvelopeC struct {
	HasMore   bool                        `json:"hasMore"`
	Remaining int                         `json:"remaining,omitempty"`
	Returned  int                         `json:"returned,omitempty"`
	NextRange nativeContinuationNextRange `json:"nextRange"`
}

type nativeContinuationNextRange struct {
	Bytes *nativeContinuationByteRange `json:"bytes,omitempty"`
	Lines *nativeContinuationLineRange `json:"lines,omitempty"`
}

type nativeContinuationByteRange struct {
	Offset      int `json:"offset,omitempty"`
	OffsetBytes int `json:"offsetBytes,omitempty"`
	Length      int `json:"length,omitempty"`
	LengthBytes int `json:"lengthBytes,omitempty"`
}

type nativeContinuationLineRange struct {
	Start     int `json:"start,omitempty"`
	StartLine int `json:"startLine,omitempty"`
	Count     int `json:"count,omitempty"`
	LineCount int `json:"lineCount,omitempty"`
}

func (r nativeContinuationByteRange) normalized() (offset, length int, ok bool) {
	offset = r.Offset
	if offset == 0 {
		offset = r.OffsetBytes
	}
	length = r.Length
	if length == 0 {
		length = r.LengthBytes
	}
	return offset, length, offset > 0 && length > 0
}

func (r nativeContinuationLineRange) normalized() (start, count int, ok bool) {
	start = r.Start
	if start == 0 {
		start = r.StartLine
	}
	count = r.Count
	if count == 0 {
		count = r.LineCount
	}
	return start, count, start > 0 && count > 0
}

// buildOverflowPreview trims body to limit and appends a simple omitted trailer.
// When allowContinuation is true and refMessageID is provided, it will emit a
// continuation wrapper (or JSON truncation) and return overflow=true to enable
// paging via message:show. Otherwise, it returns a plain truncated preview and
// overflow=false so the paging tool is not exposed.
func buildOverflowPreview(body string, threshold int, refMessageID string, allowContinuation bool) (string, bool) {
	body = strings.TrimSpace(body)
	if allowContinuation {
		body = annotateNativeContinuationJSON(body, strings.TrimSpace(refMessageID))
	}
	if threshold <= 0 || len(body) <= threshold {
		return body, false
	}
	limit := int(0.9 * float64(threshold)) // to prevent internal show result being over threshold when wrapped as json + metadata

	if allowContinuation && strings.TrimSpace(refMessageID) != "" {
		if jsonPreview, ok := truncateContinuationJSON(body, limit, strings.TrimSpace(refMessageID)); ok {
			return jsonPreview, true
		}
		size := len(body)
		returned := limit
		if returned > size {
			returned = size
		}
		remaining := size - returned
		nextTo := returned + returned
		if nextTo > size {
			nextTo = size
		}
		chunk := strings.TrimSpace(body[:returned])
		chunk += "[... omitted from " + fmt.Sprintf("%d", returned) + " to " + fmt.Sprintf("%d", size) + "]"
		id := strings.TrimSpace(refMessageID)
		return fmt.Sprintf(`overflow: true
messageId: %s
nextArgs:
  messageId: %s
  byteRange:
    from: %d
    to: %d
nextRange:
  bytes:
    offset: %d
    length: %d
hasMore: true
remaining: %d
returned: %d
useToolToSeeMore: message-show
content: |
%s`, id, id, returned, nextTo, returned, returned, remaining, returned, chunk), true
	}

	size := len(body)
	returned := limit
	if returned > size {
		returned = size
	}
	chunk := strings.TrimSpace(body[:returned])
	chunk += "[... omitted " + fmt.Sprintf("%d", size-returned) + " of " + fmt.Sprintf("%d", size) + "]"
	return chunk, false
}

// annotateNativeContinuationJSON adds an explicit follow-up hint for tool
// results that already expose native continuation ranges. This keeps the body
// JSON while making the next call args obvious to the model.
func annotateNativeContinuationJSON(body string, refMessageID string) string {
	if !strings.HasPrefix(strings.TrimSpace(body), "{") {
		return body
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &root); err != nil || root == nil {
		return body
	}
	var envelope nativeContinuationEnvelope
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return body
	}
	if !envelope.Continuation.HasMore {
		return body
	}
	if err := annotateNativeContinuationRoot(root, envelope, refMessageID); err != nil {
		return body
	}
	if out, err := json.Marshal(root); err == nil {
		return string(out)
	}
	return body
}

func annotateNativeContinuationRoot(root map[string]json.RawMessage, envelope nativeContinuationEnvelope, refMessageID string) error {
	if root == nil || !envelope.Continuation.HasMore {
		return nil
	}
	sourceMessageID := strings.TrimSpace(envelope.MessageID)
	if sourceMessageID == "" {
		sourceMessageID = strings.TrimSpace(refMessageID)
	}
	if bytesHint := envelope.Continuation.NextRange.Bytes; bytesHint != nil {
		offset, length, ok := bytesHint.normalized()
		if ok {
			return applyNativeContinuationBytes(root, sourceMessageID, offset, length)
		}
	}
	if linesHint := envelope.Continuation.NextRange.Lines; linesHint != nil {
		start, count, ok := linesHint.normalized()
		if ok {
			return applyNativeContinuationLines(root, sourceMessageID, start, count)
		}
	}
	return nil
}

func applyNativeContinuationBytes(root map[string]json.RawMessage, refMessageID string, offset, length int) error {
	if offset <= 0 || length <= 0 {
		return nil
	}
	if refMessageID != "" {
		putJSONFieldIfMissing(root, "messageId", refMessageID)
	}
	putJSONFieldIfMissing(root, "continuationHint",
		fmt.Sprintf("Call the same tool again with offsetBytes=%d, lengthBytes=%d, maxBytes=%d. Do not restart at 0.", offset, length, length))
	if _, exists := root["nextArgs"]; !exists {
		nextArgs := map[string]interface{}{}
		if refMessageID != "" {
			nextArgs["messageId"] = refMessageID
			nextArgs["byteRange"] = map[string]interface{}{
				"from": offset,
				"to":   offset + length,
			}
		} else {
			nextArgs["offsetBytes"] = offset
			nextArgs["lengthBytes"] = length
			nextArgs["maxBytes"] = length
		}
		return putJSONField(root, "nextArgs", nextArgs)
	}
	return nil
}

func applyNativeContinuationLines(root map[string]json.RawMessage, refMessageID string, start, count int) error {
	if start <= 0 || count <= 0 {
		return nil
	}
	if refMessageID != "" {
		putJSONFieldIfMissing(root, "messageId", refMessageID)
	}
	putJSONFieldIfMissing(root, "continuationHint",
		fmt.Sprintf("Call the same tool again with startLine=%d and lineCount=%d.", start, count))
	if _, exists := root["nextArgs"]; !exists {
		nextArgs := map[string]interface{}{}
		if refMessageID != "" {
			nextArgs["messageId"] = refMessageID
		}
		nextArgs["startLine"] = start
		nextArgs["lineCount"] = count
		return putJSONField(root, "nextArgs", nextArgs)
	}
	return nil
}

func putJSONFieldIfMissing(root map[string]json.RawMessage, key string, value interface{}) {
	if _, exists := root[key]; exists {
		return
	}
	_ = putJSONField(root, key, value)
}

func putJSONField(root map[string]json.RawMessage, key string, value interface{}) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	root[key] = encoded
	return nil
}

func intFromAny(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

// truncateContinuationJSON attempts to truncate the largest string field within
// a JSON object (searching nested objects/arrays) while preserving continuation
// metadata. Returns the modified JSON and true when truncation occurred,
// otherwise false.
func truncateContinuationJSON(body string, limit int, refMessageID string) (string, bool) {
	if !strings.HasPrefix(body, "{") {
		return "", false
	}
	var root interface{}
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return "", false
	}
	parent, key, idx, value := findLargestString(root)
	if parent == nil || value == "" || limit <= 0 || len(value) <= limit {
		return "", false
	}
	truncated := strings.TrimSpace(value[:limit])
	switch container := parent.(type) {
	case map[string]interface{}:
		container[key] = truncated
	case []interface{}:
		if idx >= 0 && idx < len(container) {
			container[idx] = truncated
		}
	default:
		return "", false
	}

	remaining := len(value) - len(truncated)
	returned := len(truncated)
	rootMap, ok := root.(map[string]interface{})
	if !ok {
		return "", false
	}
	rootMap["remaining"] = remaining
	rootMap["returned"] = returned

	cont, _ := rootMap["continuation"].(map[string]interface{})
	if cont == nil {
		cont = map[string]interface{}{}
		rootMap["continuation"] = cont
	}
	cont["hasMore"] = true
	cont["remaining"] = remaining
	cont["returned"] = returned
	if _, ok := cont["mode"]; !ok {
		cont["mode"] = "head"
	}
	nextRange, _ := cont["nextRange"].(map[string]interface{})
	if nextRange == nil {
		nextRange = map[string]interface{}{}
	}
	bytesHint, _ := nextRange["bytes"].(map[string]interface{})
	if bytesHint == nil {
		bytesHint = map[string]interface{}{}
	}
	bytesHint["offset"] = returned
	nextLength := returned
	if remaining > 0 && remaining < nextLength {
		nextLength = remaining
	}
	bytesHint["length"] = nextLength
	nextRange["bytes"] = bytesHint
	cont["nextRange"] = nextRange
	sourceMessageID := strings.TrimSpace(refMessageID)
	if rawID, ok := rootMap["messageId"]; ok {
		if existing, ok := rawID.(string); ok && strings.TrimSpace(existing) != "" {
			sourceMessageID = strings.TrimSpace(existing)
		}
	}
	if sourceMessageID != "" {
		rootMap["messageId"] = sourceMessageID
	}
	if _, ok := rootMap["continuationHint"]; !ok {
		rootMap["continuationHint"] = fmt.Sprintf("Call message-show with messageId=%s and byteRange.from=%d, byteRange.to=%d.", sourceMessageID, returned, returned+nextLength)
	}
	if _, ok := rootMap["nextArgs"]; !ok {
		nextArgs := map[string]interface{}{}
		if sourceMessageID != "" {
			nextArgs["messageId"] = sourceMessageID
			nextArgs["byteRange"] = map[string]interface{}{
				"from": returned,
				"to":   returned + nextLength,
			}
		} else {
			nextArgs["offsetBytes"] = returned
			nextArgs["lengthBytes"] = nextLength
			nextArgs["maxBytes"] = nextLength
		}
		rootMap["nextArgs"] = nextArgs
	}

	out, err := json.Marshal(root)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// findLargestString walks nested maps/slices to locate the largest string value.
// It returns the parent container, map key or slice index, and the string value.
func findLargestString(v interface{}) (parent interface{}, key string, idx int, val string) {
	idx = -1
	var visit func(node interface{}, p interface{}, k string, i int)
	visit = func(node interface{}, p interface{}, k string, i int) {
		switch n := node.(type) {
		case string:
			if len(n) > len(val) {
				parent, key, idx, val = p, k, i, n
			}
		case map[string]interface{}:
			for mk, mv := range n {
				visit(mv, n, mk, -1)
			}
		case []interface{}:
			for si, sv := range n {
				visit(sv, n, "", si)
			}
		}
	}
	visit(v, nil, "", -1)
	return
}
