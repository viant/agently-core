package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	nativeconv "github.com/viant/agently-core/internal/service/conversation"
	agentproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/runtime/recovery"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	core "github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/reactor"
)

type proactiveLiveModel struct {
	*openai.Client
	counts []int
}

func (m *proactiveLiveModel) CountInputTokens(ctx context.Context, r *llm.GenerateRequest) (int, error) {
	n, err := m.Client.CountInputTokens(ctx, r)
	if err == nil {
		m.counts = append(m.counts, n)
	}
	return n, err
}

type proactiveLiveFinder struct{ model llm.Model }

func (f proactiveLiveFinder) Find(context.Context, string) (llm.Model, error) { return f.model, nil }
func (f proactiveLiveFinder) ConfigByIDOrModel(string) *provider.Config {
	return &provider.Config{Options: provider.Options{Model: "gpt-5-nano", ContextWindow: 400000}}
}

type proactiveLiveRegistry struct{ *proactiveTestRegistry }

func (r *proactiveLiveRegistry) definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: "message-remove", Description: "Replace eligible old messages with a handoff summary, retaining user facts and completed operations.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"tuples": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"messageIds": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}, "summary": map[string]interface{}{"type": "string"}, "role": map[string]interface{}{"type": "string"}}, "required": []string{"messageIds", "summary"}}}}, "required": []string{"tuples"}}}
}
func (r *proactiveLiveRegistry) Definitions() []llm.ToolDefinition {
	return []llm.ToolDefinition{r.definition()}
}
func (r *proactiveLiveRegistry) MatchDefinition(string) []*llm.ToolDefinition {
	d := r.definition()
	return []*llm.ToolDefinition{&d}
}
func (r *proactiveLiveRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) {
	d := r.definition()
	return &d, true
}

