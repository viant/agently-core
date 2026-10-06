package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	turnmodel "github.com/viant/agently-core/model/turn"
	turnqueuemodel "github.com/viant/agently-core/model/turnqueue"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/runtime/streaming"
	goalsys "github.com/viant/agently-core/service/goal"
)

type captureGoalEventPublisher struct {
	mu     sync.Mutex
	events []*streaming.Event
}

func (c *captureGoalEventPublisher) Publish(_ context.Context, event *streaming.Event) error {
	if event != nil {
		c.mu.Lock()
		c.events = append(c.events, event)
		c.mu.Unlock()
	}
	return nil
}

func (c *captureGoalEventPublisher) HasEvent(eventType streaming.EventType) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ev := range c.events {
		if ev != nil && ev.Type == eventType {
			return true
		}
	}
	return false
}

func TestObserveDetachedAsyncGoalCompletion_QueuesContinuationWhenIdle(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	server, err := data.NewRuntimeFromWorkspace(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	dataSvc := data.NewService(server)
	// Resolve cold native readers before the observer timing window, and surface
	// errors that the production observer conservatively refuses to continue on.
	_, err = dataSvc.GetActiveTurn(ctx, &turnmodel.ActiveTurnsInput{ConversationID: "conv-goal", Has: &turnmodel.ActiveTurnsInputHas{ConversationID: true}})
	require.NoError(t, err)
	_, err = dataSvc.CountQueuedTurns(ctx, &turnmodel.QueuedTotalInput{ConversationID: "conv-goal", Has: &turnmodel.QueuedTotalInputHas{ConversationID: true}})
	require.NoError(t, err)

	_, err = dataSvc.PatchConversations(ctx, []*conversationmodel.Conversation{
		conversationmodel.NewMutableConversationView(conversationmodel.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyEvaluate,
	}).Encode()
	require.NoError(t, err)
	goalStore := goalsys.NewStore(server)
	goalID, conversationID, objective, status := "goal-conv-goal", "conv-goal", "finish parser cleanup", "active"
	require.NoError(t, goalStore.(goalsys.Repository).Apply(ctx, goalsys.Mutation{ID: goalID, ConversationID: goalsys.Field[*string]{Present: true, Value: &conversationID}, Objective: goalsys.Field[*string]{Present: true, Value: &objective}, Status: goalsys.Field[*string]{Present: true, Value: &status}, ControllerSpec: goalsys.Field[*string]{Present: true, Value: &spec}}))

	// All three stores share one native runtime. A memory-only turn/message
	// cannot prove the native turn_queue foreign-key and commit contract.
	convClient, err := convservice.New(ctx, server)
	require.NoError(t, err)
	conv := convcli.NewConversation()
	conv.SetId("conv-goal")
	conv.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	// Resolve the controller's remaining cold reads synchronously. The logical
	// callback budget below does not measure first-time component compilation.
	current, err := goalStore.Current(ctx, conversationID)
	require.NoError(t, err)
	require.NotNil(t, current)
	counter, ok := dataSvc.(controllerSignalCounter)
	require.True(t, ok)
	_, err = counter.CountPendingElicitations(ctx, conversationID)
	require.NoError(t, err)
	_, err = counter.CountPendingApprovals(ctx, conversationID)
	require.NoError(t, err)
	_, err = counter.CountControllerTurns(ctx, conversationID)
	require.NoError(t, err)
	_, err = convClient.GetConversation(ctx, conversationID, convcli.WithIncludeTranscript(true))
	require.NoError(t, err)
	// Warm exact native write shapes using an unrelated succeeded turn, not
	// another queued/controller action. This leaves continuation counts at zero.
	warmTurn := convcli.NewTurn()
	warmTurn.SetId("goal-fixture-warm")
	warmTurn.SetConversationID(conversationID)
	warmTurn.SetStatus("succeeded")
	warmTurn.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchTurn(ctx, warmTurn))
	warmMessage := convcli.NewMessage()
	warmMessage.SetId("goal-fixture-warm")
	warmMessage.SetConversationID(conversationID)
	warmMessage.SetTurnID("goal-fixture-warm")
	warmMessage.SetRole("user")
	warmMessage.SetType("task")
	warmMessage.SetContent("fixture initialization")
	warmMessage.SetRawContent("fixture initialization")
	warmMessage.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchMessage(ctx, warmMessage))
	queuePatcher, ok := dataSvc.(interface {
		PatchTurnQueue(context.Context, *turnqueuemodel.TurnQueue) error
	})
	require.True(t, ok)
	warmQueue := &turnqueuemodel.TurnQueue{Has: &turnqueuemodel.TurnQueueHas{}}
	warmQueue.SetId("goal-fixture-warm")
	warmQueue.SetConversationId(conversationID)
	warmQueue.SetTurnId("goal-fixture-warm")
	warmQueue.SetMessageId("goal-fixture-warm")
	warmQueue.SetQueueSeq(1)
	warmQueue.SetStatus("done")
	warmQueue.SetCreatedAt(time.Now())
	warmQueue.SetUpdatedAt(time.Now())
	require.NoError(t, queuePatcher.PatchTurnQueue(ctx, warmQueue))
	queuedBefore, err := dataSvc.CountQueuedTurns(ctx, &turnmodel.QueuedTotalInput{ConversationID: conversationID, Has: &turnmodel.QueuedTotalInputHas{ConversationID: true}})
	require.NoError(t, err)
	require.Zero(t, queuedBefore)
	manager := asynccfg.NewManager()
	pub := &captureGoalEventPublisher{}
	svc := &Service{
		dataService:  dataSvc,
		conversation: convClient,
		asyncManager: manager,
		goalRuntime:  goalsys.NewRuntime(goalStore),
		streamPub:    pub,
	}

	rec, _ := manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "op-1",
		ParentConvID:  "conv-goal",
		ParentTurnID:  "turn-parent",
		ToolCallID:    "tool-call-1",
		ToolMessageID: "tool-msg-1",
		ToolName:      "resources:search",
		ExecutionMode: string(asynccfg.ExecutionModeDetach),
		Status:        "started",
		Message:       "working",
	})
	svc.observeDetachedAsyncGoalCompletion(ctx, rec)

	_, changed := manager.Update(ctx, asynccfg.UpdateInput{
		ID:      "op-1",
		Status:  "completed",
		Message: "finished",
		KeyData: []byte(`{"continuationHint":"Open the refreshed results and continue cleanup."}`),
	})
	require.True(t, changed)

	require.Eventually(t, func() bool {
		got, err := convClient.GetConversation(ctx, "conv-goal", convcli.WithIncludeTranscript(true))
		if err != nil || got == nil {
			return false
		}
		for _, turn := range got.GetTranscript() {
			if turn == nil {
				continue
			}
			for _, msg := range turn.Message {
				if msg == nil || msg.RawContent == nil {
					continue
				}
				if strings.Contains(strings.TrimSpace(*msg.RawContent), "Open the refreshed results and continue cleanup.") {
					return true
				}
			}
		}
		return false
	}, 3*time.Second, 20*time.Millisecond)
	// Observing memory message text preceded native queue persistence in the
	// old fixture. Wait for the real worker's completed operation and assert it.
	require.Eventually(t, func() bool { _, exists := svc.asyncGoalWatches.Load(rec.ID); return !exists }, 3*time.Second, 10*time.Millisecond)
	queued, err := dataSvc.CountQueuedTurns(ctx, &turnmodel.QueuedTotalInput{ConversationID: conversationID, Has: &turnmodel.QueuedTotalInputHas{ConversationID: true}})
	require.NoError(t, err)
	require.Equal(t, 1, queued)
	require.True(t, pub.HasEvent(streaming.EventTypeGoalUpdated))
	require.True(t, pub.HasEvent(streaming.EventTypeGoalControllerScheduled))
}

