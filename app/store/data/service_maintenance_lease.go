package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viant/agently-core/internal/sqlitewrite"
)

const maintenanceLeaseExpiredRetention = 7 * 24 * time.Hour

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
	db, driver, err := s.dbWithDriver()
	if err != nil {
		return nil, err
	}
	return maintenanceLeaseWrite(ctx, s.writeGate, driver, func() (*MaintenanceLeaseAcquireResult, error) {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return nil, err
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()

		now, err := maintenanceLeaseDatabaseNow(ctx, tx, driver)
		if err != nil {
			return nil, err
		}
		lease := MaintenanceLease{
			Key:        request.Key,
			OwnerID:    request.OwnerID,
			Token:      uuid.NewString(),
			LeaseUntil: now.Add(request.TTL),
		}
		updated, err := tx.ExecContext(ctx, `UPDATE maintenance_lease
SET owner_id = ?, lease_token = ?, lease_until = ?, updated_at = ?
WHERE lease_key = ? AND lease_until <= ?`,
			lease.OwnerID, lease.Token, lease.LeaseUntil, now, lease.Key, now)
		if err != nil {
			return nil, err
		}
		affected, err := updated.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			insertSQL := `INSERT OR IGNORE INTO maintenance_lease
(lease_key, owner_id, lease_token, lease_until, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`
			if isMaintenanceMySQLDriver(driver) {
				insertSQL = `INSERT IGNORE INTO maintenance_lease
(lease_key, owner_id, lease_token, lease_until, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`
			}
			inserted, insertErr := tx.ExecContext(ctx, insertSQL,
				lease.Key, lease.OwnerID, lease.Token, lease.LeaseUntil, now, now)
			if insertErr != nil {
				return nil, insertErr
			}
			affected, err = inserted.RowsAffected()
			if err != nil {
				return nil, err
			}
		}

		result := &MaintenanceLeaseAcquireResult{Acquired: affected > 0, Lease: lease}
		if !result.Acquired {
			current, found, loadErr := loadMaintenanceLease(ctx, tx, driver, request.Key, false)
			if loadErr != nil {
				return nil, loadErr
			}
			if !found {
				return nil, fmt.Errorf("acquire maintenance lease %q: lease row disappeared", request.Key)
			}
			current.Token = ""
			result.Lease = current
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		committed = true
		return result, nil
	})
}

func (s *datlyService) RenewMaintenanceLease(ctx context.Context, lease MaintenanceLease, ttl time.Duration) (*MaintenanceLeaseRenewResult, error) {
	lease = normalizeMaintenanceLease(lease)
	if err := validateMaintenanceLease(lease); err != nil || ttl <= 0 {
		if err == nil {
			err = fmt.Errorf("positive TTL is required")
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidMaintenanceLease, err)
	}
	db, driver, err := s.dbWithDriver()
	if err != nil {
		return nil, err
	}
	return maintenanceLeaseWrite(ctx, s.writeGate, driver, func() (*MaintenanceLeaseRenewResult, error) {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		now, err := maintenanceLeaseDatabaseNow(ctx, tx, driver)
		if err != nil {
			return nil, err
		}
		leaseUntil := now.Add(ttl)
		res, err := tx.ExecContext(ctx, `UPDATE maintenance_lease
SET lease_until = ?, updated_at = ?
WHERE lease_key = ? AND owner_id = ? AND lease_token = ? AND lease_until > ?`,
			leaseUntil, now, lease.Key, lease.OwnerID, lease.Token, now)
		if err != nil {
			return nil, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		result := &MaintenanceLeaseRenewResult{Renewed: affected == 1, LeaseUntil: leaseUntil}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return result, nil
	})
}

func (s *datlyService) ReleaseMaintenanceLease(ctx context.Context, lease MaintenanceLease) (bool, error) {
	lease = normalizeMaintenanceLease(lease)
	if err := validateMaintenanceLease(lease); err != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidMaintenanceLease, err)
	}
	db, driver, err := s.dbWithDriver()
	if err != nil {
		return false, err
	}
	return maintenanceLeaseWrite(ctx, s.writeGate, driver, func() (bool, error) {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return false, err
		}
		defer func() { _ = tx.Rollback() }()
		now, err := maintenanceLeaseDatabaseNow(ctx, tx, driver)
		if err != nil {
			return false, err
		}
		res, err := tx.ExecContext(ctx, `UPDATE maintenance_lease
SET lease_until = ?, updated_at = ?
WHERE lease_key = ? AND owner_id = ? AND lease_token = ?`,
			now, now, lease.Key, lease.OwnerID, lease.Token)
		if err != nil {
			return false, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		if err = tx.Commit(); err != nil {
			return false, err
		}
		return affected == 1, nil
	})
}

