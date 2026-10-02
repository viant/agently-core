package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/datly/bootstrap/connector"
)

func TestMaintenanceLease_MySQLAcquireRenewReleaseAndFence(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open(mysql): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err = db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL: %v", err)
	}

	_, file, _, _ := runtime.Caller(0)
	sourceRoot := filepath.Join(filepath.Dir(file), "..", "..", "..")
	server, err := native.New(ctx, native.Options{SourceRoot: sourceRoot, Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
	if err != nil {
		t.Fatalf("native.New(): %v", err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	service := &datlyService{native: server}
	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	leaseKey := "test-maintenance-lease-" + suffix
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM maintenance_lease WHERE lease_key = ?`, leaseKey) })

	first := acquireTestMaintenanceLease(t, service, leaseKey, "worker-a-"+suffix)
	blocked, err := service.AcquireMaintenanceLease(ctx, MaintenanceLeaseAcquireRequest{
		Key: leaseKey, OwnerID: "worker-b-" + suffix, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("AcquireMaintenanceLease(blocked): %v", err)
	}
	if blocked == nil || blocked.Acquired || blocked.Lease.OwnerID != first.OwnerID || blocked.Lease.Token != "" {
		t.Fatalf("blocked acquisition = %#v", blocked)
	}

	renewed, err := service.RenewMaintenanceLease(ctx, first, 10*time.Minute)
	if err != nil {
		t.Fatalf("RenewMaintenanceLease(): %v", err)
	}
	if renewed == nil || !renewed.Renewed || !renewed.LeaseUntil.After(first.LeaseUntil) {
		t.Fatalf("renew result = %#v, original=%#v", renewed, first)
	}
	released, err := service.ReleaseMaintenanceLease(ctx, first)
	if err != nil || !released {
		t.Fatalf("ReleaseMaintenanceLease(): released=%t err=%v", released, err)
	}

	second := acquireTestMaintenanceLease(t, service, leaseKey, "worker-b-"+suffix)
	if second.Token == first.Token {
		t.Fatalf("reacquisition reused fencing token %q", second.Token)
	}
	if _, err = service.DeleteExpiredMaintenanceLeases(ctx, first); !errors.Is(err, ErrMaintenanceLeaseLost) {
		t.Fatalf("superseded lease fence error = %v", err)
	}
}
