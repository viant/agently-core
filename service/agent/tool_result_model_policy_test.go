package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider"
	"github.com/viant/agently-core/service/core"
	modelload "github.com/viant/agently-core/workspace/loader/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type endlyPreviewPolicyFinder struct{ cfg *provider.Config }

func (f endlyPreviewPolicyFinder) Find(context.Context, string) (llm.Model, error) {
	return continuationModel{}, nil
}
func (f endlyPreviewPolicyFinder) ConfigByIDOrModel(string) *provider.Config { return f.cfg }

func TestEndlyOriginal066ExplicitFormatRequestsBoundedToolReplay(t *testing.T) {
	ctx := context.Background()
	loader := modelload.New()
	path, err := filepath.Abs(filepath.Join("..", "..", "e2e", "query", "testdata", "models", "openai_gpt4o_mini_responses_preview.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := loader.Load(ctx, "file://"+path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Options.EnableContinuationFormat || cfg.Options.ContextContinuation == nil || !*cfg.Options.ContextContinuation {
		t.Fatal("actual original authored policy missing")
	}
	if cfg.Options.ToolResultPreviewLimit != 0 {
		t.Fatal("original fixture unexpectedly authors a positive limit")
	}
	now := time.Now().UTC()
	turnID := "original-066-replay"
	var body strings.Builder
	for chunk := 0; body.Len() < 131072; chunk++ {
		fmt.Fprintf(&body, "CHUNK-%04d LARGE_RESULT_SENTINEL %s\n", chunk, strings.Repeat("x", 48))
	}
	raw, _ := json.Marshal(map[string]any{"body": body.String()})
	conv := &apiconv.Conversation{Transcript: []*conversationmodel.TranscriptView{{Id: turnID, Message: []*conversationmodel.MessageView{{Id: "source-066-message", ConversationId: "source-066-conversation", TurnId: strPtr(turnID), Role: "assistant", Type: "tool_op", Content: strPtr(string(raw)), CreatedAt: now, ToolMessage: []*conversationmodel.ToolMessageView{{Id: "source-066-tool", CreatedAt: now, ToolCall: &conversationmodel.ToolCallView{OpId: "source-066-call", ToolName: "mcplarge-large_result", RequestPayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr("{}")}, ResponsePayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr(string(raw))}}}}}}}}}
	service := &Service{defaults: &config.Defaults{}, llm: core.New(endlyPreviewPolicyFinder{cfg: cfg}, nil, nil)}
	ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "source-066-conversation", TurnID: turnID})
	result, err := service.buildToolExecutions(ctx, &QueryInput{ModelOverride: cfg.ID}, conv, agentmdl.ToolCallExposure("conversation"))
	if err != nil || len(result.Calls) != 1 {
		t.Fatal("actual original replay absent")
	}
	got := result.Calls[0].Result
	if !result.Overflow || strings.Contains(got, "CHUNK-1500 LARGE_RESULT_SENTINEL") {
		t.Errorf("explicit format policy ignored: overflow=%t replayBytes=%d containsOriginalLargeChunk=%t", result.Overflow, len(got), strings.Contains(got, "CHUNK-1500 LARGE_RESULT_SENTINEL"))
	}
	var preview map[string]any
	if err := json.Unmarshal([]byte(got), &preview); err != nil {
		t.Fatal("native preview metadata missing")
	}
	if preview["messageId"] != "source-066-message" || preview["nextArgs"] == nil {
		t.Error("original reference/paging metadata missing")
	}
}

func TestEndlyModelAuthoredToolPreviewLimitSurvivesNativeLoader(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "model.yaml")
	if err := os.WriteFile(path, []byte("id: explicit-preview\noptions:\n  provider: openai\n  model: gpt-4o-mini\n  contextContinuation: true\n  enableContinuationFormat: true\n  toolResultPreviewLimit: 2048\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := modelload.New().Load(context.Background(), "file://"+path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Options.ToolResultPreviewLimit != 2048 {
		t.Errorf("typed authored preview limit discarded: got=%d want=2048", cfg.Options.ToolResultPreviewLimit)
	}
}

