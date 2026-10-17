package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	agproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	intent "github.com/viant/agently-core/protocol/intent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	intakesvc "github.com/viant/agently-core/service/intake"
	"github.com/viant/agently-core/service/reactor"
)

type prefetchRegistry struct {
	staticRegistry
	execute func(context.Context, string, map[string]interface{}) (string, error)
	calls   atomic.Int32
}

func (r *prefetchRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	r.calls.Add(1)
	return r.execute(ctx, name, args)
}

func prefetchInput() *QueryInput {
	return &QueryInput{Query: "original request", ConversationID: "prefetch-conversation", MessageID: "prefetch-turn", UserId: "reader", Agent: &agproto.Agent{
		Identity: agproto.Identity{ID: "reader-agent"}, Intake: agproto.Intake{Enabled: true},
		Bootstrap: agproto.Bootstrap{ToolCalls: []agproto.BootstrapToolCall{{ID: "request-reference", Tool: "reference:find", Prefetch: true,
			Args: map[string]interface{}{"query": "{{query}}", "user": "{{userId}}", "nested": map[string]interface{}{"limit": 3, "labels": []string{"one"}}}, Required: true}}},
	}}
}
func prefetchContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return runtimerequestctx.WithTurnMeta(authctx.WithCanonicalUserID(ctx, "actor"), runtimerequestctx.TurnMeta{ConversationID: "prefetch-conversation", TurnID: "prefetch-turn", Assistant: "reader-agent"})
}
func awaitPrefetch(t *testing.T, ctx context.Context, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestBootstrapPrefetch_SnapshotsAndInjectsOnce(t *testing.T) {
	ctx := prefetchContext(t)
	started, release := make(chan struct{}), make(chan struct{})
	received := make(chan map[string]interface{}, 1)
	reg := &prefetchRegistry{execute: func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		require.Equal(t, "actor", authctx.CanonicalUserID(ctx))
		tm, _ := runtimerequestctx.TurnMetaFromContext(ctx)
		require.Equal(t, "prefetch-turn", tm.TurnID)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		received <- args
		return "reference context", nil
	}}
	svc := &Service{registry: reg, intakeSvc: intakesvc.New(nil)}
	input := prefetchInput()
	next, join, cancel := svc.startBootstrapPrefetch(ctx, input)
	defer cancel()
	awaitPrefetch(t, ctx, started)
	// Intake may mutate request fields and caller-owned argument containers while
	// a prefetched executor is active; its immutable snapshot stays unchanged.
	input.Query = "changed by intake"
	input.Agent.Bootstrap.ToolCalls[0].Args["nested"].(map[string]interface{})["limit"] = 99
	input.Agent.Bootstrap.ToolCalls[0].Args["nested"].(map[string]interface{})["labels"].([]string)[0] = "changed"
	close(release)
	join()
	args := <-received
	require.Equal(t, "original request", args["query"])
	require.Equal(t, 3, args["nested"].(map[string]interface{})["limit"])
	require.Equal(t, []string{"one"}, args["nested"].(map[string]interface{})["labels"])
	// Binding consumes only the exact original request/configuration scope.
	original := prefetchInput()
	b := &binding.Binding{}
	require.NoError(t, svc.appendBootstrapSystemDocuments(next, original, b))
	require.NoError(t, svc.appendBootstrapSystemDocuments(next, original, b))
	require.EqualValues(t, 1, reg.calls.Load())
	require.Len(t, b.SystemDocuments.Items, 1)
	require.Contains(t, b.SystemDocuments.Items[0].PageContent, "reference context")
	require.Equal(t, 99, input.Agent.Bootstrap.ToolCalls[0].Args["nested"].(map[string]interface{})["limit"])
}

func TestBootstrapPrefetch_FailuresRetainedAtBindingAndNotRetried(t *testing.T) {
	for _, required := range []bool{true, false} {
		t.Run(map[bool]string{true: "required", false: "optional"}[required], func(t *testing.T) {
			ctx := prefetchContext(t)
			failure := errors.New("reference unavailable")
			reg := &prefetchRegistry{execute: func(context.Context, string, map[string]interface{}) (string, error) { return "", failure }}
			svc := &Service{registry: reg, intakeSvc: intakesvc.New(nil)}
			input := prefetchInput()
			input.Agent.Bootstrap.ToolCalls[0].Required = required
			next, join, cancel := svc.startBootstrapPrefetch(ctx, input)
			defer cancel()
			join()
			for i := 0; i < 2; i++ {
				b := &binding.Binding{}
				err := svc.appendBootstrapSystemDocuments(next, input, b)
				if required {
					require.ErrorIs(t, err, failure)
				} else {
					require.NoError(t, err)
				}
				require.Empty(t, b.SystemDocuments.Items)
			}
			require.EqualValues(t, 1, reg.calls.Load())
		})
	}
}

