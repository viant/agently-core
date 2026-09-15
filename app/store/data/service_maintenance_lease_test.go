package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestMaintenanceLease_SQLiteAcquireRenewReleaseAndFence(t *testing.T) {
	svc, _ := newSeededServiceWithDB(t)
	ctx := context.Background()

	first := acquireTestMaintenanceLease(t, svc, "lease-test", "worker-a")
	blocked, err := svc.AcquireMaintenanceLease(ctx, MaintenanceLeaseAcquireRequest{
		Key: "lease-test", OwnerID: "worker-b", TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("AcquireMaintenanceLease(blocked): %v", err)
	}
	if blocked == nil || blocked.Acquired || blocked.Lease.OwnerID != "worker-a" || blocked.Lease.Token != "" {
		t.Fatalf("blocked acquisition = %#v", blocked)
	}

	renewed, err := svc.RenewMaintenanceLease(ctx, first, 10*time.Minute)
	if err != nil {
		t.Fatalf("RenewMaintenanceLease(): %v", err)
	}
	if renewed == nil || !renewed.Renewed || !renewed.LeaseUntil.After(first.LeaseUntil) {
		t.Fatalf("renew result = %#v, original=%#v", renewed, first)
	}

	released, err := svc.ReleaseMaintenanceLease(ctx, first)
	if err != nil || !released {
		t.Fatalf("ReleaseMaintenanceLease() released=%t err=%v", released, err)
	}
	second := acquireTestMaintenanceLease(t, svc, "lease-test", "worker-b")
	if second.Token == first.Token {
		t.Fatalf("reacquisition reused fencing token %q", second.Token)
	}
	if _, err := svc.DeleteExpiredMaintenanceLeases(ctx, first); !errors.Is(err, ErrMaintenanceLeaseLost) {
		t.Fatalf("stale lease fence error = %v", err)
	}
}

func TestMaintenanceLease_SQLiteDeletesOnlyRowsExpiredOverSevenDays(t *testing.T) {
	svc, db := newSeededServiceWithDB(t)
	ctx := context.Background()
	guard := acquireTestMaintenanceLease(t, svc, "cleanup-guard", "worker-a")
	now := time.Now().UTC()

	for _, row := range []struct {
		key   string
		until time.Time
	}{
		{key: "expired-old", until: now.Add(-8 * 24 * time.Hour)},
		{key: "expired-recent", until: now.Add(-6 * 24 * time.Hour)},
		{key: "active", until: now.Add(time.Hour)},
	} {
		if _, err := db.Exec(`INSERT INTO maintenance_lease
(lease_key, owner_id, lease_token, lease_until, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, row.key, "other", row.key+"-token", row.until, now.Add(-10*24*time.Hour), now); err != nil {
			t.Fatalf("insert lease %q: %v", row.key, err)
		}
	}

	deleted, err := svc.DeleteExpiredMaintenanceLeases(ctx, guard)
	if err != nil {
		t.Fatalf("DeleteExpiredMaintenanceLeases(): %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted rows = %d, want 1", deleted)
	}
	assertMaintenanceLeaseRowCount(t, db, "expired-old", 0)
	assertMaintenanceLeaseRowCount(t, db, "expired-recent", 1)
	assertMaintenanceLeaseRowCount(t, db, "active", 1)
	assertMaintenanceLeaseRowCount(t, db, guard.Key, 1)
}

func acquireTestMaintenanceLease(t *testing.T, svc Service, keyAndOwner ...string) MaintenanceLease {
	t.Helper()
	key := "test-maintenance"
	owner := "test-worker"
	if len(keyAndOwner) > 0 {
		key = keyAndOwner[0]
	}
	if len(keyAndOwner) > 1 {
		owner = keyAndOwner[1]
	}
	result, err := svc.AcquireMaintenanceLease(context.Background(), MaintenanceLeaseAcquireRequest{
		Key: key, OwnerID: owner, TTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("AcquireMaintenanceLease(%q): %v", key, err)
	}
	if result == nil || !result.Acquired || result.Lease.Token == "" {
		t.Fatalf("AcquireMaintenanceLease(%q) = %#v", key, result)
	}
	return result.Lease
}

func assertMaintenanceLeaseRowCount(t *testing.T, db *sql.DB, key string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM maintenance_lease WHERE lease_key = ?`, key).Scan(&got); err != nil {
		t.Fatalf("count maintenance lease %q: %v", key, err)
	}
	if got != want {
		t.Fatalf("maintenance lease %q count = %d, want %d", key, got, want)
	}
}