func TestObserveDetachedAsyncGoalCompletion_RespectsAsyncPolicyWait(t *testing.T) {
	ctx := context.Background()
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	_, err = dataSvc.PatchConversations(ctx, []*conversationmodel.Conversation{
		conversationmodel.NewMutableConversationView(conversationmodel.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyWait,
	}).Encode()
	require.NoError(t, err)
	goalStore := newLinkedGoalStore(t, "goal-conv-goal", "conv-goal", "finish parser cleanup", spec)

	convClient := convmem.New()
	conv := convcli.NewConversation()
	conv.SetId("conv-goal")
	conv.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	manager := asynccfg.NewManager()
	svc := &Service{
		dataService:  dataSvc,
		conversation: convClient,
		asyncManager: manager,
		goalRuntime:  goalsys.NewRuntime(goalStore),
	}

	rec, _ := manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "op-1",
		ParentConvID:  "conv-goal",
		ParentTurnID:  "turn-parent",
		ToolCallID:    "tool-call-1",
		ToolMessageID: "tool-msg-1",
		ToolName:      "resources:search",
		ExecutionMode: string(asynccfg.ExecutionModeDetach),
		Status:        "started",
		Message:       "working",
	})
	svc.observeDetachedAsyncGoalCompletion(ctx, rec)

	_, changed := manager.Update(ctx, asynccfg.UpdateInput{
		ID:      "op-1",
		Status:  "completed",
		Message: "finished",
		KeyData: []byte(`{"continuationHint":"Open the refreshed results and continue cleanup."}`),
	})
	require.True(t, changed)

	time.Sleep(200 * time.Millisecond)
	got, err := convClient.GetConversation(ctx, "conv-goal")
	require.NoError(t, err)
	require.Len(t, got.GetTranscript(), 0)
}