func TestBootstrapPrefetch_ProfileDeferredAndDefaultsUnchanged(t *testing.T) {
	for _, mode := range []string{"unmarked", "disabled", "unavailable", "selected", "direct", "caller", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			ctx := prefetchContext(t)
			input := prefetchInput()
			reg := &prefetchRegistry{execute: func(context.Context, string, map[string]interface{}) (string, error) { return "reference context", nil }}
			svc := &Service{registry: reg, intakeSvc: intakesvc.New(nil)}
			switch mode {
			case "unmarked":
				input.Agent.Bootstrap.ToolCalls[0].Prefetch = false
			case "disabled":
				input.Agent.Intake.Enabled = false
			case "unavailable":
				svc.intakeSvc = nil
			case "selected":
				input.PromptProfileId = "selected-profile"
			case "direct":
				input.Context = map[string]interface{}{intakesvc.ContextKey: &intakesvc.Context{DirectAction: intakesvc.DirectActionContext{ToolName: "view:open", AssistantText: "opened", Input: map[string]interface{}{}}}}
			case "caller":
				input.Context = map[string]interface{}{intakesvc.ContextKey: &intakesvc.Context{Routing: intakesvc.RoutingContext{Source: intakesvc.SourceCallerProvided}}}
			case "unsupported":
				input.Agent.Bootstrap.ToolCalls[0].Inject.As = "unsupported"
				input.Agent.Bootstrap.ToolCalls[0].Required = false
			}
			profileCall := agproto.BootstrapToolCall{ID: "profile-only", Tool: "profile:read", Prefetch: true}
			ctx = withSelectedPromptProfile(ctx, &intent.Profile{ID: "selected-profile", Bootstrap: []agproto.BootstrapToolCall{profileCall}})
			next, join, cancel := svc.startBootstrapPrefetch(ctx, input)
			defer cancel()
			join()
			require.EqualValues(t, 0, reg.calls.Load())
			require.NoError(t, svc.appendBootstrapSystemDocuments(next, input, &binding.Binding{}))
			expected := int32(2)
			if mode == "unsupported" {
				expected = 1
			}
			require.Equal(t, expected, reg.calls.Load())
		})
	}
}

func TestBootstrapPrefetch_NestedRequestsCannotConsumeParentFutures(t *testing.T) {
	ctx := prefetchContext(t)
	reg := &prefetchRegistry{execute: func(context.Context, string, map[string]interface{}) (string, error) { return "reference context", nil }}
	svc := &Service{registry: reg, intakeSvc: intakesvc.New(nil)}
	input := prefetchInput()
	parent, join, cancel := svc.startBootstrapPrefetch(ctx, input)
	defer cancel()
	join()
	// New user, conversation and turn; same agent and authored call.
	child := prefetchInput()
	child.UserId = "different-reader"
	child.ConversationID = "child-conversation"
	childCtx := runtimerequestctx.WithTurnMeta(parent, runtimerequestctx.TurnMeta{ConversationID: child.ConversationID, TurnID: "child-turn", Assistant: child.Agent.ID})
	next, childJoin, childCancel := svc.startBootstrapPrefetch(childCtx, child)
	defer childCancel()
	childJoin()
	require.NoError(t, svc.appendBootstrapSystemDocuments(next, child, &binding.Binding{}))
	require.EqualValues(t, 2, reg.calls.Load())
	// A new turn with prefetch disabled must also mask an inherited batch.
	child.Agent.Intake.Enabled = false
	grandCtx := runtimerequestctx.WithTurnMeta(next, runtimerequestctx.TurnMeta{ConversationID: child.ConversationID, TurnID: "grandchild-turn", Assistant: child.Agent.ID})
	masked, done, stop := svc.startBootstrapPrefetch(grandCtx, child)
	defer stop()
	done()
	require.NoError(t, svc.appendBootstrapSystemDocuments(masked, child, &binding.Binding{}))
	require.EqualValues(t, 3, reg.calls.Load())
}