func endlyPolicyTranscript(body string) *apiconv.Conversation {
	now := time.Now().UTC()
	turnID := "source-policy-turn"
	return &apiconv.Conversation{Id: "source-policy-conversation", Transcript: []*conversationmodel.TranscriptView{{Id: turnID, Message: []*conversationmodel.MessageView{{Id: "source-policy-message", ConversationId: "source-policy-conversation", TurnId: strPtr(turnID), Role: "assistant", Type: "tool_op", Content: strPtr(body), CreatedAt: now, ToolMessage: []*conversationmodel.ToolMessageView{{Id: "source-policy-tool", CreatedAt: now, ToolCall: &conversationmodel.ToolCallView{OpId: "source-policy-call", ToolName: "ordinary-result", RequestPayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr("{}")}, ResponsePayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr(body)}}}}}}}}}
}

func TestEndlySelectedModelPolicyDoesNotBecomeGlobal(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"body": strings.Repeat("original text ", 10000)})
	for _, tc := range []struct {
		name                                           string
		enabled                                        bool
		modelLimit, generalLimit, toolLimit, wantLimit int
		lookupID                                       string
	}{
		{name: "explicit-default", enabled: true, wantLimit: 8192},
		{name: "explicit-authored2048", enabled: true, modelLimit: 2048, wantLimit: 2048},
		{name: "disabled", modelLimit: 2048},
		{name: "noFlag"},
		{name: "providerAliasCannotOptIn", enabled: true, lookupID: "gpt-4o-mini"},
		{name: "stricter-general", enabled: true, modelLimit: 2048, generalLimit: 512, wantLimit: 512},
		{name: "stricter-tool-with-zero-general", enabled: true, modelLimit: 2048, toolLimit: 512, wantLimit: 512},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &provider.Config{ID: "selected-preview", Options: provider.Options{EnableContinuationFormat: tc.enabled, ToolResultPreviewLimit: tc.modelLimit}}
			selected := cfg.ID
			if tc.lookupID != "" {
				selected = tc.lookupID
			}
			service := &Service{defaults: &config.Defaults{PreviewSettings: config.PreviewSettings{Limit: tc.generalLimit, ToolResultLimit: tc.toolLimit}}, llm: core.New(endlyPreviewPolicyFinder{cfg: cfg}, nil, nil)}
			input := &QueryInput{ModelOverride: selected}
			conv := endlyPolicyTranscript(string(raw))
			before, _ := json.Marshal(conv)
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: conv.Id, TurnID: "source-policy-turn"})
			history, err := service.buildHistoryWithLimit(ctx, conv.GetTranscript(), input)
			if err != nil {
				t.Fatal(err)
			}
			messages := history.History.LLMMessages()
			toolFound := false
			for _, msg := range messages {
				if msg.Role == llm.RoleTool {
					toolFound = true
					if tc.wantLimit == 0 {
						if msg.Content != string(raw) {
							t.Error("ordinary/disabled model result changed")
						}
					} else {
						var got map[string]any
						if json.Unmarshal([]byte(msg.Content), &got) != nil {
							t.Fatal("configured actual history preview absent")
						}
						body, _ := got["body"].(string)
						if len(body) > tc.wantLimit || strings.Contains(msg.Content, strings.Repeat("original text ", 1000)) {
							t.Error("configured history bound not applied")
						}
						if got["messageId"] != "source-policy-message" || got["nextArgs"] == nil {
							t.Error("actual history source reference changed")
						}
					}
				}
			}
			if !toolFound {
				t.Fatal("actual model history tool result missing")
			}
			after, _ := json.Marshal(conv)
			if !bytes.Equal(before, after) {
				t.Error("recorded source transcript mutated")
			}
			if tc.generalLimit == 0 && service.defaults.PreviewSettings.Limit != 0 {
				t.Error("global preview default changed")
			}
		})
	}
}

