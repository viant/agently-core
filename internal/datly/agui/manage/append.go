package manage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"math"
	"time"

	store "github.com/viant/agently-core/app/store/agui"
	eventwrite "github.com/viant/agently-core/internal/datly/agui/event/write"
	runwrite "github.com/viant/agently-core/internal/datly/agui/run/write"
	threadwrite "github.com/viant/agently-core/internal/datly/agui/thread/write"
	wire "github.com/viant/agently-core/protocol/agui"
)

func (t *operation) append(input *store.Request, output *store.Response) error {
	if input.RunID == "" || input.ExpectedRevision < 1 {
		return store.ErrConflict
	}
	// Lock the thread before its run, matching admission's order.
	thread, err := t.thread()
	if err != nil {
		return err
	}
	if thread == nil {
		return store.ErrNotFound
	}
	run, err := t.run(input.RunID)
	if err != nil {
		return err
	}
	if run == nil {
		return store.ErrNotFound
	}
	if run.Revision != input.ExpectedRevision {
		return store.ErrConflict
	}
	result := t.projectRun(run)
	lease, err := t.lease(input.RunID)
	if err != nil {
		return err
	}
	owner := ""
	if input.Change != nil {
		owner = input.Change.LeaseOwner
	}
	if lease != nil && (owner != str(lease.Owner) || !lease.LeaseUntil.After(time.Now().UTC())) {
		return store.ErrConflict
	}
	if lease == nil && owner != "" {
		return store.ErrConflict
	}

	if terminal(result.Status) || result.ResumedByRunID != "" {
		return store.ErrInvalidTransition
	}
	if result.Revision == math.MaxInt64 || int64(len(input.Events)) > math.MaxInt64-result.LastSequence {
		return store.ErrConflict
	}
	status := result.Status
	sequence := result.LastSequence
	rows := make([]*eventwrite.Event, 0, len(input.Events))
	ended := false
	for index, raw := range input.Events {
		if err := wire.ValidateEvent(raw); err != nil {
			return err
		}
		var envelope struct {
			Type, ThreadID, RunID string
			Outcome               *struct {
				Type               string
				PendingToolCallIDs []string `json:"pendingToolCallIds"`
			}
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return err
		}
		if sequence == 0 && envelope.Type != "RUN_STARTED" {
			return fmt.Errorf("%w: journal must begin with RUN_STARTED", store.ErrInvalidTransition)
		}
		if ended {
			return fmt.Errorf("%w: event follows terminal event", store.ErrInvalidTransition)
		}
		switch envelope.Type {
		case "RUN_STARTED":
			if sequence != 0 || envelope.ThreadID != t.threadID || envelope.RunID != input.RunID {
				return store.ErrInvalidTransition
			}
			status = store.StatusRunning
		case "RUN_FINISHED":
			if envelope.ThreadID != t.threadID || envelope.RunID != input.RunID {
				return store.ErrInvalidTransition
			}
			status = store.StatusFinished
			if envelope.Outcome != nil {
				if len(envelope.Outcome.PendingToolCallIDs) > 0 {
					status = store.StatusInterrupted
				}
				switch envelope.Outcome.Type {
				case "interrupt":
					status = store.StatusInterrupted
				case "cancelled":
					status = store.StatusCancelled
				}
			}
			ended = true
		case "RUN_ERROR":
			status = store.StatusError
			ended = true
		}
		sequence++
		row := &eventwrite.Event{}
		row.SetEventKey(ptr(uuid.NewString()))
		row.SetRunKey(run.Id)
		row.SetSequence(sequence)
		row.SetEventJson(clone(raw))
		row.SetKind(ptr("agui.event"))
		row.SetMimeType(ptr("application/json"))
		row.SetStorage(ptr("inline"))
		row.SetCompression(ptr("none"))
		size := len(raw)
		row.SetSizeBytes(&size)
		digest := sha256.Sum256(raw)
		row.SetDigest(ptr(hex.EncodeToString(digest[:])))
		rows = append(rows, row)
		if ended && index != len(input.Events)-1 {
			return store.ErrInvalidTransition
		}
	}
	change := input.Change
	if change != nil {
		if change.Status != "" && change.Status != status {
			// Execution can move from admitted to running before the first event is
			// persisted, but terminal states always require a matching terminal event.
			if change.Status != store.StatusRunning || status != store.StatusAdmitted || len(input.Events) != 0 {
				return store.ErrInvalidTransition
			}
			status = change.Status
		}
		if change.Pending != nil && !json.Valid(change.Pending) {
			return fmt.Errorf("invalid AG-UI pending JSON")
		}
		if change.State != nil && !json.Valid(change.State) {
			return fmt.Errorf("invalid AG-UI state JSON")
		}
		if change.Messages != nil {
			var messages []json.RawMessage
			if err := json.Unmarshal(change.Messages, &messages); err != nil || messages == nil {
				return fmt.Errorf("AG-UI messages must be an array")
			}
			for _, message := range messages {
				if err := wire.ValidateMessage(message); err != nil {
					return err
				}
			}
		}
		if change.TurnID != "" && result.TurnID != "" && change.TurnID != result.TurnID {
			return store.ErrConflict
		}
		if change.State != nil || change.Messages != nil {
			if change.ExpectedThreadRevision != thread.Revision || thread.Revision == math.MaxInt64 {
				return store.ErrConflict
			}
			row := &threadwrite.Thread{}
			row.SetId(thread.Id)
			row.SetPrincipal(ptr(t.principal))
			row.SetRevision(thread.Revision)
			if change.State != nil {
				row.SetStateJson(clone(change.State))
			}
			if change.Messages != nil {
				row.SetMessagesJson(clone(change.Messages))
			}
			if err := t.writeThread(row, "update", thread.Revision+1); err != nil {
				return err
			}
		}
	}
	row := &runwrite.Run{}
	row.SetId(run.Id)
	row.SetRunKey(run.RunKey)
	row.SetPrincipal(ptr(t.principal))
	row.SetRevision(result.Revision)
	row.SetLastSequence(sequence)
	row.SetStatus(ptr(status))
	if change != nil {
		if change.Pending != nil {
			row.SetPendingJson(clone(change.Pending))
			result.Pending = clone(change.Pending)
		}
		if change.TurnID != "" {
			row.SetTurnId(change.TurnID)
			if run.PriorRunId == "" && run.TurnId == "" {
				row.SetInitialTurnKey(ptr(store.InitialTurnKey(t.principal, t.conversationID, change.TurnID)))
			}
			result.TurnID = change.TurnID
		}
	}
	if err := t.writeRun(row, "update", result.Revision+1); err != nil {
		return err
	}
	if len(rows) > 0 {
		mutation := &eventwrite.Input{}
		mutation.SetRows(rows)
		if _, err := t.call("event/write", "writer", "POST", mutation); err != nil {
			return err
		}
	}
	result.Revision++
	result.LastSequence = sequence
	result.Status = status
	output.Run = result
	return nil
}
func terminal(status string) bool {
	switch status {
	case store.StatusInterrupted, store.StatusFinished, store.StatusError, store.StatusCancelled:
		return true
	}
	return false
}