// DeleteExpiredMaintenanceLeases removes only rows whose lease expired more
// than seven days ago. The retention is intentionally fixed.
func (s *datlyService) DeleteExpiredMaintenanceLeases(ctx context.Context, lease MaintenanceLease) (int64, error) {
	lease = normalizeMaintenanceLease(lease)
	if err := validateMaintenanceLease(lease); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidMaintenanceLease, err)
	}
	db, driver, err := s.dbWithDriver()
	if err != nil {
		return 0, err
	}
	return maintenanceLeaseWrite(ctx, s.writeGate, driver, func() (int64, error) {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return 0, err
		}
		defer func() { _ = tx.Rollback() }()
		now, err := lockMaintenanceLeaseTx(ctx, tx, driver, lease)
		if err != nil {
			return 0, err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM maintenance_lease
WHERE lease_until <= ? AND NOT (lease_key = ? AND owner_id = ? AND lease_token = ?)`,
			now.Add(-maintenanceLeaseExpiredRetention), lease.Key, lease.OwnerID, lease.Token)
		if err != nil {
			return 0, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		if err = tx.Commit(); err != nil {
			return 0, err
		}
		return affected, nil
	})
}

func lockMaintenanceLeaseTx(ctx context.Context, tx *sql.Tx, driver string, lease MaintenanceLease) (time.Time, error) {
	lease = normalizeMaintenanceLease(lease)
	if err := validateMaintenanceLease(lease); err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrInvalidMaintenanceLease, err)
	}
	now, err := maintenanceLeaseDatabaseNow(ctx, tx, driver)
	if err != nil {
		return time.Time{}, err
	}
	if isMaintenanceMySQLDriver(driver) {
		var marker int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM maintenance_lease
WHERE lease_key = ? AND owner_id = ? AND lease_token = ? AND lease_until > ?
FOR UPDATE`, lease.Key, lease.OwnerID, lease.Token, now).Scan(&marker)
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, ErrMaintenanceLeaseLost
		}
		if err != nil {
			return time.Time{}, err
		}
		return now, nil
	}
	if !isMaintenanceSQLiteDriver(driver) {
		return time.Time{}, fmt.Errorf("unsupported maintenance lease database driver %q", driver)
	}
	res, err := tx.ExecContext(ctx, `UPDATE maintenance_lease SET lease_token = lease_token
WHERE lease_key = ? AND owner_id = ? AND lease_token = ? AND lease_until > ?`,
		lease.Key, lease.OwnerID, lease.Token, now)
	if err != nil {
		return time.Time{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return time.Time{}, err
	}
	if affected != 1 {
		return time.Time{}, ErrMaintenanceLeaseLost
	}
	return now, nil
}

func loadMaintenanceLease(ctx context.Context, tx *sql.Tx, driver, key string, lock bool) (MaintenanceLease, bool, error) {
	query := `SELECT lease_key, owner_id, lease_token, CAST(lease_until AS CHAR)
FROM maintenance_lease WHERE lease_key = ?`
	if lock && isMaintenanceMySQLDriver(driver) {
		query += " FOR UPDATE"
	}
	var result MaintenanceLease
	var rawUntil sql.NullString
	err := tx.QueryRowContext(ctx, query, key).Scan(&result.Key, &result.OwnerID, &result.Token, &rawUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return MaintenanceLease{}, false, nil
	}
	if err != nil {
		return MaintenanceLease{}, false, err
	}
	until, ok := parseDBTime(rawUntil.String)
	if !rawUntil.Valid || !ok {
		return MaintenanceLease{}, false, fmt.Errorf("maintenance lease %q has invalid lease_until %q", key, rawUntil.String)
	}
	result.LeaseUntil = until
	return normalizeMaintenanceLease(result), true, nil
}

type maintenanceLeaseQueryer interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func maintenanceLeaseDatabaseNow(ctx context.Context, queryer maintenanceLeaseQueryer, driver string) (time.Time, error) {
	query := "SELECT CAST(CURRENT_TIMESTAMP AS TEXT)"
	if isMaintenanceMySQLDriver(driver) {
		query = "SELECT CAST(UTC_TIMESTAMP(6) AS CHAR)"
	} else if !isMaintenanceSQLiteDriver(driver) {
		return time.Time{}, fmt.Errorf("unsupported maintenance lease database driver %q", driver)
	}
	var raw string
	if err := queryer.QueryRowContext(ctx, query).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	now, ok := parseDBTime(raw)
	if !ok {
		return time.Time{}, fmt.Errorf("database returned invalid current time %q", raw)
	}
	return now, nil
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
