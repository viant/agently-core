package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/streaming"
)

// runAGUIGoalSubscription is a bounded background protocol run. It owns only its
// journal and observer lease; it never starts Query or changes chat projection.
func runAGUIGoalSubscription(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, payload json.RawMessage) error {
	if err := extensions.ValidateGoalPayload("goal.subscribe", payload); err != nil {
		return err
	}
	input := struct {
		DurationSeconds int `json:"durationSeconds"`
	}{DurationSeconds: 300}
	if len(bytes.TrimSpace(payload)) > 0 {
		if err := json.Unmarshal(payload, &input); err != nil {
			return err
		}
	}
	return observeAGUIGoal(ctx, client, store, record, time.Duration(input.DurationSeconds)*time.Second)
}

func observeAGUIGoal(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, duration time.Duration) (retErr error) {
	if record == nil || record.ConversationID == "" || store == nil || client == nil || record.TurnID != "" || duration <= 0 {
		return fmt.Errorf("goal subscription requires a separate background run")
	}
	if record.LeaseOwner == "" || record.LeaseUntil == nil {
		return fmt.Errorf("goal subscription requires an observer claim")
	}
	// Establish event observation before the authoritative snapshot. Event patches
	// are invalidations only: they never overwrite a newer GetGoal projection.
	subscription, err := client.StreamEvents(ctx, &StreamEventsInput{ConversationID: record.ConversationID, Filter: func(event *streaming.Event) bool {
		return event != nil && (event.ConversationID == record.ConversationID || event.ConversationID == "" && event.StreamID == record.ConversationID) && goalSubscriptionEvent(event)
	}})
	if err != nil {
		return err
	}
	defer subscription.Close()
	run := record
	leaseRevision := record.LeaseRevision
	var last []byte
	appendEvents := func(events []json.RawMessage) error {
		next, err := store.Append(context.WithoutCancel(ctx), run.Principal, run.ThreadID, run.RunID, run.Revision, events, &aguistore.Change{LeaseOwner: run.LeaseOwner})
		if err != nil {
			return err
		}
		run = next
		return nil
	}
	// If the worker is restarted under a new expired claim, resume the same stream
	// without minting another start. A terminal run is never observed again.
	if aguiRunTerminal(run.Status) {
		return aguistore.ErrInvalidTransition
	}
	if run.LastSequence == 0 {
		if err = appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": run.ThreadID, "runId": run.RunID, "protocolVersion": "1.0"})}); err != nil {
			return err
		}
	}
	refresh := func() error {
		goal, err := client.GetGoal(ctx, run.ConversationID)
		if err != nil {
			return err
		}
		content := goalCommandResult(goal, false)
		snapshot, err := json.Marshal(content)
		if err != nil {
			return err
		}
		if bytes.Equal(snapshot, last) {
			return nil
		}
		event := rawAGUI(map[string]any{"type": "ACTIVITY_SNAPSHOT", "messageId": "agently-goal-" + run.RunID, "activityType": "agently.goal", "content": content, "replace": true})
		if err = appendEvents([]json.RawMessage{event}); err != nil {
			return err
		}
		last = append(last[:0], snapshot...)
		return nil
	}
	finish := func(cancelled bool) error {
		outcome := "success"
		if cancelled {
			outcome = "cancelled"
		}
		return appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": run.ThreadID, "runId": run.RunID, "outcome": map[string]any{"type": outcome}})})
	}
	fail := func(cause error) error {
		// Accepted stream failures close only this resource subscription run.
		if err := appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_ERROR", "message": cause.Error(), "code": "GOAL_SUBSCRIPTION_ERROR"})}); err != nil {
			return fmt.Errorf("%w; journal termination failed: %v", cause, err)
		}
		return cause
	}
	if err = refresh(); err != nil {
		return fail(err)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return finish(true)
		case <-timer.C:
			return finish(false)
		case <-heartbeat.C:
			renewed, err := store.Renew(context.WithoutCancel(ctx), run.Principal, run.ThreadID, run.RunID, leaseRevision, run.LeaseOwner, time.Minute)
			if err != nil {
				return err
			}
			leaseRevision = renewed.LeaseRevision
		case event, open := <-subscription.C():
			if !open {
				if subscription.Reason() == streaming.ReasonOverflow {
					return fail(fmt.Errorf("goal subscription event buffer overflow; reconnect for an authoritative snapshot"))
				}
				return finish(false)
			}
			if event != nil && goalSubscriptionEvent(event) {
				if err = refresh(); err != nil {
					return fail(err)
				}
			}
		}
	}
}
func goalSubscriptionEvent(event *streaming.Event) bool {
	if event == nil {
		return false
	}
	switch event.Type {
	case streaming.EventTypeGoalUpdated, streaming.EventTypeGoalCleared, streaming.EventTypeGoalControllerScheduled, streaming.EventTypeTurnStarted, streaming.EventTypeTurnCompleted, streaming.EventTypeTurnFailed, streaming.EventTypeTurnCanceled:
		return true
	}
	return false
}
