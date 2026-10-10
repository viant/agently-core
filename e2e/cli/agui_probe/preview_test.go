package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestNativePreviewMetadataKeepsOriginalPagingContract(t *testing.T) {
	native := map[string]any{"body": "REAL", "messageId": "original-effect", "returned": float64(4), "remaining": float64(12), "continuationHint": "Call message-show with original-effect", "continuation": map[string]any{"hasMore": true, "returned": float64(4), "remaining": float64(12), "nextRange": map[string]any{"bytes": map[string]any{"offset": float64(4), "length": float64(4)}}}, "nextArgs": map[string]any{"messageId": "original-effect", "byteRange": map[string]any{"from": float64(4), "to": float64(8)}}}
	request := func(value map[string]any) any {
		raw, _ := json.Marshal(value)
		return map[string]any{"messages": []any{map[string]any{"role": "tool", "content": string(raw)}}}
	}
	actual, err := previewMetadata(request(native), "original-effect")
	if err != nil {
		t.Fatal(err)
	}
	if actual["body"] != nil || actual["bodyBytes"] != 4 || actual["messageId"] != "original-effect" {
		t.Fatal("metadata must reflect actual original object without exposing body")
	}
	for _, name := range []string{"wrong-message", "no-more", "wrong-size", "wrong-next", "missing-hint"} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(native)
			var changed map[string]any
			_ = json.Unmarshal(raw, &changed)
			switch name {
			case "wrong-message":
				changed["messageId"] = "later-effect"
			case "no-more":
				changed["continuation"].(map[string]any)["hasMore"] = false
			case "wrong-size":
				changed["returned"] = float64(3)
			case "wrong-next":
				changed["nextArgs"].(map[string]any)["byteRange"].(map[string]any)["from"] = float64(0)
			case "missing-hint":
				delete(changed, "continuationHint")
			}
			if _, err := previewMetadata(request(changed), "original-effect"); err == nil {
				t.Fatal("invalid original paging contract accepted")
			}
		})
	}
}

func TestCapturedNativePreviewMetadata(t *testing.T) {
	raw, err := os.ReadFile("testdata/native-preview.json")
	if err != nil {
		t.Fatal(err)
	}
	var actual any
	if err = json.Unmarshal(raw, &actual); err != nil {
		t.Fatal(err)
	}
	metadata, err := previewMetadata(actual, "811d953e-c389-4c22-8a29-0532216e28d8")
	if err != nil {
		t.Fatal(err)
	}
	if metadata["returned"] != float64(7372) || metadata["remaining"] != float64(123746) || metadata["bodyBytes"] != 7372 {
		t.Fatal("native captured lengths changed")
	}
	if metadata["messageId"] != "811d953e-c389-4c22-8a29-0532216e28d8" {
		t.Fatal("original message identity changed")
	}
}
