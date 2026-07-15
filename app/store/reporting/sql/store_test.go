package sql

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/data"
	reportfs "github.com/viant/agently-core/app/store/reporting/fs"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
	authsvc "github.com/viant/agently-core/service/auth"
	reportingsvc "github.com/viant/agently-core/service/reporting"
	fsstate "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
)

func TestStore_NewDoesNotProvisionReportingSchema(t *testing.T) {
	ctx := context.Background()
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "reporting-store-empty-schema")
	t.Cleanup(cleanup)

	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New() error = %v", err)
	}
	if err = dao.AddConnectors(ctx, view.NewConnector("agently", "sqlite", dbPath)); err != nil {
		t.Fatalf("AddConnectors() error = %v", err)
	}
	isolateReportingComponents(t, dao, "agently")

	if _, err = New(ctx, dao, "agently", nil, nil); err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var tableCount int
	if err = db.QueryRowContext(ctx, `
SELECT COUNT(1)
FROM sqlite_master
WHERE type = 'table' AND name GLOB 'report_*'`).Scan(&tableCount); err != nil {
		t.Fatalf("query reporting tables error = %v", err)
	}
	if tableCount != 0 {
		t.Fatalf("reporting table count = %d, want 0", tableCount)
	}
}

func TestStore_SharedArtifactRoundTripIsScopedByOwner(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(ctx, dao, "agently", nil, reportmemory.New())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ownerCtx := authsvc.InjectUser(ctx, "owner-1")
	otherCtx := authsvc.InjectUser(ctx, "owner-2")
	createdAt := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	artifact := &reportshareartifact.Record{
		ArtifactID:      "shared-1",
		ArtifactRef:     "reportBuilder.savedReportPayload://forecasting_q3",
		OwnerID:         "owner-1",
		OwnerRef:        "user://owner-1",
		Kind:            "reportBuilder.savedReportPayload",
		Lifecycle:       "draft",
		Version:         1,
		ReportID:        "forecasting_q3",
		Title:           "Forecasting Q3",
		DocumentVersion: 7,
		Document:        []byte(`{"kind":"reportDocument","id":"forecasting_q3"}`),
		ReportSpec:      []byte(`{"kind":"reportSpec","version":1}`),
		CompileState:    []byte(`{"status":"clean"}`),
		Metadata:        []byte(`{"workspace":"steward"}`),
		CreatedAt:       createdAt,
	}

	if err := client.CreateSharedArtifact(ownerCtx, artifact); err != nil {
		t.Fatalf("CreateSharedArtifact() error = %v", err)
	}

	got, err := client.GetSharedArtifact(ownerCtx, "shared-1")
	if err != nil {
		t.Fatalf("GetSharedArtifact(owner) error = %v", err)
	}
	if got == nil || got.OwnerID != "owner-1" || got.Title != "Forecasting Q3" {
		t.Fatalf("GetSharedArtifact(owner) = %+v, want persisted owner row", got)
	}

	if _, err := client.GetSharedArtifact(otherCtx, "shared-1"); err == nil {
		t.Fatalf("GetSharedArtifact(other) expected not found")
	}

	listed, err := client.ListSharedArtifacts(ownerCtx)
	if err != nil {
		t.Fatalf("ListSharedArtifacts(owner) error = %v", err)
	}
	if len(listed) != 1 || listed[0] == nil || listed[0].ArtifactID != "shared-1" {
		t.Fatalf("ListSharedArtifacts(owner) = %+v, want one owned artifact", listed)
	}

	otherListed, err := client.ListSharedArtifacts(otherCtx)
	if err != nil {
		t.Fatalf("ListSharedArtifacts(other) error = %v", err)
	}
	if len(otherListed) != 0 {
		t.Fatalf("ListSharedArtifacts(other) = %+v, want empty", otherListed)
	}

	updatedAt := createdAt.Add(2 * time.Hour)
	got.Title = "Forecasting Q3 Updated"
	got.Version = 2
	got.UpdatedAt = &updatedAt
	if err := client.UpdateSharedArtifact(ownerCtx, got); err != nil {
		t.Fatalf("UpdateSharedArtifact(owner) error = %v", err)
	}

	afterUpdate, err := client.GetSharedArtifact(ownerCtx, "shared-1")
	if err != nil {
		t.Fatalf("GetSharedArtifact(after update) error = %v", err)
	}
	if afterUpdate == nil || afterUpdate.Title != "Forecasting Q3 Updated" || afterUpdate.Version != 2 {
		t.Fatalf("GetSharedArtifact(after update) = %+v, want updated row", afterUpdate)
	}

	got.OwnerID = "owner-2"
	got.OwnerRef = "user://owner-2"
	if err := client.UpdateSharedArtifact(otherCtx, got); err == nil {
		t.Fatalf("UpdateSharedArtifact(other) expected not found")
	}
}

