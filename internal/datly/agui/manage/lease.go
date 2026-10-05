package manage

import (
	"fmt"
	"math"
	"time"

	store "github.com/viant/agently-core/app/store/agui"
	leaseread "github.com/viant/agently-core/internal/datly/agui/lease/read"
	leasewrite "github.com/viant/agently-core/internal/datly/agui/lease/write"
)

func (t *operation) lease(runID string) (*leaseread.Lease, error) {
	input := &leaseread.Input{}
	input.SetPrincipal(t.principal)
	if t.conversationID == "" {
		thread, err := t.thread()
		if err != nil {
			return nil, err
		}
		if thread == nil {
			return nil, nil
		}
	}
	input.SetKey(store.RunKey(t.principal, t.conversationID, runID))
	value, err := t.call("lease/read", "reader", "GET", input)
	if err != nil {
		return nil, err
	}
	rows, ok := value.(*leaseread.Output)
	if !ok || rows == nil {
		return nil, fmt.Errorf("AG-UI lease reader returned %T", value)
	}
	if len(rows.Data) > 1 {
		return nil, fmt.Errorf("AG-UI lease identity is ambiguous")
	}
	if len(rows.Data) == 0 {
		return nil, nil
	}
	row := rows.Data[0]
	if row.Revision == 0 && str(row.Owner) == "" && row.LeaseUntil == nil {
		return nil, nil
	}
	if row.Revision < 1 || str(row.Owner) == "" || row.LeaseUntil == nil {
		return nil, store.ErrConflict
	}
	return row, nil
}
func (t *operation) decorate(run *store.Run) error {
	lease, err := t.lease(run.RunID)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	until := lease.LeaseUntil.UTC()
	run.LeaseOwner = str(lease.Owner)
	run.LeaseUntil = &until
	run.LeaseRevision = lease.Revision
	return nil
}
func (t *operation) claim(input *store.Request, output *store.Response) error {
	if input.RunID == "" || input.LeaseOwner == "" || input.ExpectedRevision < 1 || input.TTL <= 0 {
		return store.ErrConflict
	}
	run, err := t.run(input.RunID)
	if err != nil {
		return err
	}
	if run == nil {
		return store.ErrNotFound
	}
	if terminal(str(run.Status)) {
		return store.ErrInvalidTransition
	}
	lease, err := t.lease(input.RunID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if input.Operation == "renew" {
		if lease == nil || str(lease.Owner) != input.LeaseOwner || lease.Revision != input.ExpectedRevision || !lease.LeaseUntil.After(now) {
			return store.ErrConflict
		}
	} else {
		if run.Revision != input.ExpectedRevision {
			return store.ErrConflict
		}
		if lease != nil && lease.LeaseUntil.After(now) && str(lease.Owner) != input.LeaseOwner {
			return store.ErrConflict
		}
	}
	row := &leasewrite.Lease{}
	row.SetId(run.Id)
	row.SetRunKey(run.RunKey)
	row.SetPrincipal(ptr(t.principal))
	row.SetOwner(ptr(input.LeaseOwner))
	until := now.Add(input.TTL)
	row.SetLeaseUntil(&until)
	mutation := &leasewrite.Input{}
	mutation.SetPrincipal(t.principal)
	mutation.SetRows([]*leasewrite.Lease{row})
	if lease == nil {
		row.SetRevision(0)
		mutation.SetMode("update")
		mutation.SetDesiredRevision(1)
	} else {
		if lease.Revision == math.MaxInt64 {
			return store.ErrConflict
		}
		row.SetRevision(lease.Revision)
		mutation.SetMode("update")
		mutation.SetDesiredRevision(lease.Revision + 1)
	}
	if _, err := t.call("lease/write", "writer", "PATCH", mutation); err != nil {
		return err
	}
	output.Run = t.projectRun(run)
	return nil
}
