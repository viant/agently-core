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
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
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
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	_, err = dataSvc.PatchConversations(ctx, []*convw.Conversation{
		convw.NewMutableConversationView(convw.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyEvaluate,
	}).Encode()
	require.NoError(t, err)
	_, err = dataSvc.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{
		aggoalwrite.NewMutableGoalView(
			aggoalwrite.WithGoalID("goal-conv-goal"),
			aggoalwrite.WithGoalConversationID("conv-goal"),
			aggoalwrite.WithGoalObjective("finish parser cleanup"),
			aggoalwrite.WithGoalStatus("active"),
			aggoalwrite.WithGoalControllerSpec(spec),
		),
	})
	require.NoError(t, err)

	convClient := convmem.New()
	conv := convcli.NewConversation()
	conv.SetId("conv-goal")
	conv.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	manager := asynccfg.NewManager()
	pub := &captureGoalEventPublisher{}
	svc := &Service{
		dataService:  dataSvc,
		conversation: convClient,
		asyncManager: manager,
		goalRuntime:  goalsys.NewRuntime(goalsys.NewStore(dataSvc)),
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
		got, err := convClient.GetConversation(ctx, "conv-goal")
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
}

func TestObserveDetachedAsyncGoalCompletion_RespectsAsyncPolicyWait(t *testing.T) {
	ctx := context.Background()
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	_, err = dataSvc.PatchConversations(ctx, []*convw.Conversation{
		convw.NewMutableConversationView(convw.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyWait,
	}).Encode()
	require.NoError(t, err)
	_, err = dataSvc.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{
		aggoalwrite.NewMutableGoalView(
			aggoalwrite.WithGoalID("goal-conv-goal"),
			aggoalwrite.WithGoalConversationID("conv-goal"),
			aggoalwrite.WithGoalObjective("finish parser cleanup"),
			aggoalwrite.WithGoalStatus("active"),
			aggoalwrite.WithGoalControllerSpec(spec),
		),
	})
	require.NoError(t, err)

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
		goalRuntime:  goalsys.NewRuntime(goalsys.NewStore(dataSvc)),
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
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	_, err = dataSvc.PatchConversations(ctx, []*convw.Conversation{
		convw.NewMutableConversationView(convw.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyEvaluate,
	}).Encode()
	require.NoError(t, err)
	_, err = dataSvc.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{
		aggoalwrite.NewMutableGoalView(
			aggoalwrite.WithGoalID("goal-conv-goal"),
			aggoalwrite.WithGoalConversationID("conv-goal"),
			aggoalwrite.WithGoalObjective("finish parser cleanup"),
			aggoalwrite.WithGoalStatus("active"),
			aggoalwrite.WithGoalControllerSpec(spec),
		),
	})
	require.NoError(t, err)

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
		goalRuntime:  goalsys.NewRuntime(goalsys.NewStore(dataSvc)),
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
		got, err := convClient.GetConversation(ctx, "conv-goal")
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
}

func TestObserveDetachedAsyncGoalCompletion_DoesNotQueueWhenGoalAlreadyHasQueuedControllerTurn(t *testing.T) {
	ctx := context.Background()
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	_, err = dataSvc.PatchConversations(ctx, []*convw.Conversation{
		convw.NewMutableConversationView(convw.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyEvaluate,
	}).Encode()
	require.NoError(t, err)
	_, err = dataSvc.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{
		aggoalwrite.NewMutableGoalView(
			aggoalwrite.WithGoalID("goal-conv-goal"),
			aggoalwrite.WithGoalConversationID("conv-goal"),
			aggoalwrite.WithGoalObjective("finish parser cleanup"),
			aggoalwrite.WithGoalStatus("active"),
			aggoalwrite.WithGoalControllerSpec(spec),
		),
	})
	require.NoError(t, err)

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
		goalRuntime:  goalsys.NewRuntime(goalsys.NewStore(dataSvc)),
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
	got, err := convClient.GetConversation(ctx, "conv-goal")
	require.NoError(t, err)
	count := 0
	for _, turn := range got.GetTranscript() {
		if turn == nil || !strings.EqualFold(strings.TrimSpace(turn.Status), "queued") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(valueOrEmpty(turn.Origin)), "controller") &&
			strings.TrimSpace(valueOrEmpty(turn.GoalID)) == "goal-conv-goal" {
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