// This test is deliberately skipped in normal suites. It uses existing API
// credentials and a private conversation, never Steward. Explicit native mode
// uses the application's Datly API against an isolated temporary SQLite file.
func TestProactiveCompactionLiveNano(t *testing.T) {
	if os.Getenv("AGENTLY_PROACTIVE_LIVE") != "1" {
		t.Skip("explicit AGENTLY_PROACTIVE_LIVE=1 required for live OpenAI requests")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	meta := requestctx.TurnMeta{ConversationID: "private-live-nano-compaction", TurnID: "private-live-nano-turn-1"}
	ctx = requestctx.WithTurnMeta(requestctx.WithConversationID(ctx, meta.ConversationID), meta)
	var store apiconv.Client = convmem.New()
	storeKind := "memory"
	if os.Getenv("AGENTLY_PROACTIVE_NATIVE") == "1" {
		root := t.TempDir()
		t.Setenv("AGENTLY_DB_DRIVER", "sqlite")
		t.Setenv("AGENTLY_DB_DSN", "")
		t.Setenv("AGENTLY_DB_PATH", filepath.Join(root, "compaction.db"))
		backend, err := native.New(ctx, native.Options{WorkspaceRoot: root})
		require.NoError(t, err)
		t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
		store, err = nativeconv.New(ctx, backend)
		require.NoError(t, err)
		storeKind = "native-datly-sqlite"
	}
	conv := apiconv.NewConversation()
	conv.SetId(meta.ConversationID)
	conv.SetStatus("active")
	require.NoError(t, store.PatchConversations(ctx, conv))
	seedMeta := requestctx.TurnMeta{ConversationID: meta.ConversationID, TurnID: "private-live-nano-seed-history"}
	seedTurn := apiconv.NewTurn()
	seedTurn.SetId(seedMeta.TurnID)
	seedTurn.SetConversationID(seedMeta.ConversationID)
	seedTurn.SetStatus("succeeded")
	require.NoError(t, store.PatchTurn(ctx, seedTurn))
	turn := apiconv.NewTurn()
	turn.SetId(meta.TurnID)
	turn.SetConversationID(meta.ConversationID)
	turn.SetStatus("running")
	require.NoError(t, store.PatchTurn(ctx, turn))
	_, err := apiconv.AddMessage(ctx, store, &seedMeta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithStatus("completed"), apiconv.WithContent("The project codename is SILVER-OTTER-718. This exact fact must survive compaction. "+strings.Repeat("Historical disposable observation: the old report was checked and its result was confirmed. ", 700)))
	require.NoError(t, err)
	toolMsg, err := apiconv.AddMessage(ctx, store, &seedMeta, apiconv.WithRole("tool"), apiconv.WithType("tool_op"), apiconv.WithStatus("completed"), apiconv.WithContent(`{"operation":"lookup-done-718","status":"completed","result":"account 7180287 verified"}`))
	require.NoError(t, err)
	toolCall := apiconv.NewToolCall()
	toolCall.SetMessageID(toolMsg.Id)
	toolCall.SetTurnID(seedMeta.TurnID)
	toolCall.SetOpID("lookup-done-718")
	toolCall.SetToolName("account-lookup")
	toolCall.SetToolKind("function")
	toolCall.SetStatus("succeeded")
	require.NoError(t, store.PatchToolCall(ctx, toolCall))
	latest, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithStatus("completed"), apiconv.WithContent("State the exact project codename and already-completed lookup operation ID. Do not repeat any completed operation or invoke tools."))
	require.NoError(t, err)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSHandshakeTimeout = 30 * time.Second
	testHTTPClient := &http.Client{Transport: transport, Timeout: 90 * time.Second}
	model := &proactiveLiveModel{Client: openai.NewClient(key, "gpt-5-nano", openai.WithHTTPClient(testHTTPClient), openai.WithTimeout(90), openai.WithMaxTokens(8192))}
	reg := &proactiveLiveRegistry{proactiveTestRegistry: &proactiveTestRegistry{store: store}}
	llmSvc := core.New(proactiveLiveFinder{model: model}, reg, store)
	svc := &Service{llm: llmSvc, conversation: store}
	percent := 1.0
	query := &QueryInput{ConversationID: meta.ConversationID, Agent: &agentproto.Agent{ContextCompactionPercent: &percent}}
	makeInput := func(prompt string) *core.GenerateInput {
		current, err := store.GetConversation(ctx, meta.ConversationID, apiconv.WithIncludeToolCall(true))
		require.NoError(t, err)
		history, err := svc.buildHistory(ctx, current.GetTranscript())
		require.NoError(t, err)
		return &core.GenerateInput{ModelSelection: llm.ModelSelection{Model: "openai_gpt5_nano_proactive_private", Options: &llm.Options{MaxTokens: 8192, Reasoning: &llm.Reasoning{Effort: "low"}}}, UserID: "private-test", Prompt: &binding.Prompt{Text: prompt}, SystemPrompt: &binding.Prompt{Text: "Do not execute completed operations again. Stating or quoting completed operation IDs is permitted and required when the user asks."}, Binding: &binding.Binding{History: history}}
	}
	svc.orchestrator = reactor.New(llmSvc, reg, store, nil, func(_ context.Context, _ *apiconv.Conversation, instruction string) (*core.GenerateInput, error) {
		return makeInput(instruction), nil
	})
	gen := makeInput(*latest.Content)
	next, _, err := svc.prepareProactiveCompaction(ctx, query, gen, nil)
	require.NoError(t, err)
	t.Logf("exact input-token counts after compaction attempt: %v", model.counts)
	require.GreaterOrEqual(t, len(model.counts), 2)
	require.GreaterOrEqual(t, model.counts[0], 4000)
	require.Greater(t, reg.effects, 0)
	afterCompaction := reg.effects
	fresh, err := store.GetConversation(ctx, meta.ConversationID, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	archived, summaries := 0, 0
	latestPreserved := false
	for _, turn := range fresh.GetTranscript() {
		for _, m := range turn.Message {
			if m.Archived != nil && *m.Archived == 1 {
				archived++
			}
			if m.Status != nil && *m.Status == "summary" {
				summaries++
				if m.Content != nil {
					t.Logf("persisted handoff: %s", *m.Content)
				}
			}
			if m.Id == latest.Id && (m.Archived == nil || *m.Archived == 0) {
				latestPreserved = true
			}
		}
	}
	require.Greater(t, archived, 0)
	require.Greater(t, summaries, 0)
	require.True(t, latestPreserved)
	// Immutable-only changes do not rearm the already-compacted state.
	_, _, err = svc.prepareProactiveCompaction(next, query, makeInput(*latest.Content), nil)
	require.NoError(t, err)
	require.Equal(t, afterCompaction, reg.effects)
	out := &core.GenerateOutput{}
	_, err = svc.orchestrator.Run(next, gen, out)
	require.NoError(t, err)
	require.Contains(t, out.Content, "SILVER-OTTER-718")
	require.Contains(t, out.Content, "lookup-done-718")
	require.Equal(t, afterCompaction, reg.effects)
	fresh, err = store.GetConversation(ctx, meta.ConversationID, apiconv.WithIncludeModelCall(true))
	require.NoError(t, err)
	require.False(t, recovery.NeedsFullHistory(fresh), "fresh provider response must clear the durable barrier")
	next = ctx // A new request must not depend on the prior in-memory flag.
	// A second user turn exercises continuation from the compact handoff.
	meta2 := requestctx.TurnMeta{ConversationID: meta.ConversationID, TurnID: "private-live-nano-turn-2"}
	turn2 := apiconv.NewTurn()
	turn2.SetId(meta2.TurnID)
	turn2.SetConversationID(meta2.ConversationID)
	turn2.SetStatus("running")
	require.NoError(t, store.PatchTurn(ctx, turn2))
	latest2, err := apiconv.AddMessage(ctx, store, &meta2, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithStatus("completed"), apiconv.WithContent("Quote the exact project codename and the completed lookup operation ID. Reporting an operation ID is not executing that operation; quoting prior operation IDs is explicitly permitted. Answer those two strings only. Do not invoke tools."))
	require.NoError(t, err)
	ctx = requestctx.WithTurnMeta(ctx, meta2)
	next = requestctx.WithTurnMeta(next, meta2)
	second := makeInput(*latest2.Content)
	next, _, err = svc.prepareProactiveCompaction(next, query, second, nil)
	require.NoError(t, err)
	out2 := &core.GenerateOutput{}
	_, err = svc.orchestrator.Run(next, second, out2)
	require.NoError(t, err)
	require.Contains(t, out2.Content, "SILVER-OTTER-718")
	require.Contains(t, out2.Content, "lookup-done-718")
	query.Agent.ContextCompactionPercent = nil
	beforeOff := len(model.counts)
	_, _, err = svc.prepareProactiveCompaction(next, query, makeInput(*latest2.Content), nil)
	require.NoError(t, err)
	require.Equal(t, beforeOff, len(model.counts))
	report, _ := json.Marshal(map[string]interface{}{"store": storeKind, "model": "gpt-5-nano", "capacity": 400000, "thresholdPercent": 1, "inputTokenCounts": model.counts, "archivedMessages": archived, "summaryMessages": summaries, "latestUserPreserved": latestPreserved, "removalEffects": reg.effects, "firstContinuation": out.Content, "secondTurnContinuation": out2.Content, "nilThresholdExtraCountCalls": 0})
	t.Log(string(report))
}
