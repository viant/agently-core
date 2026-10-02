// Package maintenancelease coordinates generated Datly components for
// distributed maintenance lease acquisition and fencing.
package maintenancelease

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	read "github.com/viant/agently-core/internal/datly/maintenancelease/read"
	write "github.com/viant/agently-core/internal/datly/maintenancelease/write"

	"github.com/viant/agently-core/internal/datly/dbtime"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/sqlx/io/errx"
	xhandler "github.com/viant/xdatly/handler"
)

var ErrInvalidLease = errors.New("invalid maintenance lease")

type Lease struct {
	Key, OwnerID, Token string
	LeaseUntil          time.Time
}

type AcquireResult struct {
	Acquired bool
	Lease    Lease
}

type Store struct{ Invoker dexec.ComponentInvoker }

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/maintenance-lease"},
}
var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/maintenance-lease"},
}

func trusted() []locator.Provider {
	return []locator.Provider{provider.Named("maintenanceaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return true, true, nil })}
}

func (s *Store) snapshot(ctx context.Context, input *read.Input) ([]*read.LeaseSnapshot, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("maintenance lease component invoker is required")
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input, Providers: trusted()})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("maintenance lease reader returned %T", value)
	}
	return out.Data, nil
}

func (s *Store) databaseNow(ctx context.Context) (time.Time, error) {
	input := &read.Input{}
	input.SetMode("clock")
	rows, err := s.snapshot(ctx, input)
	if err != nil {
		return time.Time{}, err
	}
	if len(rows) != 1 || rows[0] == nil {
		return time.Time{}, fmt.Errorf("maintenance clock returned %d rows", len(rows))
	}
	return dbtime.ParseDatabaseUTC(rows[0].DbNow)
}

func (s *Store) lookup(ctx context.Context, key string) (*Lease, error) {
	input := &read.Input{}
	input.SetLeaseKey(key)
	rows, err := s.snapshot(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 || rows[0] == nil {
		return nil, fmt.Errorf("maintenance lease lookup returned %d rows", len(rows))
	}
	row := rows[0]
	if row.LeaseKey == nil {
		return nil, nil
	}
	if row.OwnerId == nil || row.LeaseToken == nil || row.LeaseUntil == nil {
		return nil, fmt.Errorf("maintenance lease %q has incomplete persisted fields", key)
	}
	return &Lease{Key: *row.LeaseKey, OwnerID: *row.OwnerId, Token: *row.LeaseToken, LeaseUntil: row.LeaseUntil.UTC()}, nil
}

func (s *Store) patch(ctx context.Context, input *write.Input) error {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("maintenance lease component invoker is required")
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input, Providers: trusted()})
	if err != nil {
		return err
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("maintenance lease writer returned %T", value)
	}
	return nil
}

func conflicting(err error) bool {
	var conflict *xhandler.Conflict
	return errors.Is(err, write.ErrConflict) || errors.Is(err, write.ErrNotFound) || errors.As(err, &conflict) || errx.IsDuplicateKey(err)
}

// Acquire returns the current active holder without revealing its fencing
// token. Expired claims compare the previously observed token and server time.
func (s *Store) Acquire(ctx context.Context, key, ownerID string, ttl time.Duration) (*AcquireResult, error) {
	key, ownerID = strings.TrimSpace(key), strings.TrimSpace(ownerID)
	if key == "" || ownerID == "" || ttl <= 0 {
		return nil, ErrInvalidLease
	}
	deadline := time.Now().Add(5 * time.Second)
	delay := 10 * time.Millisecond
	var lastErr error
	for {
		now, err := s.databaseNow(ctx)
		if err != nil {
			if !retryableBusy(err) {
				return nil, err
			}
			lastErr = err
		} else {
			current, err := s.lookup(ctx, key)
			if err != nil {
				if !retryableBusy(err) {
					return nil, err
				}
				lastErr = err
			} else {
				if current != nil && current.LeaseUntil.After(now) {
					current.Token = ""
					return &AcquireResult{Lease: *current}, nil
				}
				lease := Lease{Key: key, OwnerID: ownerID, Token: uuid.NewString(), LeaseUntil: now.Add(ttl)}
				row := &write.Lease{}
				row.SetLeaseKey(key)
				row.SetOwnerId(ownerID)
				row.SetLeaseToken(lease.Token)
				row.SetLeaseUntil(lease.LeaseUntil)
				row.SetUpdatedAt(now)
				input := &write.Input{}
				if current == nil {
					row.SetCreatedAt(now)
					input.SetMode("create")
				} else {
					input.SetMode("claim")
					input.SetExpectedToken(current.Token)
					input.SetExpiresBefore(now)
				}
				input.SetLeases([]*write.Lease{row})
				if err := s.patch(ctx, input); err == nil {
					return &AcquireResult{Acquired: true, Lease: lease}, nil
				} else if !conflicting(err) && !retryableBusy(err) {
					return nil, err
				} else {
					lastErr = err
				}
			}
		}
		if !time.Now().Before(deadline) {
			if lastErr != nil {
				return nil, fmt.Errorf("maintenance lease %q acquisition: %w", key, lastErr)
			}
			return nil, fmt.Errorf("maintenance lease %q changed during acquisition", key)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}

func retryableBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked")
}

func (s *Store) Renew(ctx context.Context, lease Lease, ttl time.Duration) (bool, time.Time, error) {
	lease = normalize(lease)
	if !valid(lease) || ttl <= 0 {
		return false, time.Time{}, ErrInvalidLease
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return false, time.Time{}, err
	}
	until := now.Add(ttl)
	row := &write.Lease{}
	row.SetLeaseKey(lease.Key)
	row.SetLeaseUntil(until)
	row.SetUpdatedAt(now)
	input := &write.Input{}
	input.SetMode("renew")
	input.SetExpectedOwner(lease.OwnerID)
	input.SetExpectedToken(lease.Token)
	input.SetLiveAfter(now)
	input.SetLeases([]*write.Lease{row})
	if err := s.patch(ctx, input); err != nil {
		if conflicting(err) {
			return false, until, nil
		}
		return false, time.Time{}, err
	}
	return true, until, nil
}

func (s *Store) Release(ctx context.Context, lease Lease) (bool, error) {
	lease = normalize(lease)
	if !valid(lease) {
		return false, ErrInvalidLease
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return false, err
	}
	row := &write.Lease{}
	row.SetLeaseKey(lease.Key)
	row.SetLeaseUntil(now)
	row.SetUpdatedAt(now)
	input := &write.Input{}
	input.SetMode("release")
	input.SetExpectedOwner(lease.OwnerID)
	input.SetExpectedToken(lease.Token)
	input.SetLeases([]*write.Lease{row})
	if err := s.patch(ctx, input); err != nil {
		if conflicting(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func normalize(lease Lease) Lease {
	lease.Key = strings.TrimSpace(lease.Key)
	lease.OwnerID = strings.TrimSpace(lease.OwnerID)
	lease.Token = strings.TrimSpace(lease.Token)
	lease.LeaseUntil = lease.LeaseUntil.UTC()
	return lease
}

func valid(lease Lease) bool { return lease.Key != "" && lease.OwnerID != "" && lease.Token != "" }
