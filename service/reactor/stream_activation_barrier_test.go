package reactor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/protocol/binding"
	memory "github.com/viant/agently-core/runtime/requestctx"
	core "github.com/viant/agently-core/service/core"
	modelcall "github.com/viant/agently-core/service/core/modelcall"
)

type activationStreamModel struct {
	err    error
	cancel context.CancelFunc
	calls  []llm.ToolCall
	closed chan struct{}
}

func (*activationStreamModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return nil, errors.New("fixture requires streaming")
}
func (*activationStreamModel) Implements(feature string) bool { return feature == base.CanStream }
func (m *activationStreamModel) Stream(ctx context.Context, request *llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	observer := modelcall.ObserverFromContext(ctx)
	if observer != nil {
		var err error
		ctx, err = observer.OnCallStart(ctx, modelcall.Info{Provider: "fixture", Model: "fixture", LLMRequest: request, StartedAt: time.Now()})
		if err != nil {
			return nil, err
		}
	}
	events := make(chan llm.StreamEvent)
	go func() {
		defer close(events)
		defer close(m.closed)
		response := &llm.GenerateResponse{ResponseID: "fixture-response", Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: m.calls}, FinishReason: "tool_calls"}}}
		defer func() {
			if observer == nil {
				return
			}
			info := modelcall.Info{Provider: "fixture", Model: "fixture", LLMResponse: response, CompletedAt: time.Now(), FinishReason: "tool_calls"}
			if cause := errors.Join(m.err, ctx.Err()); cause != nil {
				info.Err = cause.Error()
			}
			_ = observer.OnCallEnd(ctx, info)
		}()
		send := func(event llm.StreamEvent) bool {
			select {
			case events <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for _, call := range m.calls {
			if !send(llm.StreamEvent{Kind: llm.StreamEventToolCallStarted, ResponseID: "fixture-response", ToolCallID: call.ID, ToolName: call.Name}) {
				return
			}
			for _, chunk := range []string{`{"value":`, `"fixture"}`} {
				if !send(llm.StreamEvent{Kind: llm.StreamEventToolCallDelta, ResponseID: "fixture-response", ToolCallID: call.ID, ToolName: call.Name, Delta: chunk}) {
					return
				}
			}
			if !send(llm.StreamEvent{Kind: llm.StreamEventToolCallCompleted, ResponseID: "fixture-response", ToolCallID: call.ID, ToolName: call.Name, Arguments: call.Arguments}) {
				return
			}
			if m.cancel != nil && call.ID == "activate" {
				m.cancel()
				return
			}
		}
		if m.err != nil {
			send(llm.StreamEvent{Err: m.err})
		} else {
			send(llm.StreamEvent{Kind: llm.StreamEventTurnCompleted, Response: response})
		}
	}()
	return events, nil
}

type activationPersistenceClient struct {
	apiconv.Client
	persisted chan struct{}
	once      sync.Once
}

func (c *activationPersistenceClient) PatchModelCall(ctx context.Context, call *apiconv.MutableModelCall) error {
	if err := c.Client.PatchModelCall(ctx, call); err != nil {
		return err
	}
	if call.Has != nil && call.Has.Status && call.Status != "thinking" && call.Status != "running" {
		c.once.Do(func() { close(c.persisted) })
	}
	return nil
}

type activationOrderRegistry struct {
	reactorScriptedRegistry
	muOrder                  sync.Mutex
	order                    []string
	persisted                <-chan struct{}
	streamClosed             <-chan struct{}
	startedBeforePersistence bool
	activationBeforeClose    bool
	cancelOnActivation       context.CancelFunc
}

func (r *activationOrderRegistry) Execute(ctx context.Context, name string, _ map[string]interface{}) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.muOrder.Lock()
	defer r.muOrder.Unlock()
	select {
	case <-r.persisted:
	default:
		r.startedBeforePersistence = true
	}
	if r.streamClosed != nil && name != "fixture/before" {
		select {
		case <-r.streamClosed:
		default:
			r.activationBeforeClose = true
		}
	}
	r.order = append(r.order, name)
	if name == "llm/skills:activate" && r.cancelOnActivation != nil {
		r.cancelOnActivation()
		return "", ctx.Err()
	}
	return "fixture result", nil
}

