package main

import (
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/sdk"
	"net/http"
	"reflect"
	"strings"
)

// previewMetadata reads the actual normalized model request. It does not build
// preview content or alias legacy fields; all returned metadata is native data.
func previewMetadata(request any, messageID string) (map[string]any, error) {
	object, ok := request.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("model request must be an object")
	}
	messages, ok := object["messages"].([]any)
	if !ok {
		return nil, fmt.Errorf("model request messages absent")
	}
	var found map[string]any
	for _, entry := range messages {
		message, ok := entry.(map[string]any)
		if !ok || message["role"] != "tool" {
			continue
		}
		content, ok := message["content"].(string)
		if !ok {
			continue
		}
		var native map[string]any
		if json.Unmarshal([]byte(content), &native) != nil || native["messageId"] != messageID {
			continue
		}
		continuation, ok := native["continuation"].(map[string]any)
		if !ok || continuation["hasMore"] != true {
			return nil, fmt.Errorf("original tool preview has no continuation")
		}
		returned, rok := native["returned"].(float64)
		remaining, mok := native["remaining"].(float64)
		body, bok := native["body"].(string)
		if !rok || !mok || !bok || returned <= 0 || remaining <= 0 || float64(len([]byte(body))) != returned {
			return nil, fmt.Errorf("actual preview byte lengths inconsistent")
		}
		if continuation["returned"] != returned || continuation["remaining"] != remaining {
			return nil, fmt.Errorf("continuation lengths differ from native preview")
		}
		next, ok := native["nextArgs"].(map[string]any)
		if !ok || next["messageId"] != messageID {
			return nil, fmt.Errorf("paging targets another message")
		}
		interval, ok := next["byteRange"].(map[string]any)
		if !ok || interval["from"] != returned {
			return nil, fmt.Errorf("paging offset differs from returned bytes")
		}
		end, eok := interval["to"].(float64)
		if !eok || end <= returned || end-returned > remaining {
			return nil, fmt.Errorf("paging range outside remaining bytes")
		}
		ranges, ok := continuation["nextRange"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("continuation range absent")
		}
		bytesRange, ok := ranges["bytes"].(map[string]any)
		if !ok || bytesRange["offset"] != returned || bytesRange["length"] != end-returned {
			return nil, fmt.Errorf("native paging ranges differ")
		}
		hint, ok := native["continuationHint"].(string)
		if !ok || !strings.Contains(hint, "message-show") {
			return nil, fmt.Errorf("native paging hint absent")
		}
		metadata := map[string]any{}
		for key, value := range native {
			if key != "body" {
				metadata[key] = value
			}
		}
		metadata["bodyBytes"] = len([]byte(body))
		if found != nil && !reflect.DeepEqual(found, metadata) {
			return nil, fmt.Errorf("ambiguous original message previews")
		}
		found = metadata
	}
	if found == nil {
		return nil, fmt.Errorf("actual original-message preview missing")
	}
	return found, nil
}

func (h *handler) preview(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/preview-metadata/")
	h.mu.Lock()
	record := h.runs[id]
	h.mu.Unlock()
	if record == nil || record.ownerHash != digest(token(r)) {
		http.NotFound(w, r)
		return
	}
	record.mu.Lock()
	actual, target := record.identity, record.target
	record.mu.Unlock()
	client, err := newClient(target, token(r), &observer{next: http.DefaultTransport})
	if err != nil {
		write(w, 500, &probeError{"preview", "client", "SDK initialization failed"})
		return
	}
	snapshot, err := client.GetTranscript(r.Context(), &sdk.GetTranscriptInput{ConversationID: actual.ConversationID, IncludeModelCalls: true, IncludeToolCalls: true})
	if err != nil || snapshot == nil || snapshot.Conversation == nil {
		write(w, 403, &probeError{"preview", "read-denied", "current owner cannot read observed conversation"})
		return
	}
	payloadID, messageID := r.URL.Query().Get("requestPayloadId"), r.URL.Query().Get("messageId")
	owned := false
	originalEffect := false
	for _, t := range snapshot.Conversation.Turns {
		if t == nil || t.Execution == nil {
			continue
		}
		for _, page := range t.Execution.Pages {
			if page == nil {
				continue
			}
			for _, step := range page.ToolSteps {
				if step != nil && step.ToolMessageID == messageID {
					originalEffect = true
				}
			}
		}
	}
	for _, turn := range snapshot.Conversation.Turns {
		if turn == nil || turn.TurnID != actual.TurnID || turn.Execution == nil {
			continue
		}
		for _, page := range turn.Execution.Pages {
			if page == nil {
				continue
			}
			for _, step := range page.ModelSteps {
				if step != nil && step.RequestPayloadID == payloadID {
					owned = true
				}
			}
		}
	}
	if payloadID == "" || messageID == "" || !owned || !originalEffect {
		write(w, 409, &probeError{"preview", "identity-mismatch", "payload does not belong to observed original turn"})
		return
	}
	request, _, err := rawOwnedPayload(r.Context(), target, token(r), payloadID)
	if err != nil {
		write(w, 409, &probeError{"preview", "payload", "actual payload unavailable"})
		return
	}
	metadata, err := previewMetadata(request, messageID)
	if err != nil {
		write(w, 409, &probeError{"preview", "metadata-mismatch", err.Error()})
		return
	}
	write(w, 200, map[string]any{"identity": actual, "requestPayloadId": payloadID, "preview": metadata})
}