func TestObserveDetachedAsyncGoalCompletion_SuppressesDuplicateQueueingAcrossConcurrentCompletions(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	server, err := data.NewRuntimeFromWorkspace(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	dataSvc := data.NewService(server)
	// Resolve cold native readers before the observer timing window, and surface
	// errors that the production observer conservatively refuses to continue on.
	_, err = dataSvc.GetActiveTurn(ctx, &turnmodel.ActiveTurnsInput{ConversationID: "conv-goal", Has: &turnmodel.ActiveTurnsInputHas{ConversationID: true}})
	require.NoError(t, err)
	_, err = dataSvc.CountQueuedTurns(ctx, &turnmodel.QueuedTotalInput{ConversationID: "conv-goal", Has: &turnmodel.QueuedTotalInputHas{ConversationID: true}})
	require.NoError(t, err)

	_, err = dataSvc.PatchConversations(ctx, []*conversationmodel.Conversation{
		conversationmodel.NewMutableConversationView(conversationmodel.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyEvaluate,
	}).Encode()
	require.NoError(t, err)
	goalStore := goalsys.NewStore(server)
	goalID, conversationID, objective, status := "goal-conv-goal", "conv-goal", "finish parser cleanup", "active"
	require.NoError(t, goalStore.(goalsys.Repository).Apply(ctx, goalsys.Mutation{ID: goalID, ConversationID: goalsys.Field[*string]{Present: true, Value: &conversationID}, Objective: goalsys.Field[*string]{Present: true, Value: &objective}, Status: goalsys.Field[*string]{Present: true, Value: &status}, ControllerSpec: goalsys.Field[*string]{Present: true, Value: &spec}}))

	// All three stores share one native runtime. A memory-only turn/message
	// cannot prove the native turn_queue foreign-key and commit contract.
	convClient, err := convservice.New(ctx, server)
	require.NoError(t, err)
	conv := convcli.NewConversation()
	conv.SetId("conv-goal")
	conv.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	// Resolve the controller's remaining cold reads synchronously. The logical
	// callback budget below does not measure first-time component compilation.
	current, err := goalStore.Current(ctx, conversationID)
	require.NoError(t, err)
	require.NotNil(t, current)
	counter, ok := dataSvc.(controllerSignalCounter)
	require.True(t, ok)
	_, err = counter.CountPendingElicitations(ctx, conversationID)
	require.NoError(t, err)
	_, err = counter.CountPendingApprovals(ctx, conversationID)
	require.NoError(t, err)
	_, err = counter.CountControllerTurns(ctx, conversationID)
	require.NoError(t, err)
	_, err = convClient.GetConversation(ctx, conversationID, convcli.WithIncludeTranscript(true))
	require.NoError(t, err)
	// Warm exact native write shapes using an unrelated succeeded turn, not
	// another queued/controller action. This leaves continuation counts at zero.
	warmTurn := convcli.NewTurn()
	warmTurn.SetId("goal-fixture-warm")
	warmTurn.SetConversationID(conversationID)
	warmTurn.SetStatus("succeeded")
	warmTurn.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchTurn(ctx, warmTurn))
	warmMessage := convcli.NewMessage()
	warmMessage.SetId("goal-fixture-warm")
	warmMessage.SetConversationID(conversationID)
	warmMessage.SetTurnID("goal-fixture-warm")
	warmMessage.SetRole("user")
	warmMessage.SetType("task")
	warmMessage.SetContent("fixture initialization")
	warmMessage.SetRawContent("fixture initialization")
	warmMessage.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchMessage(ctx, warmMessage))
	queuePatcher, ok := dataSvc.(interface {
		PatchTurnQueue(context.Context, *turnqueuemodel.TurnQueue) error
	})
	require.True(t, ok)
	warmQueue := &turnqueuemodel.TurnQueue{Has: &turnqueuemodel.TurnQueueHas{}}
	warmQueue.SetId("goal-fixture-warm")
	warmQueue.SetConversationId(conversationID)
	warmQueue.SetTurnId("goal-fixture-warm")
	warmQueue.SetMessageId("goal-fixture-warm")
	warmQueue.SetQueueSeq(1)
	warmQueue.SetStatus("done")
	warmQueue.SetCreatedAt(time.Now())
	warmQueue.SetUpdatedAt(time.Now())
	require.NoError(t, queuePatcher.PatchTurnQueue(ctx, warmQueue))
	queuedBefore, err := dataSvc.CountQueuedTurns(ctx, &turnmodel.QueuedTotalInput{ConversationID: conversationID, Has: &turnmodel.QueuedTotalInputHas{ConversationID: true}})
	require.NoError(t, err)
	require.Zero(t, queuedBefore)
	manager := asynccfg.NewManager()
	svc := &Service{
		dataService:  dataSvc,
		conversation: convClient,
		asyncManager: manager,
		goalRuntime:  goalsys.NewRuntime(goalStore),
	}

	rec1, _ := manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "op-1",
		ParentConvID:  "conv-goal",
		ParentTurnID:  "turn-parent",
		ToolCallID:    "tool-call-1",
		ToolMessageID: "tool-msg-1",
		ToolName:      "resources:search",
		ExecutionMode: string(asynccfg.ExecutionModeDetach),
		Status:        "started",
		Message:       "working",
	})
	rec2, _ := manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "op-2",
		ParentConvID:  "conv-goal",
		ParentTurnID:  "turn-parent",
		ToolCallID:    "tool-call-2",
		ToolMessageID: "tool-msg-2",
		ToolName:      "resources:search",
		ExecutionMode: string(asynccfg.ExecutionModeDetach),
		Status:        "started",
		Message:       "working",
	})
	svc.observeDetachedAsyncGoalCompletion(ctx, rec1)
	svc.observeDetachedAsyncGoalCompletion(ctx, rec2)

	_, changed1 := manager.Update(ctx, asynccfg.UpdateInput{
		ID:      "op-1",
		Status:  "completed",
		Message: "finished 1",
		KeyData: []byte(`{"continuationHint":"Open result 1 and continue cleanup."}`),
	})
	_, changed2 := manager.Update(ctx, asynccfg.UpdateInput{
		ID:      "op-2",
		Status:  "completed",
		Message: "finished 2",
		KeyData: []byte(`{"continuationHint":"Open result 2 and continue cleanup."}`),
	})
	require.True(t, changed1)
	require.True(t, changed2)

	require.Eventually(t, func() bool {
		got, err := convClient.GetConversation(ctx, "conv-goal", convcli.WithIncludeTranscript(true))
		if err != nil || got == nil {
			return false
		}
		count := 0
		for _, turn := range got.GetTranscript() {
			if turn == nil {
				continue
			}
			for _, msg := range turn.Message {
				if msg == nil || msg.RawContent == nil {
					continue
				}
				if strings.Contains(strings.TrimSpace(*msg.RawContent), "continue cleanup") {
					count++
				}
			}
		}
		return count == 1
	}, 3*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		_, first := svc.asyncGoalWatches.Load(rec1.ID)
		_, second := svc.asyncGoalWatches.Load(rec2.ID)
		return !first && !second
	}, 3*time.Second, 10*time.Millisecond)
	queued, err := dataSvc.CountQueuedTurns(ctx, &turnmodel.QueuedTotalInput{ConversationID: conversationID, Has: &turnmodel.QueuedTotalInputHas{ConversationID: true}})
	require.NoError(t, err)
	require.Equal(t, 1, queued)
}

