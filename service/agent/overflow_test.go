package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildOverflowPreview_EmitsMessageShowArgs(t *testing.T) {
	body := strings.Repeat("A", 5000)
	preview, overflow := buildOverflowPreview(body, 1000, "msg-123", true)
	if !overflow {
		t.Fatalf("expected overflow=true")
	}
	if !strings.Contains(preview, "useToolToSeeMore: message-show") {
		t.Fatalf("expected message-show hint, got: %s", preview)
	}
	if !strings.Contains(preview, "nextArgs:") {
		t.Fatalf("expected nextArgs block, got: %s", preview)
	}
	if !strings.Contains(preview, "messageId: msg-123") {
		t.Fatalf("expected messageId in nextArgs, got: %s", preview)
	}
	if !strings.Contains(preview, "byteRange:") {
		t.Fatalf("expected byteRange in nextArgs, got: %s", preview)
	}
	if !strings.Contains(preview, "from: 900") {
		t.Fatalf("expected byteRange.from=900, got: %s", preview)
	}
	if !strings.Contains(preview, "to: 1800") {
		t.Fatalf("expected byteRange.to=1800, got: %s", preview)
	}
}

func TestBuildOverflowPreview_AnnotatesNativeContinuationJSONWithMessageShowArgs(t *testing.T) {
	body := `{"content":"` + strings.Repeat("A", 3000) + `","continuation":{"hasMore":true,"remaining":2100,"returned":900,"nextRange":{"bytes":{"offset":900,"length":900}}}}`
	preview, overflow := buildOverflowPreview(body, 1000, "msg-456", true)
	if !overflow {
		t.Fatalf("expected overflow=true")
	}
	if !strings.Contains(preview, `"messageId":"msg-456"`) {
		t.Fatalf("expected native continuation preview to carry messageId, got: %s", preview)
	}
	if !strings.Contains(preview, `"nextArgs":{"byteRange":{"from":900,"to":1800},"messageId":"msg-456"}`) &&
		!strings.Contains(preview, `"nextArgs":{"messageId":"msg-456","byteRange":{"from":900,"to":1800}}`) {
		t.Fatalf("expected native continuation preview to carry message-show nextArgs, got: %s", preview)
	}
}

func TestAnnotateNativeContinuationJSON_DataDriven(t *testing.T) {
	testCases := []struct {
		name         string
		body         string
		refMessageID string
		wantPath     string
		wantValue    interface{}
	}{
		{
			name:         "bytes canonical fields become message-show args",
			body:         `{"content":"abc","continuation":{"hasMore":true,"nextRange":{"bytes":{"offset":900,"length":900}}}}`,
			refMessageID: "msg-bytes",
			wantPath:     "nextArgs.byteRange.from",
			wantValue:    float64(900),
		},
		{
			name:         "bytes alias fields become message-show args",
			body:         `{"content":"abc","continuation":{"hasMore":true,"nextRange":{"bytes":{"offsetBytes":512,"lengthBytes":256}}}}`,
			refMessageID: "msg-bytes-alias",
			wantPath:     "nextArgs.byteRange.to",
			wantValue:    float64(768),
		},
		{
			name:         "line canonical fields become nextArgs",
			body:         `{"content":"abc","continuation":{"hasMore":true,"nextRange":{"lines":{"start":41,"count":20}}}}`,
			refMessageID: "msg-lines",
			wantPath:     "nextArgs.startLine",
			wantValue:    float64(41),
		},
		{
			name:         "line alias fields become nextArgs",
			body:         `{"content":"abc","continuation":{"hasMore":true,"nextRange":{"lines":{"startLine":7,"lineCount":11}}}}`,
			refMessageID: "msg-lines-alias",
			wantPath:     "nextArgs.lineCount",
			wantValue:    float64(11),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := annotateNativeContinuationJSON(tc.body, tc.refMessageID)
			var doc map[string]interface{}
			if err := json.Unmarshal([]byte(got), &doc); err != nil {
				t.Fatalf("failed to decode annotated continuation: %v\nbody=%s", err, got)
			}
			if actual, ok := nestedLookup(doc, tc.wantPath); !ok || actual != tc.wantValue {
				t.Fatalf("expected %s=%v, got %v (found=%v) in %s", tc.wantPath, tc.wantValue, actual, ok, got)
			}
			if actual, ok := nestedLookup(doc, "messageId"); !ok || actual != tc.refMessageID {
				t.Fatalf("expected messageId=%s, got %v (found=%v) in %s", tc.refMessageID, actual, ok, got)
			}
		})
	}
}

func nestedLookup(root map[string]interface{}, path string) (interface{}, bool) {
	var cur interface{} = root
	for _, segment := range strings.Split(path, ".") {
		node, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		next, ok := node[segment]
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}