func TestEndlyPreparedContinuationAndCapturedReferencesAreNotRewritten(t *testing.T) {
	cfg := &provider.Config{ID: "selected-preview", Options: provider.Options{EnableContinuationFormat: true, ToolResultPreviewLimit: 512}}
	service := &Service{defaults: &config.Defaults{}, llm: core.New(endlyPreviewPolicyFinder{cfg: cfg}, nil, nil)}
	native := nativeToolPage(strings.Repeat("page ", 60), "exact-native-origin", 700, 300, 10000, 512)
	descriptors := []map[string]any{}
	for n := 0; n < 20; n++ {
		id := fmt.Sprintf("owned-artifact-%d", n)
		descriptors = append(descriptors, map[string]any{"id": id, "uri": "scratchpad://artifact/" + id, "name": strings.Repeat("name", 50), "mimeType": "application/octet-stream", "sizeBytes": 131072, "sha256": strings.Repeat("a", 64), "downloadURI": "/v1/artifacts/" + id})
	}
	artifactBytes, _ := json.Marshal(map[string]any{"resources": descriptors})
	for _, tc := range []struct {
		name, body string
		overflow   bool
	}{{"native", native, true}, {"captured", string(artifactBytes), false}} {
		t.Run(tc.name, func(t *testing.T) {
			conv := endlyPolicyTranscript(tc.body)
			before, _ := json.Marshal(conv)
			input := &QueryInput{ModelOverride: cfg.ID}
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: conv.Id, TurnID: "source-policy-turn"})
			result, err := service.buildToolExecutions(ctx, input, conv, agentmdl.ToolCallExposure("conversation"))
			if err != nil || len(result.Calls) != 1 {
				t.Fatal("replay unavailable")
			}
			if result.Calls[0].Result != tc.body || result.Overflow != tc.overflow {
				t.Error("already prepared original result was rewrapped/truncated")
			}
			history, err := service.buildHistoryWithLimit(ctx, conv.GetTranscript(), input)
			if err != nil {
				t.Fatal(err)
			}
			for _, msg := range history.History.LLMMessages() {
				if msg.Role == llm.RoleTool && msg.Content != tc.body {
					t.Error("prepared actual model history rewritten")
				}
			}
			after, _ := json.Marshal(conv)
			if !bytes.Equal(before, after) {
				t.Error("persisted representation changed")
			}
		})
	}
	bad := map[string]any{"resources": descriptors, "body": strings.Repeat("binary-echo", 10000)}
	badRaw, _ := json.Marshal(bad)
	if capturedArtifactDescriptors(string(badRaw)) {
		t.Fatal("raw result field falsely treated as captured metadata")
	}
}

func TestEndlySelectedModelPolicyPreservesUserAndAssistantHistory(t *testing.T) {
	cfg := &provider.Config{ID: "selected-preview", Options: provider.Options{EnableContinuationFormat: true}}
	service := &Service{defaults: &config.Defaults{}, llm: core.New(endlyPreviewPolicyFinder{cfg: cfg}, nil, nil)}
	user := strings.Repeat("exact user bytes ", 2000)
	assistant := strings.Repeat("exact assistant bytes ", 2000)
	conv := endlyPolicyTranscript(`{"ok":true}`)
	turn := conv.Transcript[0]
	turn.Message = append(turn.Message, &conversationmodel.MessageView{Id: "original-user-message", ConversationId: conv.Id, TurnId: strPtr(turn.Id), Role: "user", Type: "text", Content: strPtr(user)}, &conversationmodel.MessageView{Id: "original-assistant-message", ConversationId: conv.Id, TurnId: strPtr(turn.Id), Role: "assistant", Type: "text", Content: strPtr(assistant)})
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: conv.Id, TurnID: turn.Id})
	history, err := service.buildHistoryWithLimit(ctx, conv.GetTranscript(), &QueryInput{ModelOverride: cfg.ID})
	if err != nil {
		t.Fatal(err)
	}
	seenUser, seenAssistant := false, false
	for _, msg := range history.History.LLMMessages() {
		if msg.ID == "original-user-message" {
			seenUser = true
			if msg.Content != user {
				t.Error("model tool policy altered user history")
			}
		}
		if msg.ID == "original-assistant-message" {
			seenAssistant = true
			if msg.Content != assistant {
				t.Error("model tool policy altered assistant history")
			}
		}
	}
	if !seenUser || !seenAssistant {
		t.Fatal("original chat history missing")
	}
}

