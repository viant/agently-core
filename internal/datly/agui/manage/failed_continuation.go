package manage

import (
	"bytes"
	"encoding/json"

	store "github.com/viant/agently-core/app/store/agui"
	eventread "github.com/viant/agently-core/internal/datly/agui/event/read"
	runread "github.com/viant/agently-core/internal/datly/agui/run/read"
)

// This narrow extension of existing admission consumes the failed child's CAS,
// never the original parent's historical consumed relation. No journal is reset.
func (t *operation) failedContinuationClaim(a *store.Admission, prior *runread.Run) (*runread.Run, error) {
	if a.ExpectedFailedRevision < 1 || a.ParentRunID == "" || str(prior.ResumedByRunId) != a.ParentRunID {
		return nil, store.ErrConflict
	}
	failed, err := t.run(a.ParentRunID)
	if err != nil {
		return nil, err
	}
	if failed == nil || failed.Revision != a.ExpectedFailedRevision || failed.ResumedByRunId != "" || failed.PriorRunId != a.PriorRunID || failed.TurnId != prior.TurnId || str(failed.ConversationId) != str(prior.ConversationId) || str(failed.Principal) != str(prior.Principal) || failed.LastSequence != 2 {
		return nil, store.ErrConflict
	}
	query := &eventread.Input{}
	query.SetPrincipal(t.principal)
	query.SetRunKey(str(failed.Id))
	query.SetAfter(0)
	query.SetLimit(3)
	value, err := t.call("event/read", "reader", "GET", query)
	if err != nil {
		return nil, err
	}
	rows, ok := value.(*eventread.Output)
	if !ok || rows == nil || len(rows.Data) != 2 {
		return nil, store.ErrConflict
	}
	journal := make([]json.RawMessage, 0, 2)
	for index, row := range rows.Data {
		if row == nil || row.Sequence != int64(index+1) || str(row.RunId) != a.ParentRunID || str(row.ThreadId) != t.threadID {
			return nil, store.ErrConflict
		}
		journal = append(journal, clone(row.EventJson))
	}
	projected := t.projectRun(failed)
	if !store.FailedContinuationRestoration(projected, journal) || a.RunID != store.FailedContinuationRecoveryID(projected) {
		return nil, store.ErrConflict
	}
	expected, err := store.FailedContinuationRecoveryInput(projected)
	if err != nil {
		return nil, err
	}
	want, err := canonicalJSON(expected)
	if err != nil {
		return nil, err
	}
	actual, err := canonicalJSON(a.Input)
	if err != nil || !bytes.Equal(want, actual) {
		return nil, store.ErrConflict
	}
	return failed, nil
}