func TestObserveDetachedAsyncGoalCompletion_DoesNotQueueWhenGoalAlreadyHasQueuedControllerTurn(t *testing.T) {
	ctx := context.Background()
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	_, err = dataSvc.PatchConversations(ctx, []*conversationmodel.Conversation{
		conversationmodel.NewMutableConversationView(conversationmodel.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyEvaluate,
	}).Encode()
	require.NoError(t, err)
	goalStore := newLinkedGoalStore(t, "goal-conv-goal", "conv-goal", "finish parser cleanup", spec)

	convClient := convmem.New()
	conv := convcli.NewConversation()
	conv.SetId("conv-goal")
	conv.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	existingTurn := convcli.NewTurn()
	existingTurn.SetId("queued-existing")
	existingTurn.SetConversationID("conv-goal")
	existingTurn.SetStatus("queued")
	existingTurn.SetOrigin("controller")
	existingTurn.SetGoalID("goal-conv-goal")
	existingTurn.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchTurn(ctx, existingTurn))

	existingMsg := convcli.NewMessage()
	existingMsg.SetId("queued-existing")
	existingMsg.SetConversationID("conv-goal")
	existingMsg.SetTurnID("queued-existing")
	existingMsg.SetRole("user")
	existingMsg.SetType("task")
	existingMsg.SetContent("Continue existing goal")
	existingMsg.SetRawContent("Continue existing goal")
	existingMsg.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchMessage(ctx, existingMsg))

	manager := asynccfg.NewManager()
	svc := &Service{
		dataService:  dataSvc,
		conversation: convClient,
		asyncManager: manager,
		goalRuntime:  goalsys.NewRuntime(goalStore),
	}

	rec, _ := manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "op-1",
		ParentConvID:  "conv-goal",
		ParentTurnID:  "turn-parent",
		ToolCallID:    "tool-call-1",
		ToolMessageID: "tool-msg-1",
		ToolName:      "resources:search",
		ExecutionMode: string(asynccfg.ExecutionModeDetach),
		Status:        "started",
		Message:       "working",
	})
	svc.observeDetachedAsyncGoalCompletion(ctx, rec)

	_, changed := manager.Update(ctx, asynccfg.UpdateInput{
		ID:      "op-1",
		Status:  "completed",
		Message: "finished",
		KeyData: []byte(`{"continuationHint":"Open result and continue cleanup."}`),
	})
	require.True(t, changed)

	time.Sleep(200 * time.Millisecond)
	got, err := convClient.GetConversation(ctx, "conv-goal", convcli.WithIncludeTranscript(true))
	require.NoError(t, err)
	count := 0
	for _, turn := range got.GetTranscript() {
		if turn == nil || !strings.EqualFold(strings.TrimSpace(turn.Status), "queued") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(valueOrEmpty(turn.Origin)), "controller") &&
			strings.TrimSpace(valueOrEmpty(turn.GoalId)) == "goal-conv-goal" {
			count++
		}
	}
	require.Equal(t, 1, count)
}

func TestEmitGoalControllerScheduled_PublishesLifecycleEvent(t *testing.T) {
	pub := &captureGoalEventPublisher{}
	svc := &Service{streamPub: pub}
	svc.emitGoalControllerScheduled(context.Background(), "conv-goal", "turn-goal", "goal-conv-goal", "queue", nil, &goalsys.ContinuationHint{
		Reason:  "continue active goal",
		Preview: "Continue parser cleanup",
		Payload: "Continue parser cleanup with latest async result.",
	})
	require.True(t, pub.HasEvent(streaming.EventTypeGoalControllerScheduled))
}