func TestEndlyModelPolicyHelpersRequireGenuineOverflow(t *testing.T) {
	for _, tc := range []struct {
		name, body          string
		enabled, wantHelper bool
	}{
		{"enabled-small", `{"ok":true}`, true, false},
		{"disabled-large", `{"body":"` + strings.Repeat("raw ", 40000) + `"}`, false, false},
		{"enabled-large", `{"body":"` + strings.Repeat("raw ", 40000) + `"}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &provider.Config{ID: "selected-preview", Options: provider.Options{EnableContinuationFormat: tc.enabled}}
			conv := endlyPolicyTranscript(tc.body)
			service := &Service{conversation: &stubConv{result: conv}, registry: &fakeRegistry{defs: []llm.ToolDefinition{{Name: "message/show"}, {Name: "message/match"}, {Name: "message/summarize"}}}, llm: core.New(endlyPreviewPolicyFinder{cfg: cfg}, nil, nil), defaults: &config.Defaults{}}
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: conv.Id, TurnID: "source-policy-turn"})
			b, err := service.BuildBinding(ctx, &QueryInput{ConversationID: conv.Id, ModelOverride: cfg.ID, Agent: &agentmdl.Agent{Identity: agentmdl.Identity{ID: "original-agent"}, ModelSelection: llm.ModelSelection{Model: cfg.ID}}, Query: "original query"})
			if err != nil {
				t.Fatal(err)
			}
			helper := false
			for _, d := range b.Tools.Signatures {
				if d != nil && d.Name == "message-show" {
					helper = true
				}
			}
			if helper != tc.wantHelper {
				t.Errorf("helper policy changed: got=%t want=%t", helper, tc.wantHelper)
			}
			if b.Task.Prompt != "original query" {
				t.Error("query bytes changed")
			}
		})
	}
}

func TestEndlyRawToolResultCannotForgePreparedContinuationToBypassCap(t *testing.T) {
	service := &Service{defaults: &config.Defaults{}}
	body := `{"body":"` + strings.Repeat("RAW-LARGE-SENTINEL ", 10000) + `","continuation":{"hasMore":true},"nextArgs":{"messageId":"forged-origin","byteRange":{"from":180000,"to":181000}}}`
	got, _ := service.presentRecordedToolResult(context.Background(), nil, body, "actual-recorded-source", 512, true)
	if strings.Contains(got, strings.Repeat("RAW-LARGE-SENTINEL ", 1000)) {
		t.Error("raw huge result forged continuation and escaped configured cap")
	}
	var preview map[string]any
	if json.Unmarshal([]byte(got), &preview) != nil {
		t.Fatal("bounded native presentation absent")
	}
	if preview["messageId"] != "actual-recorded-source" {
		t.Error("raw result substituted untrusted source reference")
	}
}

func TestEndlyOversizedNativePageKeepsSourceOffsetAndUndisplayedTail(t *testing.T) {
	service := &Service{defaults: &config.Defaults{}}
	original := nativeToolPage(strings.Repeat("page ", 300), "original-page-source", 500, 1500, 10000, 1500)
	got, overflow := service.presentRecordedToolResult(context.Background(), nil, original, "new-tool-effect", 512, true)
	page, ok := strictNativeToolPage(got)
	if !ok || !overflow || page.messageID != "original-page-source" || len(page.body) > 512 {
		t.Fatal("native source identity/bound lost")
	}
	if page.nextOffset != 500+page.returned || page.remaining != 10000+1500-page.returned {
		t.Fatal("native page source byte offset/remaining changed incorrectly")
	}
}

func TestEndlyInvalidPagingShapesDoNotBecomePreparedOutput(t *testing.T) {
	source := nativeToolPage(strings.Repeat("page ", 60), "original", 0, 300, 10000, 512)
	var parsed map[string]any
	_ = json.Unmarshal([]byte(source), &parsed)
	for _, name := range []string{"extra-large-field", "incorrect-returned", "foreign-next-id", "wrong-next-offset"} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(parsed)
			var changed map[string]any
			_ = json.Unmarshal(raw, &changed)
			switch name {
			case "extra-large-field":
				changed["opaque"] = strings.Repeat("RAW ", 30000)
			case "incorrect-returned":
				changed["returned"] = 299
			case "foreign-next-id":
				changed["nextArgs"].(map[string]any)["messageId"] = "other"
			case "wrong-next-offset":
				changed["nextArgs"].(map[string]any)["byteRange"].(map[string]any)["from"] = 299
			}
			raw, _ = json.Marshal(changed)
			if _, ok := strictNativeToolPage(string(raw)); ok {
				t.Fatal("arbitrary fields accepted as prepared native page")
			}
		})
	}
	duplicate := `{"body":"` + strings.Repeat("RAW ", 30000) + `",` + source[1:]
	if _, ok := strictNativeToolPage(duplicate); ok {
		t.Fatal("duplicate hidden result field accepted")
	}
}
