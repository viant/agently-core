package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/streaming"
)

// runAGUIFeedSubscription observes live feed state in its own journal. It never
// sends another user turn, invokes a model, or changes chat shared state.
func runAGUIFeedSubscription(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, payload json.RawMessage) error {
	if err := extensions.ValidateWorkspacePayload("feed.subscribe", payload); err != nil {
		return err
	}
	input := struct {
		ID              string `json:"id"`
		DurationSeconds int    `json:"durationSeconds"`
	}{DurationSeconds: 300}
	if err := json.Unmarshal(payload, &input); err != nil {
		return err
	}
	return observeAGUIFeed(ctx, client, store, record, input.ID, time.Duration(input.DurationSeconds)*time.Second)
}
func observeAGUIFeed(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, id string, duration time.Duration) error {
	if record == nil || record.ConversationID == "" || record.TurnID != "" || record.LeaseOwner == "" || duration <= 0 {
		return fmt.Errorf("feed subscription requires an independently claimed resource run")
	}
	relevant := func(event *streaming.Event) bool {
		if event == nil || (event.ConversationID != record.ConversationID && !(event.ConversationID == "" && event.StreamID == record.ConversationID)) {
			return false
		}
		switch event.Type {
		case streaming.EventTypeToolFeedActive, streaming.EventTypeToolFeedInactive:
			return event.FeedID == id
		case streaming.EventTypeToolCallCompleted, streaming.EventTypeTurnCompleted, streaming.EventTypeTurnCanceled, streaming.EventTypeTurnFailed:
			return true
		}
		return false
	}
	subscription, err := client.StreamEvents(ctx, &StreamEventsInput{ConversationID: record.ConversationID, Filter: relevant})
	if err != nil {
		return err
	}
	defer subscription.Close()
	activation, err := recoverAGUIFeedActivation(ctx, client, store, record, id)
	if err != nil {
		return err
	}
	run := record
	leaseRevision := record.LeaseRevision
	var prior []byte
	appendEvents := func(events []json.RawMessage) error {
		next, err := store.Append(context.WithoutCancel(ctx), run.Principal, run.ThreadID, run.RunID, run.Revision, events, &aguistore.Change{LeaseOwner: run.LeaseOwner})
		if err != nil {
			return err
		}
		run = next
		return nil
	}
	finish := func(cancelled bool) error {
		outcome := "success"
		if cancelled {
			outcome = "cancelled"
		}
		return appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": run.ThreadID, "runId": run.RunID, "outcome": map[string]any{"type": outcome}})})
	}
	if aguiRunTerminal(run.Status) {
		return aguistore.ErrInvalidTransition
	}
	if run.LastSequence == 0 {
		if err := appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": run.ThreadID, "runId": run.RunID, "protocolVersion": "1.0"})}); err != nil {
			return err
		}
	}
	refresh := func(transition *agui.FeedLifecycleFact) error {
		payload, _ := json.Marshal(map[string]any{"id": id})
		value, handled, err := dispatchAGUIWorkspace(ctx, client, run.ConversationID, "feed.get", payload)
		if err != nil {
			return err
		}
		if !handled {
			return fmt.Errorf("feed capability is unavailable")
		}
		content := map[string]any{"version": "1", "feed": value, "active": activation.Active, "activationKnown": activation.Active != nil}
		if !activation.At.IsZero() {
			content["activationAt"] = activation.At.UTC().Format(time.RFC3339Nano)
		}
		if activation.Origin != nil {
			content["activationSource"] = activation.Origin
		}
		if transition != nil {
			fact := map[string]any{"version": "1", "feedId": id, "active": transition.Active, "activationSource": transition.Origin}
			if !transition.At.IsZero() {
				fact["activationAt"] = transition.At.UTC().Format(time.RFC3339Nano)
			}
			content["activationFact"] = fact
		}
		snapshot, err := json.Marshal(content)
		if err != nil {
			return err
		}
		if err = extensions.ValidatePresentation("FeedActivationSnapshot", snapshot); err != nil {
			return err
		}
		if bytes.Equal(prior, snapshot) {
			return nil
		}
		if err := appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "ACTIVITY_SNAPSHOT", "messageId": "agently-feed-" + run.RunID, "activityType": "agently.feed", "content": content, "replace": true})}); err != nil {
			return err
		}
		prior = append(prior[:0], snapshot...)
		return nil
	}
	fail := func(cause error) error {
		if err := appendEvents([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_ERROR", "message": cause.Error(), "code": "FEED_SUBSCRIPTION_ERROR"})}); err != nil {
			return fmt.Errorf("%w; termination failed: %v", cause, err)
		}
		return cause
	}
	if err = refresh(nil); err != nil {
		return fail(err)
	}
	lifetime := time.NewTimer(duration)
	defer lifetime.Stop()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return finish(true)
		case <-lifetime.C:
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
					return fail(fmt.Errorf("feed event buffer overflow; reconnect for an authoritative snapshot"))
				}
				return finish(false)
			}
			if relevant(event) {
				var transition *agui.FeedLifecycleFact
				if event.Type == streaming.EventTypeToolFeedActive || event.Type == streaming.EventTypeToolFeedInactive {
					active := event.Type == streaming.EventTypeToolFeedActive
					transition = &agui.FeedLifecycleFact{Active: active, At: event.CreatedAt, Origin: &agui.FeedLifecycleOrigin{RunID: run.RunID, Sequence: run.LastSequence + 1}}
					activation.observe(*transition)
				}
				if err = refresh(transition); err != nil {
					return fail(err)
				}
			}
		}
	}
}