func TestBootstrapPrefetch_CancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{true: "deadline", false: "cancel"}[deadline], func(t *testing.T) {
			parent := prefetchContext(t)
			ctx, cancel := context.WithCancel(parent)
			defer cancel()
			started, finished := make(chan struct{}), make(chan struct{})
			reg := &prefetchRegistry{execute: func(ctx context.Context, _ string, _ map[string]interface{}) (string, error) {
				close(started)
				defer close(finished)
				if deadline {
					_, ok := ctx.Deadline()
					require.True(t, ok)
				}
				<-ctx.Done()
				return "", ctx.Err()
			}}
			svc := &Service{registry: reg, intakeSvc: intakesvc.New(nil), defaults: &config.Defaults{ToolCallTimeoutSec: 1}}
			input := prefetchInput()
			next, join, stop := svc.startBootstrapPrefetch(ctx, input)
			defer stop()
			awaitPrefetch(t, parent, started)
			if deadline {
				awaitPrefetch(t, parent, finished)
			} else {
				cancel()
			}
			join()
			awaitPrefetch(t, parent, finished)
			expected := context.Canceled
			if deadline {
				expected = context.DeadlineExceeded
			}
			require.ErrorIs(t, svc.appendBootstrapSystemDocuments(next, input, &binding.Binding{}), expected)
			require.EqualValues(t, 1, reg.calls.Load())
		})
	}
}

type prefetchBarrierModel struct {
	toolStarted   <-chan struct{}
	intakeEntered chan struct{}
	releaseIntake <-chan struct{}
	mainEntered   chan struct{}
	count         atomic.Int32
	mainRequest   *llm.GenerateRequest
}

func (m *prefetchBarrierModel) Implements(string) bool { return false }
func (m *prefetchBarrierModel) Generate(ctx context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	content := "finished"
	if m.count.Add(1) == 1 {
		select {
		case <-m.toolStarted:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		close(m.intakeEntered)
		select {
		case <-m.releaseIntake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		content = `{"classification":{"intent":"analysis","confidence":0.99},"routing":{"mode":"execute"}}`
	} else {
		m.mainRequest = request
		close(m.mainEntered)
	}
	return &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: content}, FinishReason: "stop"}}, Model: "mock-model"}, nil
}

