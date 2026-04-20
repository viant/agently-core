package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
)

type skillTestModel struct{}

func (m *skillTestModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return nil, nil
}

func (m *skillTestModel) Implements(string) bool { return false }

type skillTestFinder struct{}

func (f *skillTestFinder) Find(context.Context, string) (llm.Model, error) {
	return &skillTestModel{}, nil
}

func TestPrepareGenerateRequest_WritesLLMRequestWithSkillsPrompt(t *testing.T) {
	traceFile := filepath.Join(t.TempDir(), "trace.jsonl")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", traceFile)
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)

	svc := New(&skillTestFinder{}, nil, nil)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-skill-debug")
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{ConversationID: "conv-skill-debug", TurnID: "turn-skill-debug"})
	ctx = runtimerequestctx.WithRunMeta(ctx, runtimerequestctx.RunMeta{RunID: "turn-skill-debug", Iteration: 2})
	ctx = toolexec.WithWorkdir(ctx, "/tmp/workdir")
	in := &GenerateInput{
		ModelSelection: llm.ModelSelection{
			Model: "test-model",
			Options: &llm.Options{
				Metadata: map[string]interface{}{"modelSource": "agent.model"},
			},
		},
		Binding: &binding.Binding{
			SkillsPrompt: "<skills_instructions>\n## Skills\n- playwright-cli: Automate browser interactions.\n</skills_instructions>",
		},
		Prompt: &binding.Prompt{Engine: "go", Text: "hello"},
		UserID: "tester",
	}
	req, _, err := svc.prepareGenerateRequest(ctx, in)
	if err != nil {
		t.Fatalf("prepareGenerateRequest() error: %v", err)
	}
	if len(req.Messages) == 0 || !strings.Contains(req.Messages[0].Content, "<skills_instructions>") {
		t.Fatalf("request messages = %#v", req.Messages)
	}
	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-skill-debug.json"))
	if err != nil {
		t.Fatalf("read llm-request payload: %v", err)
	}
	var payload struct {
		DebugContext map[string]interface{} `json:"debugContext"`
		Messages     []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.Messages) == 0 || !strings.Contains(payload.Messages[0].Content, "<skills_instructions>") {
		t.Fatalf("payload missing skills prompt: %s", string(data))
	}
	if payload.DebugContext["conversationId"] != "conv-skill-debug" {
		t.Fatalf("expected conversationId in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["turnId"] != "turn-skill-debug" {
		t.Fatalf("expected turnId in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["iteration"] != float64(2) {
		t.Fatalf("expected iteration in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["workdir"] != "/tmp/workdir" {
		t.Fatalf("expected workdir in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["modelSource"] != "agent.model" {
		t.Fatalf("expected modelSource in debugContext, got %#v", payload.DebugContext)
	}

	data, err = os.ReadFile(filepath.Join(payloadDir, "llm-request-turn-skill-debug-iter_2.json"))
	if err != nil {
		t.Fatalf("read iteration llm-request payload: %v", err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal iteration payload: %v", err)
	}
	if len(payload.Messages) == 0 || !strings.Contains(payload.Messages[0].Content, "<skills_instructions>") {
		t.Fatalf("iteration payload missing skills prompt: %s", string(data))
	}
	if payload.DebugContext["turnId"] != "turn-skill-debug" || payload.DebugContext["iteration"] != float64(2) {
		t.Fatalf("expected iteration debugContext in iteration payload, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["workdir"] != "/tmp/workdir" {
		t.Fatalf("expected workdir in iteration debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["modelSource"] != "agent.model" {
		t.Fatalf("expected modelSource in iteration debugContext, got %#v", payload.DebugContext)
	}
}

func TestWriteLLMRequestDebugPayload_WritesNarratorPayload(t *testing.T) {
	traceFile := filepath.Join(t.TempDir(), "trace.jsonl")
	payloadDir := t.TempDir()
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", traceFile)
	t.Setenv("AGENTLY_DEBUG_PAYLOAD_DIR", payloadDir)

	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-narrator")
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{ConversationID: "conv-narrator", TurnID: "turn-narrator"})
	ctx = runtimerequestctx.WithRunMeta(ctx, runtimerequestctx.RunMeta{RunID: "turn-narrator", Iteration: 2})
	req := &llm.GenerateRequest{
		Messages: []llm.Message{
			llm.NewSystemMessage("Narrate"),
			llm.NewTextMessage(llm.RoleUser, "status update"),
		},
		Options: &llm.Options{
			Metadata: map[string]interface{}{
				"modelSource":          "async.narrator",
				"asyncNarrator":        true,
				"asyncNarrationMode":   "llm",
				"asyncNarratorOpID":    "op-1",
				"asyncNarratorUserAsk": "Analyze order 2639076 performance",
				"asyncNarratorIntent":  "pull pacing and delivery slices",
				"asyncNarratorSummary": "orderId=2639076 | workdir=/tmp/ws",
				"asyncNarratorTool":    "llm/agents:status",
				"asyncNarratorStatus":  "running",
			},
		},
	}
	WriteLLMRequestDebugPayload(ctx, "test-model", req, nil, "narrator-op-1")

	data, err := os.ReadFile(filepath.Join(payloadDir, "llm-request-conv-narrator-narrator-op-1.json"))
	if err != nil {
		t.Fatalf("read narrator payload: %v", err)
	}
	var payload struct {
		DebugContext map[string]interface{} `json:"debugContext"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal narrator payload: %v", err)
	}
	if payload.DebugContext["asyncNarrator"] != true {
		t.Fatalf("expected asyncNarrator in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["asyncNarrationMode"] != "llm" {
		t.Fatalf("expected asyncNarrationMode llm in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["asyncNarratorOpID"] != "op-1" {
		t.Fatalf("expected asyncNarratorOpID op-1 in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["asyncNarratorUserAsk"] != "Analyze order 2639076 performance" {
		t.Fatalf("expected asyncNarratorUserAsk in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["asyncNarratorIntent"] != "pull pacing and delivery slices" {
		t.Fatalf("expected asyncNarratorIntent in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["asyncNarratorSummary"] != "orderId=2639076 | workdir=/tmp/ws" {
		t.Fatalf("expected asyncNarratorSummary in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["asyncNarratorTool"] != "llm/agents:status" {
		t.Fatalf("expected asyncNarratorTool in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["asyncNarratorStatus"] != "running" {
		t.Fatalf("expected asyncNarratorStatus in debugContext, got %#v", payload.DebugContext)
	}
	if payload.DebugContext["modelSource"] != "async.narrator" {
		t.Fatalf("expected modelSource async.narrator, got %#v", payload.DebugContext)
	}
}
