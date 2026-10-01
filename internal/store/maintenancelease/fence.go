package maintenancelease

import (
	"context"
	"time"

	write "github.com/viant/agently-core/internal/datly/maintenancelease/write"
)

// Fence joins a caller-started managed transaction and guards its live owner
// and acquisition token through the canonical writer. The imperative writer
// acquires the row's write lock; the parent retains completion ownership.
func (s *Store) Fence(ctx context.Context, lease Lease) (time.Time, error) {
	lease = normalize(lease)
	if !valid(lease) {
		return time.Time{}, ErrInvalidLease
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return time.Time{}, err
	}
	row := &write.Lease{}
	row.SetLeaseKey(lease.Key)
	row.SetUpdatedAt(now)
	input := &write.Input{}
	input.SetMode("guard")
	input.SetExpectedOwner(lease.OwnerID)
	input.SetExpectedToken(lease.Token)
	input.SetLiveAfter(now)
	input.SetLeases([]*write.Lease{row})
	if err = s.patch(ctx, input); err != nil {
		if conflicting(err) {
			return time.Time{}, ErrLeaseLost
		}
		return time.Time{}, err
	}
	return now, nil
}
