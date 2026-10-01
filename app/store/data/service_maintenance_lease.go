package data

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/sqlitewrite"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
)

// MaintenanceLease identifies one acquisition of a distributed maintenance
// lease. Token changes on every acquisition so a former owner cannot mutate
// data after another worker has taken the lease.
type MaintenanceLease struct {
	Key        string
	OwnerID    string
	Token      string
	LeaseUntil time.Time
}

type MaintenanceLeaseAcquireRequest struct {
	Key     string
	OwnerID string
	TTL     time.Duration
}

type MaintenanceLeaseAcquireResult struct {
	Acquired bool
	Lease    MaintenanceLease
}

type MaintenanceLeaseRenewResult struct {
	Renewed    bool
	LeaseUntil time.Time
}

// AcquireMaintenanceLease atomically inserts a missing lease or replaces an
// expired one. An active lease is returned without its fencing token.
func (s *datlyService) AcquireMaintenanceLease(ctx context.Context, request MaintenanceLeaseAcquireRequest) (*MaintenanceLeaseAcquireResult, error) {
	request.Key = strings.TrimSpace(request.Key)
	request.OwnerID = strings.TrimSpace(request.OwnerID)
	if request.Key == "" || request.OwnerID == "" || request.TTL <= 0 {
		return nil, fmt.Errorf("%w: key, owner id and positive TTL are required", ErrInvalidMaintenanceLease)
	}
	if s.native == nil {
		return nil, fmt.Errorf("native maintenance lease runtime is required")
	}
	result, err := (&maintenance.Store{Invoker: s.native}).Acquire(ctx, request.Key, request.OwnerID, request.TTL)
	if err != nil {
		return nil, err
	}
	return &MaintenanceLeaseAcquireResult{Acquired: result.Acquired, Lease: MaintenanceLease{
		Key: result.Lease.Key, OwnerID: result.Lease.OwnerID,
		Token: result.Lease.Token, LeaseUntil: result.Lease.LeaseUntil,
	}}, nil
}

func (s *datlyService) RenewMaintenanceLease(ctx context.Context, lease MaintenanceLease, ttl time.Duration) (*MaintenanceLeaseRenewResult, error) {
	lease = normalizeMaintenanceLease(lease)
	if err := validateMaintenanceLease(lease); err != nil || ttl <= 0 {
		if err == nil {
			err = fmt.Errorf("positive TTL is required")
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidMaintenanceLease, err)
	}
	if s.native == nil {
		return nil, fmt.Errorf("native maintenance lease runtime is required")
	}
	renewed, until, err := (&maintenance.Store{Invoker: s.native}).Renew(ctx, maintenance.Lease{
		Key: lease.Key, OwnerID: lease.OwnerID, Token: lease.Token, LeaseUntil: lease.LeaseUntil,
	}, ttl)
	if err != nil {
		return nil, err
	}
	return &MaintenanceLeaseRenewResult{Renewed: renewed, LeaseUntil: until}, nil
}

func (s *datlyService) ReleaseMaintenanceLease(ctx context.Context, lease MaintenanceLease) (bool, error) {
	lease = normalizeMaintenanceLease(lease)
	if err := validateMaintenanceLease(lease); err != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidMaintenanceLease, err)
	}
	if s.native == nil {
		return false, fmt.Errorf("native maintenance lease runtime is required")
	}
	return (&maintenance.Store{Invoker: s.native}).Release(ctx, maintenance.Lease{
		Key: lease.Key, OwnerID: lease.OwnerID, Token: lease.Token, LeaseUntil: lease.LeaseUntil,
	})
}

// DeleteExpiredMaintenanceLeases removes only rows whose lease expired more
// than seven days ago. The retention is intentionally fixed.
func (s *datlyService) DeleteExpiredMaintenanceLeases(ctx context.Context, lease MaintenanceLease) (int64, error) {
	lease = normalizeMaintenanceLease(lease)
	if err := validateMaintenanceLease(lease); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidMaintenanceLease, err)
	}
	if s.native == nil {
		return 0, fmt.Errorf("native maintenance lease runtime is required")
	}
	deleted, err := (&maintenance.Store{Invoker: s.native}).DeleteExpired(ctx, maintenance.Lease{
		Key: lease.Key, OwnerID: lease.OwnerID, Token: lease.Token, LeaseUntil: lease.LeaseUntil,
	})
	if errors.Is(err, maintenance.ErrLeaseLost) {
		return 0, ErrMaintenanceLeaseLost
	}
	return deleted, err
}

func normalizeMaintenanceLease(lease MaintenanceLease) MaintenanceLease {
	lease.Key = strings.TrimSpace(lease.Key)
	lease.OwnerID = strings.TrimSpace(lease.OwnerID)
	lease.Token = strings.TrimSpace(lease.Token)
	lease.LeaseUntil = lease.LeaseUntil.UTC()
	return lease
}

func validateMaintenanceLease(lease MaintenanceLease) error {
	if lease.Key == "" || lease.OwnerID == "" || lease.Token == "" {
		return fmt.Errorf("key, owner id and token are required")
	}
	return nil
}

func isMaintenanceMySQLDriver(driver string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(driver)), "mysql")
}

func isMaintenanceSQLiteDriver(driver string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(driver)), "sqlite")
}

func maintenanceLeaseWrite[T any](ctx context.Context, gate, driver string, fn func() (T, error)) (T, error) {
	deadline := time.Now().Add(5 * time.Second)
	delay := 10 * time.Millisecond
	for {
		result, err := sqlitewrite.Do(ctx, gate, fn)
		if err == nil || !isMaintenanceSQLiteDriver(driver) || !isRetryableMaintenanceSQLiteError(err) || !time.Now().Before(deadline) {
			return result, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			var zero T
			return zero, ctx.Err()
		case <-timer.C:
		}
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}

func isRetryableMaintenanceSQLiteError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked")
}
