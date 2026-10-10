package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
)

func TestBuildToolExecutions_ReplayWithDisabledGeneralPreview(t *testing.T) {
	small := `{"agents":[{"id":"chatter"}]}`
	native := `{"body":"page one","messageId":"original-effect","continuation":{"hasMore":true,"returned":8,"remaining":131064,"nextRange":{"bytes":{"offset":8,"length":8}}},"nextArgs":{"messageId":"original-effect","byteRange":{"from":8,"to":16}},"continuationHint":"Call message-show with original-effect"}`
	artifact := `{"resources":[{"id":"original-artifact","uri":"scratchpad://artifact/original-artifact","mimeType":"application/octet-stream","sizeBytes":131072,"sha256":"original-digest"}]}`
	large := `{"body":"` + strings.Repeat("CHUNK-0000 LARGE_RESULT_SENTINEL ", 4096) + `"}`
	for _, tc := range []struct {
		name, body    string
		general, tool int
		wantPreview   bool
	}{
		{"zero-small-compressed", small, 0, 0, false},
		{"negative-small-compressed", small, -1, 0, false},
		{"zero-existing-native-continuation", native, 0, 0, false},
		{"zero-captured-artifact-descriptor", artifact, 0, 0, false},
		{"zero-general-positive-tool-limit", large, 0, 1024, true},
		{"positive-configured-general-limit", large, 1024, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			turnID := "turn-original"
			conv := &apiconv.Conversation{Transcript: []*conversationmodel.TranscriptView{{Id: turnID, Message: []*conversationmodel.MessageView{{Id: "tool-parent-original", ConversationId: "conv-original", TurnId: strPtr(turnID), Role: "assistant", Type: "tool_op", CreatedAt: now, ToolMessage: []*conversationmodel.ToolMessageView{{Id: "tool-message-original", CreatedAt: now, ToolCall: &conversationmodel.ToolCallView{OpId: "call-original", ToolName: "mcplarge-large_result", RequestPayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr("{}")}, ResponsePayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr(gzipString(t, tc.body)), Compression: "gzip"}}}}}}}}}
			service := &Service{defaults: &config.Defaults{PreviewSettings: config.PreviewSettings{Limit: tc.general, ToolResultLimit: tc.tool}}, llm: core.New(continuationFinder{}, nil, nil)}
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-original", TurnID: turnID})
			result, err := service.buildToolExecutions(ctx, &QueryInput{ModelOverride: "openai_gpt4o_mini_responses_preview"}, conv, agentmdl.ToolCallExposure("conversation"))
			if err != nil || len(result.Calls) != 1 {
				t.Fatal("original replay call missing")
			}
			call := result.Calls[0]
			if call.ID != "call-original" || call.Name != "mcplarge-large_result" {
				t.Fatal("original operation/tool identity changed")
			}
			if !tc.wantPreview {
				if call.Result != tc.body {
					t.Errorf("actual replay body lost or changed: gotBytes=%d wantBytes=%d gotSHA=%x wantSHA=%x", len(call.Result), len(tc.body), sha256.Sum256([]byte(call.Result)), sha256.Sum256([]byte(tc.body)))
				}
				if result.Overflow {
					t.Error("unconfigured preview unexpectedly enabled")
				}
			} else {
				var preview map[string]any
				if err := json.Unmarshal([]byte(call.Result), &preview); err != nil {
					t.Fatal("configured preview is not native structured output")
				}
				if !result.Overflow || len(call.Result) >= len(tc.body) || strings.Contains(call.Result, strings.Repeat("CHUNK-0000 LARGE_RESULT_SENTINEL ", 512)) {
					t.Error("configured oversized body leaked or continuation lost")
				}
				if preview["messageId"] != "tool-parent-original" {
					t.Error("configured preview changed original reference")
				}
				continuation, ok := preview["continuation"].(map[string]any)
				if !ok || continuation["hasMore"] != true {
					t.Fatal("original paging unavailable")
				}
				next, ok := preview["nextArgs"].(map[string]any)
				if !ok || next["messageId"] != "tool-parent-original" {
					t.Error("next request refers to another effect")
				}
			}
		})
	}
}
