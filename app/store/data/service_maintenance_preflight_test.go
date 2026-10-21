package data

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/internal/store/conversationmaintenance"
	"github.com/viant/agently-core/internal/store/maintenancelease"
	"github.com/viant/agently-core/internal/store/orphanmaintenance"
	"github.com/viant/agently-core/internal/store/scheduledmaintenance"
	"github.com/viant/agently-core/internal/store/technicalmaintenance"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

func TestMaintenancePrivateMutationsRequirePreflight(t *testing.T) {
	svc, db := newNativeLeaseServiceWithDB(t)
	runtime := svc.(*datlyService).native
	publicLease := acquireTestMaintenanceLease(t, svc)
	lease := maintenancelease.Lease{Key: publicLease.Key, OwnerID: publicLease.OwnerID, Token: publicLease.Token}
	now := time.Now().UTC()
	for _, entry := range []struct {
		name, path string
		input      any
	}{
		{"conversation", "/v1/internal/agently/conversation/maintenance", &conversationmaintenance.Input{RootID: "missing", Kind: conversationmaintenance.Interactive, Mode: scheduledmaintenance.DeleteMode, InactiveBefore: now, Lease: lease}},
		{"scheduled", "/v1/internal/agently/scheduled-run/maintenance", &scheduledmaintenance.Input{RunID: "missing", Mode: scheduledmaintenance.DeleteMode, InactiveBefore: now, Lease: lease}},
		{"orphan", "/v1/internal/agently/orphan-maintenance/apply", &orphanmaintenance.ApplyInput{RuleID: "call_payload.unused", RecordID: "missing", OlderThan: now, Lease: lease}},
		{"technical", "/v1/internal/agently/technical-maintenance", &technicalmaintenance.Request{Kind: technicalmaintenance.Session, Scope: technicalmaintenance.Unclassified, RecordID: "missing", Mode: technicalmaintenance.Delete, OlderThan: now, EvaluatedAt: now, Lease: lease}},
		{"purge", "/v1/internal/agently/maintenance-lease/purge", &maintenancelease.PurgeInput{Key: lease.Key, OwnerID: lease.OwnerID, Token: lease.Token}},
	} {
		t.Run(entry.name, func(t *testing.T) {
			var component spec.Key
			switch entry.name {
			case "conversation":
				component = spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[conversationmaintenance.Component]().PkgPath(), Name: "ConversationMaintenance"}
			case "scheduled":
				component = spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[scheduledmaintenance.Component]().PkgPath(), Name: "ScheduledRunMaintenance"}
			case "orphan":
				component = spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[orphanmaintenance.ApplyComponent]().PkgPath(), Name: "OrphanMaintenanceApply"}
			case "technical":
				component = spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[technicalmaintenance.Component]().PkgPath(), Name: "TechnicalMaintenance"}
			case "purge":
				component = spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[maintenancelease.PurgeComponent]().PkgPath(), Name: "MaintenanceLeasePurge"}
			}
			_, err := runtime.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: component, Route: spec.RouteRef{Method: "POST", Path: entry.path}}, Input: entry.input})
			require.ErrorIs(t, err, maintenancelease.ErrUncheckedLease)
		})
	}
	var token string
	require.NoError(t, db.QueryRow("SELECT lease_token FROM maintenance_lease WHERE lease_key = ?", lease.Key).Scan(&token))
	require.Equal(t, lease.Token, token)
}
