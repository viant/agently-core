package maintenancelease

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/viant/agently-core/internal/datly/dbtime"
	read "github.com/viant/agently-core/internal/datly/maintenancelease/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	dexec "github.com/viant/datly/exec"
)

var (
	ErrTransactionContext = errors.New("maintenance lease check requires a context outside a managed invocation")
	ErrUncheckedLease     = errors.New("maintenance mutation requires a preceding lease check")
)

type checkedLeaseKey struct{}
type checkedLease struct {
	key, owner, token string
	at                time.Time
}

// Check observes ownership and database time together, outside any managed
// invocation. It does not write or lock the lease, and does not guarantee
// ownership through the subsequent deletion's commit.
func (s *Store) Check(ctx context.Context, lease Lease) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	// Presence is deliberately rejected even if the inherited lookup would
	// currently return nil: it may acquire a transaction later in the invocation.
	if dexec.HasInvocationTransactionLookup(ctx) {
		return time.Time{}, ErrTransactionContext
	}
	lease = normalize(lease)
	if !valid(lease) {
		return time.Time{}, ErrInvalidLease
	}
	input := &read.Input{}
	input.SetLeaseKey(lease.Key)
	ctx = queryselectors.ForUpdateOptions(ctx, false).Context(ctx)
	rows, err := s.snapshot(ctx, input)
	if err != nil {
		return time.Time{}, err
	}
	if len(rows) != 1 || rows[0] == nil {
		return time.Time{}, fmt.Errorf("maintenance lease check returned %d rows", len(rows))
	}
	row := rows[0]
	now, err := dbtime.ParseDatabaseUTC(row.DbNow)
	if err != nil {
		return time.Time{}, err
	}
	if row.LeaseKey == nil {
		return time.Time{}, ErrLeaseLost
	}
	if row.OwnerId == nil || row.LeaseToken == nil || row.LeaseUntil == nil {
		return time.Time{}, fmt.Errorf("maintenance lease %q has incomplete persisted fields", lease.Key)
	}
	if *row.LeaseKey != lease.Key || *row.OwnerId != lease.OwnerID || *row.LeaseToken != lease.Token || !row.LeaseUntil.After(now) {
		return time.Time{}, ErrLeaseLost
	}
	return now, nil
}

// Preflight finishes the standalone lease read before the caller invokes its
// mutating component. The context receipt cannot be supplied through JSON.
func (s *Store) Preflight(ctx context.Context, lease Lease) (context.Context, error) {
	now, err := s.Check(ctx, lease)
	if err != nil {
		return nil, err
	}
	lease = normalize(lease)
	return context.WithValue(ctx, checkedLeaseKey{}, checkedLease{key: lease.Key, owner: lease.OwnerID, token: lease.Token, at: now}), nil
}

// CheckedAt validates the in-memory receipt without accessing the database.
// Mutation handlers call it before binding dependencies or starting a transaction.
func CheckedAt(ctx context.Context, lease Lease) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	lease = normalize(lease)
	if !valid(lease) {
		return time.Time{}, ErrInvalidLease
	}
	checked, ok := ctx.Value(checkedLeaseKey{}).(checkedLease)
	if !ok || checked.key != lease.Key || checked.owner != lease.OwnerID || checked.token != lease.Token {
		return time.Time{}, ErrUncheckedLease
	}
	return checked.at, nil
}
