package manage

import (
	"encoding/json"

	store "github.com/viant/agently-core/app/store/agui"
)

// Compose the existing admission CAS and terminal journal in one transaction.
// The old interrupted run is never reopened or modified beyond its normal
// successor link; failed append rolls back that link and the new run together.
func (t *operation) admitPendingRecovery(input *store.Request, output *store.Response) error {
	if input.Admission == nil || input.Admission.PriorRunID == "" || input.Change == nil || input.Change.Pending == nil || input.Change.State != nil || input.Change.Messages != nil || input.Change.Status != "" || input.Change.LeaseOwner != "" || input.Change.TurnID != "" || len(input.Events) != 2 {
		return store.ErrInvalidTransition
	}
	var start, end struct {
		Type    string `json:"type"`
		Outcome struct {
			Type               string   `json:"type"`
			PendingToolCallIDs []string `json:"pendingToolCallIds"`
		} `json:"outcome"`
	}
	if json.Unmarshal(input.Events[0], &start) != nil || start.Type != "RUN_STARTED" || json.Unmarshal(input.Events[1], &end) != nil || end.Type != "RUN_FINISHED" || (end.Outcome.Type != "interrupt" && (end.Outcome.Type != "success" || len(end.Outcome.PendingToolCallIDs) == 0)) {
		return store.ErrInvalidTransition
	}
	if err := t.admit(input, output); err != nil {
		return err
	}
	if !output.New {
		// The caller adopts a committed replacement through ordinary reads. Never
		// interpret a second request as permission to rewrite an interrupted wait.
		return store.ErrConflict
	}

	appendInput := *input
	appendInput.ExpectedRevision = output.Run.Revision
	return t.append(&appendInput, output)
}
