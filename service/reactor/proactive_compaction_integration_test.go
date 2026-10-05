package reactor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/mcp/manager"
	messagesvc "github.com/viant/agently-core/protocol/tool/service/message"
	"github.com/viant/agently-core/runtime/recovery"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
)

// Real registry display names and real message/remove validation are essential:
// a canonical-name fake registry hid the production message_remove lookup bug.
func TestCompactProactivelyRealMessageRegistry(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, invalid := range []bool{false, true} {
			t.Run(map[bool]string{false: "generate", true: "stream"}[stream]+"/"+map[bool]string{false: "valid", true: "invalid"}[invalid], func(t *testing.T) {
				store := convmem.New()
				meta := requestctx.TurnMeta{ConversationID: "private", TurnID: "history"}
				ctx := requestctx.WithTurnMeta(requestctx.WithConversationID(context.Background(), meta.ConversationID), meta)
				conv := apiconv.NewConversation()
				conv.SetId(meta.ConversationID)
				require.NoError(t, store.PatchConversations(ctx, conv))
				turn := apiconv.NewTurn()
				turn.SetId(meta.TurnID)
				turn.SetConversationID(meta.ConversationID)
				turn.SetStatus("succeeded")
				require.NoError(t, store.PatchTurn(ctx, turn))
				old, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithContent("Preserve exact project SILVER-OTTER-718."))
				require.NoError(t, err)
				latest, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithContent("Continue."))
				require.NoError(t, err)
				mgr, err := manager.New(manager.NewRepoProvider())
				require.NoError(t, err)
				reg, err := registry.NewWithManager(mgr)
				require.NoError(t, err)
				require.NoError(t, reg.AddInternalService(messagesvc.New(store)))
				summary := "Project SILVER-OTTER-718."
				if invalid {
					summary = ""
				}
				call := llm.NewToolCall("compact-call", "message-remove", map[string]interface{}{"tuples": []interface{}{map[string]interface{}{"messageIds": []interface{}{old.Id}, "summary": summary}}}, "")
				model := &plannedCallsModel{stream: stream, response: &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}}}}
				llmService := core.New(plannedCallsFinder{model: model}, reg, store)
				sharedOptions := &llm.Options{Mode: "task"}
				service := New(llmService, reg, store, nil, func(context.Context, *apiconv.Conversation, string) (*core.GenerateInput, error) {
					return &core.GenerateInput{ModelSelection: llm.ModelSelection{Model: "fixture", Options: sharedOptions}, Prompt: &binding.Prompt{Text: "Compact history"}, Binding: &binding.Binding{}}, nil
				})
				fresh, err := store.GetConversation(ctx, meta.ConversationID)
				require.NoError(t, err)
				err = service.CompactProactively(ctx, fresh, &core.GenerateInput{}, &recovery.CompactionScope{ConversationID: meta.ConversationID, EligibleIDs: map[string]bool{old.Id: true}}, 9000, 10000, 80)
				if invalid {
					require.ErrorContains(t, err, "handoff summary")
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, 1, model.calls, "proactive compaction must not regenerate stale input")
				require.Equal(t, "task", sharedOptions.Mode, "recovery mode must not leak into future turns")
				require.Equal(t, llm.ToolChoice{}, sharedOptions.ToolChoice, "forced removal must not leak into future turns")
				require.Empty(t, sharedOptions.Tools)
				fresh, err = store.GetConversation(ctx, meta.ConversationID)
				require.NoError(t, err)
				summaries := 0
				for _, turn := range fresh.GetTranscript() {
					for _, message := range turn.Message {
						if message.Id == old.Id {
							require.Equal(t, !invalid, message.Archived != nil && *message.Archived == 1)
						}
						if message.Id == latest.Id {
							require.True(t, message.Archived == nil || *message.Archived == 0)
						}
						if message.Status != nil && *message.Status == "summary" {
							summaries++
							require.Equal(t, recovery.ProactiveSummaryMarker, *message.ContextSummary)
						}
					}
				}
				require.Equal(t, map[bool]int{false: 1, true: 0}[invalid], summaries)
			})
		}
	}
}

type canceledCompactionModel struct {
	*plannedCallsModel
	cancel context.CancelFunc
}

func (m *canceledCompactionModel) Generate(ctx context.Context, _ *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.calls++
	m.cancel()
	return nil, ctx.Err()
}
func TestCompactProactivelyCancellationDoesNotSucceed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "private", TurnID: "turn"})
	model := &canceledCompactionModel{plannedCallsModel: &plannedCallsModel{}, cancel: cancel}
	store := convmem.New()
	mgr, err := manager.New(manager.NewRepoProvider())
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(messagesvc.New(store)))
	service := New(core.New(plannedCallsFinder{model: model}, reg, store), reg, store, nil, func(context.Context, *apiconv.Conversation, string) (*core.GenerateInput, error) {
		return &core.GenerateInput{ModelSelection: llm.ModelSelection{Model: "fixture"}, Prompt: &binding.Prompt{Text: "Compact"}, Binding: &binding.Binding{}}, nil
	})
	err = service.CompactProactively(ctx, &apiconv.Conversation{Id: "private"}, &core.GenerateInput{}, &recovery.CompactionScope{ConversationID: "private", EligibleIDs: map[string]bool{"old": true}}, 9000, 10000, 80)
	require.True(t, errors.Is(err, context.Canceled), "canceled partial reactor plan must not authorize fresh continuation")
	require.Equal(t, 1, model.calls)
}