func TestStore_ImportsFilesystemReportingState(t *testing.T) {
	root := t.TempDir()
	stateStore := fsstate.NewStateStore(root)
	fsClient := reportfs.New(stateStore)
	fsAudit := reportfs.NewAuditSink(stateStore)
	now := time.Date(2026, 7, 13, 18, 0, 0, 0, time.UTC)

	mustNoErr(t, fsClient.CreateJob(context.Background(), &reportjob.Record{
		JobID:       "job-fs-1",
		ArtifactRef: "report://draft/fs",
		OwnerID:     "user-fs",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		SubmittedAt: now,
	}))
	mustNoErr(t, fsClient.PutArtifact(context.Background(), &reportartifact.Record{
		ArtifactID:  "artifact-fs-1",
		JobID:       "job-fs-1",
		ArtifactRef: "report://draft/fs",
		OwnerID:     "user-fs",
		Format:      "pdf",
		ContentType: "application/pdf",
		Data:        []byte("%PDF-fs"),
		CreatedAt:   now,
	}))
	mustNoErr(t, fsClient.CreateSharedArtifact(context.Background(), &reportshareartifact.Record{
		ArtifactID:      "shared-fs-1",
		ArtifactRef:     "reportBuilder.savedReportPayload://fs_report",
		OwnerID:         "user-fs",
		OwnerRef:        "user://user-fs",
		Kind:            "reportBuilder.savedReportPayload",
		Lifecycle:       "draft",
		Version:         1,
		ReportID:        "fs_report",
		Title:           "FS Report",
		DocumentVersion: 2,
		Document:        []byte(`{"kind":"reportDocument","id":"fs_report"}`),
		CreatedAt:       now,
	}))
	mustNoErr(t, fsAudit.Record(context.Background(), &reportingsvc.AuditEvent{
		EventType:   "report.saved",
		ArtifactRef: "reportBuilder.savedReportPayload://fs_report",
		Version:     1,
		ArtifactID:  "shared-fs-1",
		ActorID:     "user-fs",
		ActorRef:    "user://user-fs",
		OccurredAt:  now,
		Metadata:    map[string]interface{}{"source": "fs"},
	}))

	dao, err := data.NewDatlyInMemory(context.Background())
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(context.Background(), dao, "agently", stateStore, fsClient)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ownerCtx := authsvc.InjectUser(context.Background(), "user-fs")
	jobs, err := client.ListJobs(ownerCtx)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].JobID != "job-fs-1" {
		t.Fatalf("ListJobs() = %+v, want migrated job", jobs)
	}

	artifacts, err := client.ListArtifacts(ownerCtx)
	if err != nil {
		t.Fatalf("ListArtifacts() error = %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ArtifactID != "artifact-fs-1" {
		t.Fatalf("ListArtifacts() = %+v, want migrated artifact", artifacts)
	}

	shared, err := client.ListSharedArtifacts(ownerCtx)
	if err != nil {
		t.Fatalf("ListSharedArtifacts() error = %v", err)
	}
	if len(shared) != 1 || shared[0].ArtifactID != "shared-fs-1" {
		t.Fatalf("ListSharedArtifacts() = %+v, want migrated shared artifact", shared)
	}

	sqlStore, ok := client.(*Store)
	if !ok || sqlStore == nil {
		t.Fatalf("expected *Store, got %#v", client)
	}
	db, err := sqlStore.dbHandle()
	if err != nil {
		t.Fatalf("dbHandle() error = %v", err)
	}
	var auditCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(1) FROM report_audit_event WHERE actor_id = ?`, "user-fs").Scan(&auditCount); err != nil {
		t.Fatalf("QueryRowContext(audit count) error = %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("auditCount = %d, want 1", auditCount)
	}
}

func TestStore_ImportsFilesystemStateWhenComponentsAlreadyRegistered(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	isolateReportingComponents(t, dao, "agently")

	if _, err = New(ctx, dao, "agently", nil, nil); err != nil {
		t.Fatalf("first New() error = %v", err)
	}

	stateStore := fsstate.NewStateStore(t.TempDir())
	fsClient := reportfs.New(stateStore)
	mustNoErr(t, fsClient.CreateJob(ctx, &reportjob.Record{
		JobID:       "job-fs-loaded-components",
		ArtifactRef: "report://draft/loaded-components",
		OwnerID:     "user-fs-loaded-components",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		SubmittedAt: time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC),
	}))

	client, err := New(ctx, dao, "agently", stateStore, fsClient)
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}

	ownerCtx := authsvc.InjectUser(ctx, "user-fs-loaded-components")
	jobs, err := client.ListJobs(ownerCtx)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0] == nil || jobs[0].JobID != "job-fs-loaded-components" {
		t.Fatalf("ListJobs() = %+v, want imported job from second Store initialization", jobs)
	}
}

func TestStore_InternalAccessBypassesOwnerScopeForWorkerFlows(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(ctx, dao, "agently", nil, reportmemory.New())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store, ok := client.(*Store)
	if !ok || store == nil {
		t.Fatalf("expected *Store, got %#v", client)
	}

	ownerCtx := authsvc.InjectUser(ctx, "owner-1")
	internalCtx := WithInternalAccess(ctx)
	submittedAt := time.Date(2026, 7, 14, 21, 0, 0, 0, time.UTC)
	job := &reportjob.Record{
		JobID:       "job-internal-1",
		ArtifactRef: "report://preset/internal",
		OwnerID:     "owner-1",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		SubmittedAt: submittedAt,
	}
	if err := store.CreateJob(ownerCtx, job); err != nil {
		t.Fatalf("CreateJob(owner) error = %v", err)
	}

	jobs, err := store.ListJobs(internalCtx)
	if err != nil {
		t.Fatalf("ListJobs(internal) error = %v", err)
	}
	foundJob := false
	for _, item := range jobs {
		if item != nil && item.JobID == "job-internal-1" {
			foundJob = true
			break
		}
	}
	if !foundJob {
		t.Fatalf("ListJobs(internal) = %+v, want queued job-internal-1 present", jobs)
	}

	got, err := store.GetJob(internalCtx, "job-internal-1")
	if err != nil {
		t.Fatalf("GetJob(internal) error = %v", err)
	}
	if got == nil || got.OwnerID != "owner-1" {
		t.Fatalf("GetJob(internal) = %+v, want owner-scoped row", got)
	}

	got.Status = "succeeded"
	got.ArtifactID = "artifact-internal-1"
	completedAt := submittedAt.Add(2 * time.Second)
	got.CompletedAt = &completedAt
	if err := store.UpdateJob(internalCtx, got); err != nil {
		t.Fatalf("UpdateJob(internal) error = %v", err)
	}

	updated, err := store.GetJob(ownerCtx, "job-internal-1")
	if err != nil {
		t.Fatalf("GetJob(owner after internal update) error = %v", err)
	}
	if updated == nil || updated.Status != "succeeded" || updated.ArtifactID != "artifact-internal-1" {
		t.Fatalf("GetJob(owner after internal update) = %+v, want succeeded artifact row", updated)
	}

	artifact := &reportartifact.Record{
		ArtifactID:  "artifact-internal-1",
		JobID:       "job-internal-1",
		ArtifactRef: "report://preset/internal",
		OwnerID:     "owner-1",
		Format:      "pdf",
		ContentType: "application/pdf",
		Data:        []byte("%PDF-internal"),
		CreatedAt:   submittedAt.Add(3 * time.Second),
	}
	if err := store.PutArtifact(internalCtx, artifact); err != nil {
		t.Fatalf("PutArtifact(internal) error = %v", err)
	}

	gotArtifact, err := store.GetArtifact(ownerCtx, "artifact-internal-1")
	if err != nil {
		t.Fatalf("GetArtifact(owner) error = %v", err)
	}
	if gotArtifact == nil || string(gotArtifact.Data) != "%PDF-internal" {
		t.Fatalf("GetArtifact(owner) = %+v, want inserted artifact", gotArtifact)
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func isolateReportingComponents(t *testing.T, dao *datly.Service, connectorRef string) {
	t.Helper()
	key := fmt.Sprintf("%d:%s", reflect.ValueOf(dao).Pointer(), normalizeConnectorRef(connectorRef))
	reportingComponentsBy.Delete(key)
	t.Cleanup(func() {
		reportingComponentsBy.Delete(key)
	})
}
