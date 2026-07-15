package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportfs "github.com/viant/agently-core/app/store/reporting/fs"
	authctx "github.com/viant/agently-core/internal/auth"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
	forgereport "github.com/viant/agently-core/pkg/forge/reporting"
	forgereportlist "github.com/viant/agently-core/pkg/forge/reporting/list"
	forgereportwrite "github.com/viant/agently-core/pkg/forge/reporting/write"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
)

const defaultConnectorRef = "agently"

var (
	errNotFound           = errors.New("reporting sql store: not found")
	reportingComponentsBy = sync.Map{}
)

type internalAccessKey struct{}

func WithInternalAccess(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, internalAccessKey{}, true)
}

func hasInternalAccess(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, _ := ctx.Value(internalAccessKey{}).(bool)
	return allowed
}

type Store struct {
	dao          *datly.Service
	connectorRef string
	stateStore   workspace.StateStore
	fallback     reportstore.Client
	mu           sync.RWMutex
	db           *sql.DB
}

func New(ctx context.Context, dao *datly.Service, connectorRef string, stateStore workspace.StateStore, fallback reportstore.Client) (reportstore.Client, error) {
	if dao == nil {
		return nil, errors.New("reporting sql store: datly service is required")
	}
	store := &Store{
		dao:          dao,
		connectorRef: normalizeConnectorRef(connectorRef),
		stateStore:   stateStore,
		fallback:     fallback,
	}
	if err := store.init(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) init(ctx context.Context) error {
	key := fmt.Sprintf("%d:%s", reflect.ValueOf(s.dao).Pointer(), s.connectorRef)
	if _, loaded := reportingComponentsBy.LoadOrStore(key, struct{}{}); loaded {
		return s.ensureSchema(ctx)
	}
	if err := forgereport.DefineSharedArtifactComponent(ctx, s.dao, s.connectorRef); err != nil {
		return err
	}
	if err := forgereportlist.DefineComponent(ctx, s.dao, s.connectorRef); err != nil {
		return err
	}
	if _, err := forgereportwrite.DefineComponent(ctx, s.dao); err != nil {
		return err
	}
	return s.ensureSchema(ctx)
}

func (s *Store) ensureSchema(ctx context.Context) error {
	db, err := s.dbHandle()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS report_shared_artifact (
  artifact_id TEXT PRIMARY KEY,
  artifact_ref TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  owner_ref TEXT,
  kind TEXT NOT NULL,
  lifecycle TEXT NOT NULL,
  version INTEGER NOT NULL DEFAULT 0,
  report_id TEXT,
  title TEXT,
  source_artifact_id TEXT,
  base_artifact_ref TEXT,
  policy_ref TEXT,
  document_version INTEGER NOT NULL DEFAULT 0,
  report_document_json BLOB,
  report_spec_json BLOB,
  compile_state_json BLOB,
  report_fill_json BLOB,
  report_print_json BLOB,
  saved_view_overlay_json BLOB,
  metadata_json BLOB,
  created_at DATETIME NOT NULL,
  updated_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_report_shared_artifact_owner_artifact_ref
  ON report_shared_artifact(owner_id, artifact_ref);
CREATE INDEX IF NOT EXISTS idx_report_shared_artifact_owner_report_id
  ON report_shared_artifact(owner_id, report_id);
CREATE INDEX IF NOT EXISTS idx_report_shared_artifact_owner_kind_lifecycle_updated
  ON report_shared_artifact(owner_id, kind, lifecycle, updated_at, created_at);
CREATE TABLE IF NOT EXISTS report_export_job (
  job_id TEXT PRIMARY KEY,
  artifact_ref TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  conversation_id TEXT,
  workspace_id TEXT,
  auth_context_ref TEXT,
  format TEXT NOT NULL,
  scope TEXT NOT NULL,
  status TEXT NOT NULL,
  report_spec_json BLOB,
  report_fill_json BLOB,
  report_print_json BLOB,
  metadata_json BLOB,
  artifact_id TEXT,
  error_text TEXT,
  diagnostics_json BLOB,
  submitted_at DATETIME NOT NULL,
  started_at DATETIME,
  completed_at DATETIME,
  retention_ttl_sec INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_report_export_job_owner_submitted_at
  ON report_export_job(owner_id, submitted_at);
CREATE INDEX IF NOT EXISTS idx_report_export_job_owner_artifact_ref
  ON report_export_job(owner_id, artifact_ref);
CREATE INDEX IF NOT EXISTS idx_report_export_job_owner_status
  ON report_export_job(owner_id, status);
CREATE TABLE IF NOT EXISTS report_export_artifact (
  artifact_id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  artifact_ref TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  format TEXT NOT NULL,
  content_type TEXT NOT NULL,
  inline_data BLOB,
  created_at DATETIME NOT NULL,
  retention_ttl_sec INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_report_export_artifact_owner_created_at
  ON report_export_artifact(owner_id, created_at);
CREATE INDEX IF NOT EXISTS idx_report_export_artifact_owner_artifact_ref
  ON report_export_artifact(owner_id, artifact_ref);
CREATE INDEX IF NOT EXISTS idx_report_export_artifact_job_id
  ON report_export_artifact(job_id);
CREATE TABLE IF NOT EXISTS report_audit_event (
  event_id TEXT PRIMARY KEY,
  event_type TEXT NOT NULL,
  artifact_ref TEXT NOT NULL,
  version INTEGER NOT NULL DEFAULT 0,
  job_id TEXT,
  artifact_id TEXT,
  actor_id TEXT NOT NULL,
  actor_ref TEXT,
  occurred_at DATETIME NOT NULL,
  metadata_json BLOB
);
CREATE INDEX IF NOT EXISTS idx_report_audit_event_actor_occurred_at
  ON report_audit_event(actor_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_report_audit_event_artifact_ref
  ON report_audit_event(artifact_ref);
`)
	if err != nil {
		return fmt.Errorf("reporting sql store: ensure schema: %w", err)
	}
	return s.importFilesystemState(ctx)
}

func (s *Store) dbHandle() (*sql.DB, error) {
	if s == nil || s.dao == nil {
		return nil, errors.New("reporting sql store: datly service is required")
	}
	s.mu.RLock()
	if s.db != nil {
		db := s.db
		s.mu.RUnlock()
		return db, nil
	}
	s.mu.RUnlock()
	conn, err := s.dao.Resource().Connector(s.connectorRef)
	if err != nil {
		return nil, err
	}
	db, err := conn.DB()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.db == nil {
		s.db = db
	}
	cached := s.db
	s.mu.Unlock()
	return cached, nil
}

func (s *Store) CreateJob(ctx context.Context, job *reportjob.Record) error {
	if job == nil {
		return errNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	if !hasInternalAccess(ctx) && (ownerID == "" || ownerID != strings.TrimSpace(job.OwnerID)) {
		return errNotFound
	}
	if ownerID == "" {
		ownerID = strings.TrimSpace(job.OwnerID)
	}
	if existing, err := s.GetJob(ctx, job.JobID); err == nil && existing != nil {
		return fmt.Errorf("reporting sql store: job %s already exists: %w", strings.TrimSpace(job.JobID), reportstore.ErrAlreadyExists)
	} else if err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	db, err := s.dbHandle()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO report_export_job (
  job_id, artifact_ref, owner_id, conversation_id, workspace_id, auth_context_ref, format, scope, status,
  report_spec_json, report_fill_json, report_print_json, metadata_json, artifact_id, error_text, diagnostics_json,
  submitted_at, started_at, completed_at, retention_ttl_sec
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(job.JobID),
		strings.TrimSpace(job.ArtifactRef),
		ownerID,
		nullIfEmpty(job.ConversationID),
		nullIfEmpty(job.WorkspaceID),
		nullIfEmpty(job.AuthContextRef),
		strings.TrimSpace(job.Format),
		strings.TrimSpace(job.Scope),
		strings.TrimSpace(job.Status),
		clonedBytes(job.ReportSpec),
		clonedBytes(job.ReportFill),
		clonedBytes(job.ReportPrint),
		clonedBytes(job.Metadata),
		nullIfEmpty(job.ArtifactID),
		nullIfEmpty(job.Error),
		clonedBytes(job.Diagnostics),
		job.SubmittedAt.UTC(),
		nullableTime(job.StartedAt),
		nullableTime(job.CompletedAt),
		durationSeconds(job.RetentionTTL),
	)
	return wrapDuplicateErr(err, "job", strings.TrimSpace(job.JobID))
}

func (s *Store) GetJob(ctx context.Context, jobID string) (*reportjob.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	internal := hasInternalAccess(ctx)
	if ownerID == "" && !internal {
		return nil, errNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, err
	}
	query := `
SELECT job_id, artifact_ref, owner_id, conversation_id, workspace_id, auth_context_ref, format, scope, status,
       report_spec_json, report_fill_json, report_print_json, metadata_json, artifact_id, error_text, diagnostics_json,
       submitted_at, started_at, completed_at, retention_ttl_sec
FROM report_export_job
WHERE job_id = ?`
	args := []interface{}{strings.TrimSpace(jobID)}
	if !internal {
		query += ` AND owner_id = ?`
		args = append(args, ownerID)
	}
	query += `
LIMIT 1`
	row := db.QueryRowContext(ctx, query, args...)
	record, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return record, err
}

func (s *Store) ListJobs(ctx context.Context) ([]*reportjob.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	internal := hasInternalAccess(ctx)
	if ownerID == "" && !internal {
		return []*reportjob.Record{}, nil
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, err
	}
	query := `
SELECT job_id, artifact_ref, owner_id, conversation_id, workspace_id, auth_context_ref, format, scope, status,
       report_spec_json, report_fill_json, report_print_json, metadata_json, artifact_id, error_text, diagnostics_json,
       submitted_at, started_at, completed_at, retention_ttl_sec
FROM report_export_job`
	args := []interface{}{}
	if !internal {
		query += `
WHERE owner_id = ?`
		args = append(args, ownerID)
	}
	query += `
ORDER BY submitted_at DESC, job_id DESC`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (s *Store) UpdateJob(ctx context.Context, job *reportjob.Record) error {
	if job == nil {
		return errNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	internal := hasInternalAccess(ctx)
	if !internal && (ownerID == "" || ownerID != strings.TrimSpace(job.OwnerID)) {
		return errNotFound
	}
	if ownerID == "" {
		ownerID = strings.TrimSpace(job.OwnerID)
	}
	db, err := s.dbHandle()
	if err != nil {
		return err
	}
	query := `
UPDATE report_export_job
SET artifact_ref = ?, conversation_id = ?, workspace_id = ?, auth_context_ref = ?, format = ?, scope = ?, status = ?,
    report_spec_json = ?, report_fill_json = ?, report_print_json = ?, metadata_json = ?, artifact_id = ?, error_text = ?,
    diagnostics_json = ?, submitted_at = ?, started_at = ?, completed_at = ?, retention_ttl_sec = ?
WHERE job_id = ?`
	args := []interface{}{
		strings.TrimSpace(job.ArtifactRef),
		nullIfEmpty(job.ConversationID),
		nullIfEmpty(job.WorkspaceID),
		nullIfEmpty(job.AuthContextRef),
		strings.TrimSpace(job.Format),
		strings.TrimSpace(job.Scope),
		strings.TrimSpace(job.Status),
		clonedBytes(job.ReportSpec),
		clonedBytes(job.ReportFill),
		clonedBytes(job.ReportPrint),
		clonedBytes(job.Metadata),
		nullIfEmpty(job.ArtifactID),
		nullIfEmpty(job.Error),
		clonedBytes(job.Diagnostics),
		job.SubmittedAt.UTC(),
		nullableTime(job.StartedAt),
		nullableTime(job.CompletedAt),
		durationSeconds(job.RetentionTTL),
		strings.TrimSpace(job.JobID),
	}
	if !internal {
		query += ` AND owner_id = ?`
		args = append(args, ownerID)
	}
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	return requireRowsAffected(result)
}

func (s *Store) PutArtifact(ctx context.Context, artifact *reportartifact.Record) error {
	if artifact == nil {
		return errNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	if !hasInternalAccess(ctx) && (ownerID == "" || ownerID != strings.TrimSpace(artifact.OwnerID)) {
		return errNotFound
	}
	if ownerID == "" {
		ownerID = strings.TrimSpace(artifact.OwnerID)
	}
	db, err := s.dbHandle()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO report_export_artifact (
  artifact_id, job_id, artifact_ref, owner_id, format, content_type, inline_data, created_at, retention_ttl_sec
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(artifact.ArtifactID),
		strings.TrimSpace(artifact.JobID),
		strings.TrimSpace(artifact.ArtifactRef),
		ownerID,
		strings.TrimSpace(artifact.Format),
		strings.TrimSpace(artifact.ContentType),
		append([]byte{}, artifact.Data...),
		artifact.CreatedAt.UTC(),
		durationSeconds(artifact.RetentionTTL),
	)
	return wrapDuplicateErr(err, "artifact", strings.TrimSpace(artifact.ArtifactID))
}

func (s *Store) GetArtifact(ctx context.Context, artifactID string) (*reportartifact.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	internal := hasInternalAccess(ctx)
	if ownerID == "" && !internal {
		return nil, errNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, err
	}
	query := `
SELECT artifact_id, job_id, artifact_ref, owner_id, format, content_type, inline_data, created_at, retention_ttl_sec
FROM report_export_artifact
WHERE artifact_id = ?`
	args := []interface{}{strings.TrimSpace(artifactID)}
	if !internal {
		query += ` AND owner_id = ?`
		args = append(args, ownerID)
	}
	query += `
LIMIT 1`
	row := db.QueryRowContext(ctx, query, args...)
	record, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return record, err
}

func (s *Store) ListArtifacts(ctx context.Context) ([]*reportartifact.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	internal := hasInternalAccess(ctx)
	if ownerID == "" && !internal {
		return []*reportartifact.Record{}, nil
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, err
	}
	query := `
SELECT artifact_id, job_id, artifact_ref, owner_id, format, content_type, inline_data, created_at, retention_ttl_sec
FROM report_export_artifact`
	args := []interface{}{}
	if !internal {
		query += `
WHERE owner_id = ?`
		args = append(args, ownerID)
	}
	query += `
ORDER BY created_at DESC, artifact_id DESC`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifacts(rows)
}

func (s *Store) CreateSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error {
	if artifact == nil {
		return errNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" || ownerID != strings.TrimSpace(artifact.OwnerID) {
		return errNotFound
	}
	existing, err := s.GetSharedArtifact(ctx, artifact.ArtifactID)
	switch {
	case err == nil && existing != nil:
		return fmt.Errorf("reporting sql store: shared artifact %s already exists: %w", strings.TrimSpace(artifact.ArtifactID), reportstore.ErrAlreadyExists)
	case err != nil && !errors.Is(err, errNotFound):
		return err
	}
	return s.upsertSharedArtifact(ctx, artifact)
}

func (s *Store) GetSharedArtifact(ctx context.Context, artifactID string) (*reportshareartifact.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" {
		return nil, errNotFound
	}
	id := strings.TrimSpace(artifactID)
	if id == "" {
		return nil, errNotFound
	}
	in := &forgereport.SharedArtifactInput{
		ArtifactID: id,
		OwnerID:    ownerID,
		Has: &forgereport.SharedArtifactInputHas{
			ArtifactID: true,
			OwnerID:    true,
		},
	}
	out := &forgereport.SharedArtifactOutput{}
	uri := strings.ReplaceAll(forgereport.SharedArtifactPathURI, "{artifactId}", id)
	if _, err := s.dao.Operate(ctx, datly.WithURI(uri), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 || out.Data[0] == nil {
		return nil, errNotFound
	}
	return toSharedArtifactRecord(out.Data[0]), nil
}

func (s *Store) ListSharedArtifacts(ctx context.Context) ([]*reportshareartifact.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" {
		return []*reportshareartifact.Record{}, nil
	}
	in := &forgereportlist.Input{
		OwnerID: ownerID,
		Has: &forgereportlist.InputHas{
			OwnerID: true,
		},
	}
	out := &forgereportlist.Output{}
	if _, err := s.dao.Operate(ctx, datly.WithURI(forgereportlist.PathURI), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return nil, err
	}
	result := make([]*reportshareartifact.Record, 0, len(out.Data))
	for _, artifact := range out.Data {
		if artifact == nil {
			continue
		}
		result = append(result, toSharedArtifactRecord(artifact))
	}
	return result, nil
}

func (s *Store) UpdateSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error {
	if artifact == nil {
		return errNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" || ownerID != strings.TrimSpace(artifact.OwnerID) {
		return errNotFound
	}
	current, err := s.GetSharedArtifact(ctx, artifact.ArtifactID)
	if err != nil {
		return err
	}
	if current == nil || strings.TrimSpace(current.OwnerID) != ownerID {
		return errNotFound
	}
	return s.upsertSharedArtifact(ctx, artifact)
}

func (s *Store) upsertSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error {
	in := &forgereportwrite.Input{Artifact: toForgeSharedArtifact(artifact)}
	out := &forgereportwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("PATCH", forgereportwrite.PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	)
	return err
}

func (s *Store) importFilesystemState(ctx context.Context) error {
	if s.fallback == nil {
		return nil
	}
	db, err := s.dbHandle()
	if err != nil {
		return err
	}
	if err := s.importSharedArtifacts(ctx, db); err != nil {
		return err
	}
	if err := s.importJobs(ctx, db); err != nil {
		return err
	}
	if err := s.importArtifacts(ctx, db); err != nil {
		return err
	}
	if err := s.importAudits(ctx, db); err != nil {
		return err
	}
	return nil
}

func (s *Store) importSharedArtifacts(ctx context.Context, db *sql.DB) error {
	items, err := s.fallback.ListSharedArtifacts(ctx)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		exists, err := rowExists(ctx, db, `SELECT COUNT(1) FROM report_shared_artifact WHERE artifact_id = ?`, item.ArtifactID)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		_, err = db.ExecContext(ctx, `
INSERT INTO report_shared_artifact (
  artifact_id, artifact_ref, owner_id, owner_ref, kind, lifecycle, version, report_id, title, source_artifact_id,
  base_artifact_ref, policy_ref, document_version, report_document_json, report_spec_json, compile_state_json,
  report_fill_json, report_print_json, saved_view_overlay_json, metadata_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ArtifactID, item.ArtifactRef, item.OwnerID, item.OwnerRef, item.Kind, item.Lifecycle, item.Version, item.ReportID, item.Title,
			strings.TrimSpace(item.SourceArtifactID), strings.TrimSpace(item.BaseArtifactRef), strings.TrimSpace(item.PolicyRef), item.DocumentVersion,
			clonedBytes(item.Document), clonedBytes(item.ReportSpec), clonedBytes(item.CompileState), clonedBytes(item.ReportFill),
			clonedBytes(item.ReportPrint), clonedBytes(item.SavedViewOverlay), clonedBytes(item.Metadata), item.CreatedAt.UTC(), nullableTime(item.UpdatedAt),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) importJobs(ctx context.Context, db *sql.DB) error {
	items, err := s.fallback.ListJobs(ctx)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		exists, err := rowExists(ctx, db, `SELECT COUNT(1) FROM report_export_job WHERE job_id = ?`, item.JobID)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		_, err = db.ExecContext(ctx, `
INSERT INTO report_export_job (
  job_id, artifact_ref, owner_id, conversation_id, workspace_id, auth_context_ref, format, scope, status,
  report_spec_json, report_fill_json, report_print_json, metadata_json, artifact_id, error_text, diagnostics_json,
  submitted_at, started_at, completed_at, retention_ttl_sec
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.JobID, item.ArtifactRef, item.OwnerID, nullIfEmpty(item.ConversationID), nullIfEmpty(item.WorkspaceID),
			nullIfEmpty(item.AuthContextRef), item.Format, item.Scope, item.Status, clonedBytes(item.ReportSpec), clonedBytes(item.ReportFill),
			clonedBytes(item.ReportPrint), clonedBytes(item.Metadata), nullIfEmpty(item.ArtifactID), nullIfEmpty(item.Error),
			clonedBytes(item.Diagnostics), item.SubmittedAt.UTC(), nullableTime(item.StartedAt), nullableTime(item.CompletedAt),
			durationSeconds(item.RetentionTTL),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) importArtifacts(ctx context.Context, db *sql.DB) error {
	items, err := s.fallback.ListArtifacts(ctx)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		exists, err := rowExists(ctx, db, `SELECT COUNT(1) FROM report_export_artifact WHERE artifact_id = ?`, item.ArtifactID)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		_, err = db.ExecContext(ctx, `
INSERT INTO report_export_artifact (
  artifact_id, job_id, artifact_ref, owner_id, format, content_type, inline_data, created_at, retention_ttl_sec
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ArtifactID, item.JobID, item.ArtifactRef, item.OwnerID, item.Format, item.ContentType, append([]byte{}, item.Data...),
			item.CreatedAt.UTC(), durationSeconds(item.RetentionTTL),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) importAudits(ctx context.Context, db *sql.DB) error {
	if s.stateStore == nil {
		return nil
	}
	items, err := reportfs.ListAuditEvents(ctx, s.stateStore)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		metadata, err := json.Marshal(item.Metadata)
		if err != nil {
			return err
		}
		eventID := fmt.Sprintf("%s_%s_%s_%s", item.EventType, item.ArtifactID, item.JobID, item.ActorID)
		if strings.Trim(eventID, "_") == "" {
			continue
		}
		exists, err := rowExists(ctx, db, `SELECT COUNT(1) FROM report_audit_event WHERE event_id = ?`, eventID)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		_, err = db.ExecContext(ctx, `
INSERT INTO report_audit_event (
  event_id, event_type, artifact_ref, version, job_id, artifact_id, actor_id, actor_ref, occurred_at, metadata_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			eventID, item.EventType, item.ArtifactRef, item.Version, nullIfEmpty(item.JobID), nullIfEmpty(item.ArtifactID),
			item.ActorID, nullIfEmpty(item.ActorRef), item.OccurredAt.UTC(), metadata,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func normalizeConnectorRef(connectorRef string) string {
	if strings.TrimSpace(connectorRef) == "" {
		return defaultConnectorRef
	}
	return strings.TrimSpace(connectorRef)
}

func effectiveOwnerID(ctx context.Context) string {
	return strings.TrimSpace(authctx.EffectiveUserID(ctx))
}

func toForgeSharedArtifact(input *reportshareartifact.Record) *forgereport.SharedArtifact {
	if input == nil {
		return nil
	}
	out := &forgereport.SharedArtifact{
		ArtifactID:       strings.TrimSpace(input.ArtifactID),
		ArtifactRef:      strings.TrimSpace(input.ArtifactRef),
		OwnerID:          strings.TrimSpace(input.OwnerID),
		OwnerRef:         strings.TrimSpace(input.OwnerRef),
		Kind:             strings.TrimSpace(input.Kind),
		Lifecycle:        strings.TrimSpace(input.Lifecycle),
		Version:          input.Version,
		ReportID:         strings.TrimSpace(input.ReportID),
		Title:            strings.TrimSpace(input.Title),
		SourceArtifactID: strings.TrimSpace(input.SourceArtifactID),
		BaseArtifactRef:  strings.TrimSpace(input.BaseArtifactRef),
		PolicyRef:        strings.TrimSpace(input.PolicyRef),
		DocumentVersion:  input.DocumentVersion,
		Document:         append([]byte{}, input.Document...),
		ReportSpec:       append([]byte{}, input.ReportSpec...),
		CompileState:     append([]byte{}, input.CompileState...),
		ReportFill:       append([]byte{}, input.ReportFill...),
		ReportPrint:      append([]byte{}, input.ReportPrint...),
		SavedViewOverlay: append([]byte{}, input.SavedViewOverlay...),
		Metadata:         append([]byte{}, input.Metadata...),
		CreatedAt:        input.CreatedAt,
	}
	if input.UpdatedAt != nil {
		updatedAt := *input.UpdatedAt
		out.UpdatedAt = &updatedAt
	}
	return out
}

func toSharedArtifactRecord(input *forgereport.SharedArtifact) *reportshareartifact.Record {
	if input == nil {
		return nil
	}
	out := &reportshareartifact.Record{
		ArtifactID:       strings.TrimSpace(input.ArtifactID),
		ArtifactRef:      strings.TrimSpace(input.ArtifactRef),
		OwnerID:          strings.TrimSpace(input.OwnerID),
		OwnerRef:         strings.TrimSpace(input.OwnerRef),
		Kind:             strings.TrimSpace(input.Kind),
		Lifecycle:        strings.TrimSpace(input.Lifecycle),
		Version:          input.Version,
		ReportID:         strings.TrimSpace(input.ReportID),
		Title:            strings.TrimSpace(input.Title),
		SourceArtifactID: strings.TrimSpace(input.SourceArtifactID),
		BaseArtifactRef:  strings.TrimSpace(input.BaseArtifactRef),
		PolicyRef:        strings.TrimSpace(input.PolicyRef),
		DocumentVersion:  input.DocumentVersion,
		Document:         append([]byte{}, input.Document...),
		ReportSpec:       append([]byte{}, input.ReportSpec...),
		CompileState:     append([]byte{}, input.CompileState...),
		ReportFill:       append([]byte{}, input.ReportFill...),
		ReportPrint:      append([]byte{}, input.ReportPrint...),
		SavedViewOverlay: append([]byte{}, input.SavedViewOverlay...),
		Metadata:         append([]byte{}, input.Metadata...),
		CreatedAt:        input.CreatedAt,
	}
	if input.UpdatedAt != nil {
		updatedAt := *input.UpdatedAt
		out.UpdatedAt = &updatedAt
	}
	return out
}

func scanJob(scanner interface {
	Scan(dest ...interface{}) error
}) (*reportjob.Record, error) {
	record := &reportjob.Record{}
	var (
		conversationID sql.NullString
		workspaceID    sql.NullString
		authContextRef sql.NullString
		artifactID     sql.NullString
		errorText      sql.NullString
		reportSpec     []byte
		reportFill     []byte
		reportPrint    []byte
		metadata       []byte
		diagnostics    []byte
		startedAt      sql.NullTime
		completedAt    sql.NullTime
		retentionSec   int64
	)
	err := scanner.Scan(&record.JobID, &record.ArtifactRef, &record.OwnerID, &conversationID, &workspaceID, &authContextRef,
		&record.Format, &record.Scope, &record.Status, &reportSpec, &reportFill, &reportPrint, &metadata, &artifactID,
		&errorText, &diagnostics, &record.SubmittedAt, &startedAt, &completedAt, &retentionSec)
	if err != nil {
		return nil, err
	}
	record.ConversationID = conversationID.String
	record.WorkspaceID = workspaceID.String
	record.AuthContextRef = authContextRef.String
	record.ReportSpec = reportSpec
	record.ReportFill = reportFill
	record.ReportPrint = reportPrint
	record.Metadata = metadata
	record.ArtifactID = artifactID.String
	record.Error = errorText.String
	record.Diagnostics = diagnostics
	if startedAt.Valid {
		value := startedAt.Time
		record.StartedAt = &value
	}
	if completedAt.Valid {
		value := completedAt.Time
		record.CompletedAt = &value
	}
	record.RetentionTTL = time.Duration(retentionSec) * time.Second
	return record, nil
}

func scanJobs(rows *sql.Rows) ([]*reportjob.Record, error) {
	result := []*reportjob.Record{}
	for rows.Next() {
		record, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func scanArtifact(scanner interface {
	Scan(dest ...interface{}) error
}) (*reportartifact.Record, error) {
	record := &reportartifact.Record{}
	var data []byte
	var retentionSec int64
	err := scanner.Scan(&record.ArtifactID, &record.JobID, &record.ArtifactRef, &record.OwnerID, &record.Format, &record.ContentType, &data, &record.CreatedAt, &retentionSec)
	if err != nil {
		return nil, err
	}
	record.Data = data
	record.RetentionTTL = time.Duration(retentionSec) * time.Second
	return record, nil
}

func scanArtifacts(rows *sql.Rows) ([]*reportartifact.Record, error) {
	result := []*reportartifact.Record{}
	for rows.Next() {
		record, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func nullableTime(value *time.Time) interface{} {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullIfEmpty(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func durationSeconds(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value / time.Second)
}

func clonedBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	return append([]byte{}, value...)
}

func requireRowsAffected(result sql.Result) error {
	if result == nil {
		return errNotFound
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return errNotFound
	}
	return nil
}

func wrapDuplicateErr(err error, kind, id string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		return fmt.Errorf("reporting sql store: %s %s already exists: %w", kind, id, reportstore.ErrAlreadyExists)
	}
	return err
}

func rowExists(ctx context.Context, db *sql.DB, query string, args ...interface{}) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}
