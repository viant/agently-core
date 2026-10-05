package sdk

import (
	"context"
	"fmt"
	"strings"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/runtime/streaming"
)

const CompatibilityReconcileEvent streaming.EventType = "compatibility_reconcile"

// compatibilityObserverBackend deliberately excludes remote/generic adapters.
// Authentication and native visibility must succeed before provenance is read.
type compatibilityObserverBackend interface {
	authorizeCompatibilityConversation(context.Context, string) error
	compatibilityExecutionProvenance() aguistore.ExecutionProvenanceReader
}

func (c *backendClient) authorizeCompatibilityConversation(ctx context.Context, conversationID string) error {
	if c == nil || c.goalInvoker == nil {
		return fmt.Errorf("native observation unavailable")
	}
	return native.RequireVisibleConversation(ctx, c.goalInvoker, conversationID)
}
func (c *backendClient) compatibilityExecutionProvenance() aguistore.ExecutionProvenanceReader {
	if c == nil || c.goalInvoker == nil {
		return nil
	}
	return aguistore.New(c.goalInvoker)
}

type compatibilityProvenance string

const (
	compatibilityUnknown compatibilityProvenance = "unknown"
	compatibilityNative  compatibilityProvenance = "native"
	compatibilityAGUI    compatibilityProvenance = "ag-ui"
)

type compatibilityObserver struct {
	reader             aguistore.ExecutionProvenanceReader
	conversationID     string
	classified         map[string]compatibilityProvenance
	reconciliationSent map[string]bool
}

func newCompatibilityObserver(reader aguistore.ExecutionProvenanceReader, conversationID string) *compatibilityObserver {
	return &compatibilityObserver{reader: reader, conversationID: conversationID, classified: map[string]compatibilityProvenance{}, reconciliationSent: map[string]bool{}}
}

// classify relies only on immutable admission and persisted native identities.
// AG-UI admission commits TurnID before Query can persist/publish that turn.
// No lease, connection, status, active observer, or viewer principal is evidence.
func (o *compatibilityObserver) classify(ctx context.Context, threadID, turnID string, visited map[string]bool) compatibilityProvenance {
	key := threadID + "\x00" + turnID
	if result, ok := o.classified[key]; ok {
		return result
	}
	if o.reader == nil || threadID == "" || turnID == "" || visited[key] || len(visited) >= 64 {
		return compatibilityUnknown
	}
	visited[key] = true
	evidence, err := o.reader.ReadExecutionProvenance(ctx, threadID, turnID)
	if err != nil || evidence == nil || evidence.ThreadID != threadID || evidence.TurnID != turnID {
		return compatibilityUnknown
	}
	if evidence.AGUIOwned {
		o.classified[key] = compatibilityAGUI
		return compatibilityAGUI
	}
	if !evidence.NativeTurnFound {
		return compatibilityUnknown
	}
	if evidence.ParentThreadID != "" || evidence.ParentTurnID != "" {
		if evidence.ParentThreadID == "" || evidence.ParentTurnID == "" {
			return compatibilityUnknown
		}
		ancestor := o.classify(ctx, evidence.ParentThreadID, evidence.ParentTurnID, visited)
		if ancestor == compatibilityUnknown {
			return compatibilityUnknown
		}
		o.classified[key] = ancestor
		return ancestor
	}
	o.classified[key] = compatibilityNative
	return compatibilityNative
}

func compatibilityApplicationEvent(event *streaming.Event) bool {
	switch event.Type {
	case streaming.EventTypeConversationMetaUpdated, streaming.EventTypeGoalUpdated, streaming.EventTypeGoalCleared, streaming.EventTypeGoalControllerScheduled:
		return true
	case streaming.EventTypeSkillStarted, streaming.EventTypeSkillCompleted, streaming.EventTypeToolFeedActive, streaming.EventTypeToolFeedInactive:
		return strings.TrimSpace(event.TurnID) == ""
	}
	return false
}

// project returns native/application events unchanged. Unknown execution only
// emits a minimal reconciliation hint; payloads and speculative text stay out.
func (o *compatibilityObserver) project(ctx context.Context, event *streaming.Event) *streaming.Event {
	if event == nil {
		return nil
	}
	if applicationWorkspaceEvent(event) {
		return event
	}
	threadID := strings.TrimSpace(event.ConversationID)
	if threadID == "" {
		threadID = strings.TrimSpace(event.StreamID)
	}
	if threadID != o.conversationID {
		return nil
	}
	if compatibilityApplicationEvent(event) {
		return event
	}
	turnID := strings.TrimSpace(event.TurnID)
	provenance := o.classify(ctx, threadID, turnID, map[string]bool{})
	if provenance == compatibilityAGUI {
		return nil
	}
	if provenance == compatibilityNative && event.Type != streaming.EventTypeProtocol {
		return event
	}
	key := threadID + "\x00" + turnID
	if o.reconciliationSent[key] {
		return nil
	}
	o.reconciliationSent[key] = true
	return &streaming.Event{Type: CompatibilityReconcileEvent, ConversationID: threadID, StreamID: threadID, TurnID: turnID, EventSeq: event.EventSeq, CreatedAt: time.Now().UTC(), Status: "unknown", Patch: map[string]any{"reason": "execution_provenance_unavailable"}}
}

func applicationWorkspaceEvent(event *streaming.Event) bool {
	return event != nil && event.Type == streaming.EventTypeSkillRegistryUpdated && (event.ConversationID == "" || event.ConversationID == "skills") && (event.StreamID == "" || event.StreamID == "skills") && event.TurnID == ""
}
func subscribeNativeEvents(ctx context.Context, client Client, input *StreamEventsInput) (streaming.Subscription, error) {
	backend, ok := client.(interface {
		StreamEvents(context.Context, *StreamEventsInput) (streaming.Subscription, error)
	})
	if !ok {
		return nil, fmt.Errorf("native execution event subscription unavailable")
	}
	return backend.StreamEvents(ctx, input)
}