func TestStreamActivationDrainsArgumentsBeforeOrderedToolDispatch(t *testing.T) {
	for _, name := range []string{"completed", "stream-failure", "canceled"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "activation-conversation", TurnID: "activation-turn"})
			store := convmem.New()
			conversation := apiconv.NewConversation()
			conversation.SetId("activation-conversation")
			require.NoError(t, store.PatchConversations(ctx, conversation))
			model := &activationStreamModel{closed: make(chan struct{}), calls: []llm.ToolCall{
				{ID: "before", Name: "fixture/before", Arguments: map[string]interface{}{"value": "fixture"}},
				{ID: "activate", Name: "llm/skills:activate", Arguments: map[string]interface{}{"value": "fixture"}},
				{ID: "after", Name: "fixture/after", Arguments: map[string]interface{}{"value": "fixture"}},
			}}
			cause := errors.New("synthetic stream failure")
			if name == "stream-failure" {
				model.err = cause
			}
			if name == "canceled" {
				model.cancel = cancel
			}
			client := &activationPersistenceClient{Client: store, persisted: make(chan struct{})}
			registry := &activationOrderRegistry{persisted: client.persisted, streamClosed: model.closed}
			service := New(core.New(plannedCallsFinder{model: model}, registry, client), registry, client, nil, nil)
			input := &core.GenerateInput{ModelSelection: llm.ModelSelection{Model: "fixture"}, UserID: "fixture-user", Prompt: &binding.Prompt{Text: "fixture"}, Binding: &binding.Binding{}}
			plan, err := service.Run(ctx, input, &core.GenerateOutput{})
			if name == "canceled" {
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				require.NoError(t, err)
				require.Len(t, plan.Steps, 2)
				require.Empty(t, registry.order, "canceled pending calls must not execute tools")
				results := service.TurnToolResults("activation-turn")
				require.Len(t, results, 2)
				for _, result := range results {
					message, getErr := store.GetMessage(context.Background(), result.ResultMessageID, apiconv.WithIncludeToolCall(true))
					require.NoError(t, getErr)
					require.Equal(t, "canceled", message.ToolMessage[0].ToolCall.Status)
				}
				return
			}
			require.NoError(t, ctx.Err(), "stream consumer waited on activation before consuming the remaining model events")
			if name == "stream-failure" {
				require.ErrorContains(t, err, cause.Error())
			} else {
				require.NoError(t, err)
			}
			require.Len(t, plan.Steps, 3)
			registry.muOrder.Lock()
			require.Equal(t, []string{"fixture/before", "llm/skills:activate", "fixture/after"}, registry.order)
			require.False(t, registry.startedBeforePersistence)
			require.False(t, registry.activationBeforeClose)
			registry.muOrder.Unlock()
			results := service.TurnToolResults("activation-turn")
			require.Len(t, results, 3)
			for _, result := range results {
				require.NotEmpty(t, result.ResultMessageID)
				message, getErr := store.GetMessage(ctx, result.ResultMessageID, apiconv.WithIncludeToolCall(true))
				require.NoError(t, getErr)
				require.Equal(t, "fixture result", message.GetContent())
				require.Equal(t, "completed", message.ToolMessage[0].ToolCall.Status)
			}
		})
	}
}

type activationGenerateModel struct{ plannedCallsModel }

func (m *activationGenerateModel) Generate(ctx context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	observer := modelcall.ObserverFromContext(ctx)
	if observer == nil {
		return m.plannedCallsModel.Generate(ctx, request)
	}
	started := time.Now()
	info := modelcall.Info{Provider: "fixture", Model: "fixture", LLMRequest: request, StartedAt: started}
	ctx, err := observer.OnCallStart(ctx, info)
	if err != nil {
		return nil, err
	}
	response, err := m.plannedCallsModel.Generate(ctx, request)
	if err != nil {
		return nil, err
	}
	info.LLMResponse, info.CompletedAt, info.FinishReason = response, time.Now(), "tool_calls"
	return response, observer.OnCallEnd(ctx, info)
}

func TestNonStreamingActivationDrainsPlannedToolsOnce(t *testing.T) {
	for _, name := range []string{"completed", "canceled"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "activation-conversation", TurnID: "activation-turn"})
			store := convmem.New()
			conversation := apiconv.NewConversation()
			conversation.SetId("activation-conversation")
			require.NoError(t, store.PatchConversations(ctx, conversation))
			model := &activationGenerateModel{plannedCallsModel{response: &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{ID: "before", Name: "fixture/before"},
				{ID: "activate", Name: "llm/skills:activate"},
				{ID: "after", Name: "fixture/after"},
			}}}}}}}
			client := &activationPersistenceClient{Client: store, persisted: make(chan struct{})}
			registry := &activationOrderRegistry{persisted: client.persisted}
			if name == "canceled" {
				registry.cancelOnActivation = cancel
			}
			service := New(core.New(plannedCallsFinder{model: model}, registry, client), registry, client, nil, nil)
			input := &core.GenerateInput{ModelSelection: llm.ModelSelection{Model: "fixture"}, UserID: "fixture-user", Prompt: &binding.Prompt{Text: "fixture"}, Binding: &binding.Binding{}}
			plan, err := service.Run(ctx, input, &core.GenerateOutput{})
			require.NoError(t, err)
			require.Len(t, plan.Steps, 3)
			require.Equal(t, 1, model.calls)
			expectedOrder := []string{"fixture/before", "llm/skills:activate", "fixture/after"}
			expectedStatuses := map[string]string{"before": "completed", "activate": "completed", "after": "completed"}
			if name == "canceled" {
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				expectedOrder = expectedOrder[:2]
				expectedStatuses["activate"], expectedStatuses["after"] = "canceled", "canceled"
			}
			require.Equal(t, expectedOrder, registry.order)
			require.False(t, registry.startedBeforePersistence)
			results := service.TurnToolResults("activation-turn")
			require.Len(t, results, 3)
			for _, result := range results {
				message, err := store.GetMessage(context.Background(), result.ResultMessageID, apiconv.WithIncludeToolCall(true))
				require.NoError(t, err)
				require.Equal(t, expectedStatuses[result.ID], message.ToolMessage[0].ToolCall.Status)
			}
		})
	}
}