func TestQuery_BootstrapPrefetchOverlapsActualIntakeAfterUserPersistence(t *testing.T) {
	ctx := prefetchContext(t)
	store := convmem.New()
	conv := apiconv.NewConversation()
	conv.SetId("prefetch-conversation")
	require.NoError(t, store.PatchConversations(ctx, conv))
	started, releaseTool, releaseIntake := make(chan struct{}), make(chan struct{}), make(chan struct{})
	reg := &prefetchRegistry{execute: func(ctx context.Context, _ string, _ map[string]interface{}) (string, error) {
		current, err := store.GetConversation(ctx, "prefetch-conversation", apiconv.WithIncludeTranscript(true))
		if err != nil {
			return "", err
		}
		if len(current.GetTranscript()) == 0 || len(current.GetTranscript()[0].Message) == 0 {
			return "", errors.New("bootstrap started before user persistence")
		}
		close(started)
		select {
		case <-releaseTool:
			return "reference context", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	model := &prefetchBarrierModel{toolStarted: started, intakeEntered: make(chan struct{}), releaseIntake: releaseIntake, mainEntered: make(chan struct{})}
	llmSvc := core.New(&plannerRoutingFinder{model: model}, reg, store)
	svc := &Service{llm: llmSvc, conversation: store, orchestrator: reactor.New(llmSvc, nil, store, nil, nil), defaults: &config.Defaults{}, registry: reg, intakeSvc: intakesvc.New(llmSvc)}
	input := prefetchInput()
	input.Agent.ModelSelection = llm.ModelSelection{Model: "mock-model"}
	input.Agent.Prompt = &binding.Prompt{Text: "{{ .Task.Query }}"}
	input.Agent.Intake.Model = "mock-model"
	done := make(chan error, 1)
	output := &QueryOutput{}
	go func() { done <- svc.Query(ctx, input, output) }()
	awaitPrefetch(t, ctx, model.intakeEntered)
	current, err := store.GetConversation(ctx, input.ConversationID, apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.NotEmpty(t, current.GetTranscript())
	require.NotEmpty(t, current.GetTranscript()[0].Message)
	close(releaseIntake)
	// Main cannot start while the bootstrap result is still unavailable.
	select {
	case <-model.mainEntered:
		t.Fatal("main started before bootstrap joined")
	default:
	}
	close(releaseTool)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Equal(t, "finished", output.Content)
	require.EqualValues(t, 1, reg.calls.Load())
	require.EqualValues(t, 2, model.count.Load())
	text := model.mainRequest.Instructions
	for _, message := range model.mainRequest.Messages {
		text += llm.MessageText(message)
	}
	require.Equal(t, 1, strings.Count(text, "reference context"))
}

func TestBootstrapPrefetch_AuthScopeAndExpandedArgsMustStillMatch(t *testing.T) {
	for _, change := range []string{"effective", "canonical", "provider", "query", "user", "conversation", "turn"} {
		t.Run(change, func(t *testing.T) {
			ctx := prefetchContext(t)
			input := prefetchInput()
			failure := errors.New("not cached")
			reg := &prefetchRegistry{execute: func(context.Context, string, map[string]interface{}) (string, error) { return "", failure }}
			svc := &Service{registry: reg, intakeSvc: intakesvc.New(nil)}
			next, join, cancel := svc.startBootstrapPrefetch(ctx, input)
			defer cancel()
			join()
			switch change {
			case "effective":
				next = authctx.WithUserInfo(next, &authctx.UserInfo{Subject: "another-actor"})
			case "canonical":
				next = authctx.WithCanonicalUserID(next, "another-canonical")
			case "provider":
				next = authctx.WithProvider(next, "another-provider")
			case "query":
				input.Query = "changed query"
			case "user":
				input.UserId = "another-reader"
			case "conversation":
				input.ConversationID = "another-conversation"
			case "turn":
				next = runtimerequestctx.WithTurnMeta(next, runtimerequestctx.TurnMeta{TurnID: "another-turn"})
			}
			require.ErrorIs(t, svc.appendBootstrapSystemDocuments(next, input, &binding.Binding{}), failure)
			require.EqualValues(t, 2, reg.calls.Load())
		})
	}
}

func TestBootstrapPrefetch_OnlyAgentOwnedCallsStartBeforeBinding(t *testing.T) {
	ctx := prefetchContext(t)
	input := prefetchInput()
	reg := &prefetchRegistry{execute: func(context.Context, string, map[string]interface{}) (string, error) { return "context", nil }}
	svc := &Service{registry: reg, intakeSvc: intakesvc.New(nil)}
	ctx = withSelectedPromptProfile(ctx, &intent.Profile{ID: "profile", Bootstrap: []agproto.BootstrapToolCall{{ID: "profile-only", Tool: "reference:profile", Prefetch: true}}})
	next, join, cancel := svc.startBootstrapPrefetch(ctx, input)
	defer cancel()
	join()
	require.EqualValues(t, 1, reg.calls.Load())
	require.NoError(t, svc.appendBootstrapSystemDocuments(next, input, &binding.Binding{}))
	require.EqualValues(t, 2, reg.calls.Load())
}

type bootstrapTypedArgs struct{ Values map[string]interface{} }

func TestBootstrapPrefetch_UnsupportedTypedArgumentsStaySerial(t *testing.T) {
	ctx := prefetchContext(t)
	input := prefetchInput()
	input.Agent.Bootstrap.ToolCalls[0].Args["typed"] = &bootstrapTypedArgs{Values: map[string]interface{}{"a": 1}}
	reg := &prefetchRegistry{execute: func(_ context.Context, _ string, args map[string]interface{}) (string, error) {
		require.IsType(t, &bootstrapTypedArgs{}, args["typed"])
		return "context", nil
	}}
	svc := &Service{registry: reg, intakeSvc: intakesvc.New(nil)}
	next, join, cancel := svc.startBootstrapPrefetch(ctx, input)
	defer cancel()
	join()
	require.EqualValues(t, 0, reg.calls.Load())
	require.NoError(t, svc.appendBootstrapSystemDocuments(next, input, &binding.Binding{}))
	require.EqualValues(t, 1, reg.calls.Load())
}

func TestBootstrapPrefetch_ConfigIsExplicitAndDefaultsOff(t *testing.T) {
	var call agproto.BootstrapToolCall
	require.NoError(t, json.Unmarshal([]byte(`{"id":"reference","tool":"reference:find"}`), &call))
	require.False(t, call.Prefetch)
	raw, err := json.Marshal(call)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "prefetch")
	require.NoError(t, json.Unmarshal([]byte(`{"prefetch":true}`), &call))
	require.True(t, call.Prefetch)
}

func TestQuery_BootstrapPrefetchFailureUsesExistingBindingRules(t *testing.T) {
	for _, required := range []bool{true, false} {
		t.Run(map[bool]string{true: "required", false: "optional"}[required], func(t *testing.T) {
			ctx := prefetchContext(t)
			store := convmem.New()
			conv := apiconv.NewConversation()
			conv.SetId("prefetch-conversation")
			require.NoError(t, store.PatchConversations(ctx, conv))
			started, released := make(chan struct{}), make(chan struct{})
			close(released)
			failure := errors.New("reference read failed")
			reg := &prefetchRegistry{execute: func(context.Context, string, map[string]interface{}) (string, error) {
				close(started)
				return "", failure
			}}
			model := &prefetchBarrierModel{toolStarted: started, intakeEntered: make(chan struct{}), releaseIntake: released, mainEntered: make(chan struct{})}
			llmSvc := core.New(&plannerRoutingFinder{model: model}, reg, store)
			svc := &Service{llm: llmSvc, conversation: store, orchestrator: reactor.New(llmSvc, nil, store, nil, nil), defaults: &config.Defaults{}, registry: reg, intakeSvc: intakesvc.New(llmSvc)}
			input := prefetchInput()
			input.Agent.ModelSelection = llm.ModelSelection{Model: "mock-model"}
			input.Agent.Prompt = &binding.Prompt{Text: "{{ .Task.Query }}"}
			input.Agent.Intake.Model = "mock-model"
			input.Agent.Bootstrap.ToolCalls[0].Required = required
			output := &QueryOutput{}
			err := svc.Query(ctx, input, output)
			if required {
				require.ErrorIs(t, err, failure)
				require.EqualValues(t, 1, model.count.Load())
			} else {
				require.NoError(t, err)
				require.Equal(t, "finished", output.Content)
				require.EqualValues(t, 2, model.count.Load())
			}
			require.EqualValues(t, 1, reg.calls.Load())
		})
	}
}

func TestBootstrapPrefetch_SuccessCacheIsIsolatedByAuthIdentity(t *testing.T) {
	for _, change := range []string{"effective", "canonical", "provider"} {
		t.Run(change, func(t *testing.T) {
			ctx := prefetchContext(t)
			input := prefetchInput()
			reg := &prefetchRegistry{execute: func(ctx context.Context, _ string, _ map[string]interface{}) (string, error) {
				return authctx.EffectiveUserID(ctx) + "|" + authctx.CanonicalUserID(ctx) + "|" + authctx.Provider(ctx), nil
			}}
			svc := &Service{registry: reg, intakeSvc: intakesvc.New(nil)}
			next, join, cancel := svc.startBootstrapPrefetch(ctx, input)
			defer cancel()
			join()
			original, err := svc.bootstrapSystemDocument(next, input, input.Agent.Bootstrap.ToolCalls[0])
			require.NoError(t, err)
			switch change {
			case "effective":
				next = authctx.WithUserInfo(next, &authctx.UserInfo{Subject: "another-actor"})
			case "canonical":
				next = authctx.WithCanonicalUserID(next, "another-canonical")
			case "provider":
				next = authctx.WithProvider(next, "another-provider")
			}
			updated, err := svc.bootstrapSystemDocument(next, input, input.Agent.Bootstrap.ToolCalls[0])
			require.NoError(t, err)
			require.NotEqual(t, original.PageContent, updated.PageContent)
			require.EqualValues(t, 2, reg.calls.Load())
			// Reusing the new identity is still a hit in the original success cache.
			_, err = svc.bootstrapSystemDocument(next, input, input.Agent.Bootstrap.ToolCalls[0])
			require.NoError(t, err)
			require.EqualValues(t, 2, reg.calls.Load())
		})
	}
}
