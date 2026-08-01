package sql

import (
	"bytes"
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
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
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
		return s.importFilesystemState(ctx)
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

// CreateReportRun persists a new owner-scoped browser materialization.
func (s *Store) CreateReportRun(ctx context.Context, run *reportrun.Record) error {
	if run == nil {
		return reportstore.ErrNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" || ownerID != strings.TrimSpace(run.OwnerID) {
		return reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO report_run (
  report_run_id, owner_id, conversation_id, materializer, origin, builder_ref, preset_id, source_kind, source_id,
  requested_params_json, effective_params_json, status, failure_code, failure_text, started_at, completed_at,
  revision, ui_run_request_id, report_spec_json, report_fill_json, report_print_json, activation_source,
  adoption_source, actor_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(run.ReportRunID), ownerID, nullIfEmpty(run.ConversationID), strings.TrimSpace(run.Materializer),
		nullIfEmpty(run.Origin), nullIfEmpty(run.BuilderRef), nullIfEmpty(run.PresetID), nullIfEmpty(run.SourceKind),
		nullIfEmpty(run.SourceID), clonedBytes(run.RequestedParams), clonedBytes(run.EffectiveParams), strings.TrimSpace(run.Status),
		nullIfEmpty(run.FailureCode), nullIfEmpty(run.FailureText), run.StartedAt.UTC(), nullableTime(run.CompletedAt),
		run.Revision, strings.TrimSpace(run.UIRunRequestID), clonedBytes(run.ReportSpec), clonedBytes(run.ReportFill),
		clonedBytes(run.ReportPrint), nullIfEmpty(run.ActivationSource), nullIfEmpty(run.AdoptionSource),
		nullIfEmpty(run.ActorID), run.CreatedAt.UTC(), run.UpdatedAt.UTC(),
	)
	return wrapDuplicateErr(err, "run", strings.TrimSpace(run.ReportRunID))
}

// GetReportRun loads a browser run in the authenticated owner scope.
func (s *Store) GetReportRun(ctx context.Context, reportRunID string) (*reportrun.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" {
		return nil, reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, err
	}
	record, err := scanReportRun(db.QueryRowContext(ctx, reportRunSelect+`
WHERE report_run_id = ? AND owner_id = ?
LIMIT 1`, strings.TrimSpace(reportRunID), ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, reportstore.ErrNotFound
	}
	return record, err
}

// GetReportRunByRequestID resolves a transport retry in owner scope.
func (s *Store) GetReportRunByRequestID(ctx context.Context, uiRunRequestID string) (*reportrun.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" {
		return nil, reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, err
	}
	record, err := scanReportRun(db.QueryRowContext(ctx, reportRunSelect+`
WHERE owner_id = ? AND ui_run_request_id = ?
LIMIT 1`, ownerID, strings.TrimSpace(uiRunRequestID)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, reportstore.ErrNotFound
	}
	return record, err
}

// UpdateReportRunCAS replaces a run only when its owner and revision match.
func (s *Store) UpdateReportRunCAS(ctx context.Context, run *reportrun.Record, expectedRevision int64) error {
	if run == nil {
		return reportstore.ErrNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" || ownerID != strings.TrimSpace(run.OwnerID) {
		return reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return err
	}
	current, err := s.GetReportRun(ctx, run.ReportRunID)
	if err != nil {
		return err
	}
	if err := reportstore.ValidateReportRunUpdate(current, run, expectedRevision); err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `
UPDATE report_run
SET conversation_id = ?, materializer = ?, origin = ?, builder_ref = ?, preset_id = ?, source_kind = ?, source_id = ?,
    requested_params_json = ?, effective_params_json = ?, status = ?, failure_code = ?, failure_text = ?,
    started_at = ?, completed_at = ?, revision = ?, report_spec_json = ?, report_fill_json = ?, report_print_json = ?,
    activation_source = ?, adoption_source = ?, actor_id = ?, updated_at = ?
WHERE report_run_id = ? AND owner_id = ? AND revision = ? AND ui_run_request_id = ?`,
		nullIfEmpty(run.ConversationID), strings.TrimSpace(run.Materializer), nullIfEmpty(run.Origin), nullIfEmpty(run.BuilderRef),
		nullIfEmpty(run.PresetID), nullIfEmpty(run.SourceKind), nullIfEmpty(run.SourceID), clonedBytes(run.RequestedParams),
		clonedBytes(run.EffectiveParams), strings.TrimSpace(run.Status), nullIfEmpty(run.FailureCode), nullIfEmpty(run.FailureText),
		run.StartedAt.UTC(), nullableTime(run.CompletedAt), run.Revision, clonedBytes(run.ReportSpec), clonedBytes(run.ReportFill),
		clonedBytes(run.ReportPrint), nullIfEmpty(run.ActivationSource), nullIfEmpty(run.AdoptionSource), nullIfEmpty(run.ActorID),
		run.UpdatedAt.UTC(), strings.TrimSpace(run.ReportRunID), ownerID, expectedRevision, strings.TrimSpace(run.UIRunRequestID),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		if _, getErr := s.GetReportRun(ctx, run.ReportRunID); errors.Is(getErr, reportstore.ErrNotFound) {
			return reportstore.ErrNotFound
		}
		return reportstore.ErrCASMismatch
	}
	return nil
}

// AdoptReportRunAndContextCAS binds the completed manual run and advances the
// conversation pointer in one SQL transaction.
func (s *Store) AdoptReportRunAndContextCAS(ctx context.Context, run *reportrun.Record, expectedRunRevision int64, record *reportcontext.Record, expectedContextRevision int64) error {
	if run == nil || record == nil {
		return reportstore.ErrNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" || ownerID != strings.TrimSpace(run.OwnerID) || ownerID != strings.TrimSpace(record.OwnerID) {
		return reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	current, err := scanReportRun(tx.QueryRowContext(ctx, reportRunSelect+`
WHERE report_run_id = ? AND owner_id = ?
LIMIT 1`, strings.TrimSpace(run.ReportRunID), ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return reportstore.ErrNotFound
	}
	if err != nil {
		return err
	}
	if current.Revision != expectedRunRevision {
		return reportstore.ErrCASMismatch
	}
	currentContext := &reportcontext.Record{}
	contextErr := tx.QueryRowContext(ctx, `
SELECT owner_id, conversation_id, active_report_run_id, revision, activation_source, actor_id, updated_at
FROM conversation_report_context
WHERE owner_id = ? AND conversation_id = ?
LIMIT 1`, ownerID, strings.TrimSpace(record.ConversationID)).Scan(
		&currentContext.OwnerID, &currentContext.ConversationID, &currentContext.ActiveReportRunID, &currentContext.Revision,
		&currentContext.ActivationSource, &currentContext.ActorID, &currentContext.UpdatedAt,
	)
	switch {
	case errors.Is(contextErr, sql.ErrNoRows) && expectedContextRevision != 0:
		return reportstore.ErrCASMismatch
	case contextErr != nil && !errors.Is(contextErr, sql.ErrNoRows):
		return contextErr
	case contextErr == nil && currentContext.Revision != expectedContextRevision:
		return reportstore.ErrCASMismatch
	}
	if err := reportstore.ValidateAdoptionMutation(current, run, expectedRunRevision, record, expectedContextRevision); err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, `
UPDATE report_run
SET conversation_id = ?, adoption_source = ?, actor_id = ?, revision = ?, updated_at = ?
WHERE report_run_id = ? AND owner_id = ? AND revision = ? AND conversation_id IS NULL
  AND status = ? AND origin = ?`,
		strings.TrimSpace(run.ConversationID), strings.TrimSpace(run.AdoptionSource), strings.TrimSpace(run.ActorID),
		run.Revision, run.UpdatedAt.UTC(), strings.TrimSpace(run.ReportRunID), ownerID, expectedRunRevision,
		reportrun.StatusCompleted, "manual",
	)
	if err != nil {
		return adoptionSQLError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return reportstore.ErrCASMismatch
	}
	if errors.Is(contextErr, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
INSERT INTO conversation_report_context (
  owner_id, conversation_id, active_report_run_id, revision, activation_source, actor_id, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ownerID, strings.TrimSpace(record.ConversationID), strings.TrimSpace(record.ActiveReportRunID),
			record.Revision, strings.TrimSpace(record.ActivationSource), strings.TrimSpace(record.ActorID), record.UpdatedAt.UTC(),
		)
	} else {
		result, err = tx.ExecContext(ctx, `
UPDATE conversation_report_context
SET active_report_run_id = ?, revision = ?, activation_source = ?, actor_id = ?, updated_at = ?
WHERE owner_id = ? AND conversation_id = ? AND revision = ?`,
			strings.TrimSpace(record.ActiveReportRunID), record.Revision, strings.TrimSpace(record.ActivationSource),
			strings.TrimSpace(record.ActorID), record.UpdatedAt.UTC(), ownerID, strings.TrimSpace(record.ConversationID),
			expectedContextRevision,
		)
		if err == nil {
			affected, err = result.RowsAffected()
			if err == nil && affected != 1 {
				return reportstore.ErrCASMismatch
			}
		}
	}
	if err != nil {
		return adoptionSQLError(err)
	}
	if err := tx.Commit(); err != nil {
		return adoptionSQLError(err)
	}
	return nil
}

func adoptionSQLError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unique") || strings.Contains(message, "duplicate") ||
		strings.Contains(message, "locked") || strings.Contains(message, "busy") {
		return reportstore.ErrCASMismatch
	}
	return err
}

// GetConversationReportContext loads the active run pointer for an exact
// owner+conversation pair.
func (s *Store) GetConversationReportContext(ctx context.Context, conversationID string) (*reportcontext.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" || strings.TrimSpace(conversationID) == "" {
		return nil, reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, err
	}
	record := &reportcontext.Record{}
	err = db.QueryRowContext(ctx, `
SELECT owner_id, conversation_id, active_report_run_id, revision, activation_source, actor_id, updated_at
FROM conversation_report_context
WHERE owner_id = ? AND conversation_id = ?
LIMIT 1`, ownerID, strings.TrimSpace(conversationID)).Scan(
		&record.OwnerID, &record.ConversationID, &record.ActiveReportRunID, &record.Revision,
		&record.ActivationSource, &record.ActorID, &record.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, reportstore.ErrNotFound
	}
	return record, err
}

// PutConversationReportContextCAS creates revision one from expected zero or
// updates an exact existing revision.
func (s *Store) PutConversationReportContextCAS(ctx context.Context, record *reportcontext.Record, expectedRevision int64) error {
	if record == nil {
		return reportstore.ErrNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" || ownerID != strings.TrimSpace(record.OwnerID) || strings.TrimSpace(record.ConversationID) == "" {
		return reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return err
	}
	if expectedRevision == 0 {
		_, err = db.ExecContext(ctx, `
INSERT INTO conversation_report_context (
  owner_id, conversation_id, active_report_run_id, revision, activation_source, actor_id, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ownerID, strings.TrimSpace(record.ConversationID), strings.TrimSpace(record.ActiveReportRunID),
			record.Revision, strings.TrimSpace(record.ActivationSource), strings.TrimSpace(record.ActorID), record.UpdatedAt.UTC(),
		)
		if err == nil {
			return nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return reportstore.ErrCASMismatch
		}
		return err
	}
	result, err := db.ExecContext(ctx, `
UPDATE conversation_report_context
SET active_report_run_id = ?, revision = ?, activation_source = ?, actor_id = ?, updated_at = ?
WHERE owner_id = ? AND conversation_id = ? AND revision = ?`,
		strings.TrimSpace(record.ActiveReportRunID), record.Revision, strings.TrimSpace(record.ActivationSource),
		strings.TrimSpace(record.ActorID), record.UpdatedAt.UTC(), ownerID, strings.TrimSpace(record.ConversationID), expectedRevision,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		if _, getErr := s.GetConversationReportContext(ctx, record.ConversationID); errors.Is(getErr, reportstore.ErrNotFound) {
			return reportstore.ErrNotFound
		}
		return reportstore.ErrCASMismatch
	}
	return nil
}

const reportRunSelect = `
SELECT report_run_id, owner_id, conversation_id, materializer, origin, builder_ref, preset_id, source_kind, source_id,
       requested_params_json, effective_params_json, status, failure_code, failure_text, started_at, completed_at,
       revision, ui_run_request_id, report_spec_json, report_fill_json, report_print_json, activation_source,
       adoption_source, actor_id, created_at, updated_at
FROM report_run
`

func scanReportRun(scanner interface {
	Scan(dest ...interface{}) error
}) (*reportrun.Record, error) {
	record := &reportrun.Record{}
	var conversationID, origin, builderRef, presetID, sourceKind, sourceID sql.NullString
	var failureCode, failureText, activationSource, adoptionSource, actorID sql.NullString
	var completedAt sql.NullTime
	var requestedParams, effectiveParams, reportSpec, reportFill, reportPrint []byte
	err := scanner.Scan(
		&record.ReportRunID, &record.OwnerID, &conversationID, &record.Materializer, &origin, &builderRef, &presetID,
		&sourceKind, &sourceID, &requestedParams, &effectiveParams, &record.Status, &failureCode,
		&failureText, &record.StartedAt, &completedAt, &record.Revision, &record.UIRunRequestID, &reportSpec,
		&reportFill, &reportPrint, &activationSource, &adoptionSource, &actorID, &record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	record.ConversationID = conversationID.String
	record.Origin = origin.String
	record.BuilderRef = builderRef.String
	record.PresetID = presetID.String
	record.SourceKind = sourceKind.String
	record.SourceID = sourceID.String
	record.FailureCode = failureCode.String
	record.FailureText = failureText.String
	record.RequestedParams = requestedParams
	record.EffectiveParams = effectiveParams
	record.ReportSpec = reportSpec
	record.ReportFill = reportFill
	record.ReportPrint = reportPrint
	record.ActivationSource = activationSource.String
	record.AdoptionSource = adoptionSource.String
	record.ActorID = actorID.String
	if completedAt.Valid {
		value := completedAt.Time
		record.CompletedAt = &value
	}
	return record, nil
}

func (s *Store) CreateJob(ctx context.Context, job *reportjob.Record) error {
	if job == nil {
		return errNotFound
	}
	if reportstore.HasRunExportLink(job) {
		return reportstore.ErrInvalidTransition
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
	t2Schema, err := hasColumns(ctx, db, "report_export_job", "report_run_id", "report_run_revision", "export_request_id")
	if err != nil {
		return nil, err
	}
	query := jobSelect(t2Schema) + `
WHERE job_id = ?`
	args := []interface{}{strings.TrimSpace(jobID)}
	if !internal {
		query += ` AND owner_id = ?`
		args = append(args, ownerID)
	}
	query += `
LIMIT 1`
	row := db.QueryRowContext(ctx, query, args...)
	record, err := scanJob(row, t2Schema)
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
	t2Schema, err := hasColumns(ctx, db, "report_export_job", "report_run_id", "report_run_revision", "export_request_id")
	if err != nil {
		return nil, err
	}
	query := jobSelect(t2Schema)
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
	return scanJobs(rows, t2Schema)
}

func (s *Store) SubmitJobFromRun(ctx context.Context, candidate *reportjob.Record) (*reportjob.Record, bool, error) {
	ownerID := effectiveOwnerID(ctx)
	if candidate == nil || ownerID == "" || ownerID != strings.TrimSpace(candidate.OwnerID) {
		return nil, false, reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, false, err
	}
	t2Schema, err := s.runExportSchemaReady(ctx, db)
	if err != nil {
		return nil, false, err
	}
	if !t2Schema {
		return nil, false, reportstore.ErrSchemaRequired
	}
	if err := reportstore.ValidateRunExportCandidate(candidate); err != nil {
		return nil, false, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	existing, err := getJobByExportRequest(ctx, tx, ownerID, candidate.ConversationID, candidate.ExportRequestID)
	switch {
	case err == nil:
		if sameRunExportRequest(existing, candidate) {
			return existing, true, nil
		}
		return nil, false, reportstore.ErrConflict
	case !errors.Is(err, sql.ErrNoRows):
		return nil, false, err
	}

	run, err := scanReportRun(tx.QueryRowContext(ctx, reportRunSelect+`
WHERE report_run_id = ? AND owner_id = ?
LIMIT 1`, strings.TrimSpace(candidate.ReportRunID), ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, reportstore.ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(run.ConversationID) == "" ||
		strings.TrimSpace(run.ConversationID) != strings.TrimSpace(candidate.ConversationID) {
		return nil, false, reportstore.ErrNotFound
	}
	if run.Status != reportrun.StatusCompleted ||
		run.Revision < 1 ||
		len(bytes.TrimSpace(run.ReportSpec)) == 0 ||
		len(bytes.TrimSpace(run.ReportFill)) == 0 ||
		len(bytes.TrimSpace(run.ReportPrint)) == 0 {
		return nil, false, reportstore.ErrInvalidTransition
	}

	result, err := tx.ExecContext(ctx, `
INSERT INTO report_export_job (
  job_id, artifact_ref, owner_id, conversation_id, workspace_id, auth_context_ref, format, scope, status,
  report_run_id, report_run_revision, export_request_id,
  report_spec_json, report_fill_json, report_print_json, metadata_json, artifact_id, error_text, diagnostics_json,
  submitted_at, started_at, completed_at, retention_ttl_sec
) SELECT ?, ?, ?, r.conversation_id, NULL, ?, ?, ?, 'queued',
         r.report_run_id, r.revision, ?,
         r.report_spec_json, r.report_fill_json, r.report_print_json, NULL, NULL, NULL, NULL,
         ?, NULL, NULL, ?
FROM report_run r
WHERE r.report_run_id = ?
  AND r.owner_id = ?
  AND r.conversation_id = ?
  AND r.status = ?
  AND r.revision = ?
  AND r.report_spec_json IS NOT NULL AND length(r.report_spec_json) > 0
  AND r.report_fill_json IS NOT NULL AND length(r.report_fill_json) > 0
  AND r.report_print_json IS NOT NULL AND length(r.report_print_json) > 0`,
		strings.TrimSpace(candidate.JobID),
		"report-run://"+strings.TrimSpace(candidate.ReportRunID),
		ownerID,
		nullIfEmpty(candidate.AuthContextRef),
		"pdf",
		"draft",
		strings.TrimSpace(candidate.ExportRequestID),
		candidate.SubmittedAt.UTC(),
		durationSeconds(candidate.RetentionTTL),
		strings.TrimSpace(candidate.ReportRunID),
		ownerID,
		strings.TrimSpace(candidate.ConversationID),
		reportrun.StatusCompleted,
		run.Revision,
	)
	if err != nil {
		if !isDuplicateError(err) {
			return nil, false, err
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return nil, false, rollbackErr
		}
		existing, replayErr := getJobByExportRequest(ctx, db, ownerID, candidate.ConversationID, candidate.ExportRequestID)
		if replayErr == nil {
			if sameRunExportRequest(existing, candidate) {
				return existing, true, nil
			}
			return nil, false, reportstore.ErrConflict
		}
		if !errors.Is(replayErr, sql.ErrNoRows) {
			return nil, false, replayErr
		}
		return nil, false, reportstore.ErrAlreadyExists
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if affected != 1 {
		run, runErr := scanReportRun(tx.QueryRowContext(ctx, reportRunSelect+`
WHERE report_run_id = ? AND owner_id = ?
LIMIT 1`, strings.TrimSpace(candidate.ReportRunID), ownerID))
		switch {
		case errors.Is(runErr, sql.ErrNoRows):
			return nil, false, reportstore.ErrNotFound
		case runErr != nil:
			return nil, false, runErr
		case strings.TrimSpace(run.ConversationID) == "" ||
			strings.TrimSpace(run.ConversationID) != strings.TrimSpace(candidate.ConversationID):
			return nil, false, reportstore.ErrNotFound
		default:
			return nil, false, reportstore.ErrInvalidTransition
		}
	}
	job, err := getJobFromExecutor(ctx, tx, true, candidate.JobID, ownerID, false)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return job, false, nil
}

func (s *Store) ClaimJob(ctx context.Context, jobID string, startedAt time.Time) (*reportjob.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	internal := hasInternalAccess(ctx)
	if ownerID == "" && !internal {
		return nil, reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, err
	}
	t2Schema, err := hasColumns(ctx, db, "report_export_job", "report_run_id", "report_run_revision", "export_request_id")
	if err != nil {
		return nil, err
	}
	query := `
UPDATE report_export_job
SET status = ?, started_at = ?`
	query += `
WHERE job_id = ? AND status = ?`
	args := []interface{}{"running", startedAt.UTC(), strings.TrimSpace(jobID), "queued"}
	if !internal {
		query += ` AND owner_id = ?`
		args = append(args, ownerID)
	}
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		if _, getErr := getJobFromExecutor(ctx, db, t2Schema, jobID, ownerID, internal); errors.Is(getErr, sql.ErrNoRows) {
			return nil, reportstore.ErrNotFound
		}
		return nil, reportstore.ErrInvalidTransition
	}
	job, err := getJobFromExecutor(ctx, db, t2Schema, jobID, ownerID, internal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, reportstore.ErrNotFound
	}
	return job, err
}

func (s *Store) CompleteJobWithArtifact(ctx context.Context, jobID string, artifact *reportartifact.Record, diagnostics []byte, completedAt time.Time, retentionTTL time.Duration) (*reportjob.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	internal := hasInternalAccess(ctx)
	if artifact == nil || (ownerID == "" && !internal) {
		return nil, reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, err
	}
	t2Schema, err := hasColumns(ctx, db, "report_export_job", "report_run_id", "report_run_revision", "export_request_id")
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	job, err := getJobFromExecutor(ctx, tx, t2Schema, jobID, ownerID, internal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, reportstore.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	existingArtifact, artifactErr := getArtifactByJob(ctx, tx, job.JobID)
	if artifactErr != nil && !errors.Is(artifactErr, sql.ErrNoRows) {
		return nil, artifactErr
	}
	if job.Status == "succeeded" && existingArtifact != nil &&
		strings.TrimSpace(job.ArtifactID) == strings.TrimSpace(existingArtifact.ArtifactID) &&
		sameArtifactJob(existingArtifact, job) {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return job, nil
	}
	if job.Status != "running" {
		return nil, reportstore.ErrInvalidTransition
	}
	persistedArtifact := existingArtifact
	if persistedArtifact != nil {
		if !sameArtifactJob(persistedArtifact, job) {
			return nil, reportstore.ErrConflict
		}
	} else {
		persistedArtifact = cloneArtifact(artifact)
		persistedArtifact.JobID = job.JobID
		persistedArtifact.OwnerID = job.OwnerID
		persistedArtifact.ArtifactRef = job.ArtifactRef
		persistedArtifact.Format = job.Format
		_, err = tx.ExecContext(ctx, `
INSERT INTO report_export_artifact (
  artifact_id, job_id, artifact_ref, owner_id, format, content_type, inline_data, created_at, retention_ttl_sec
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			strings.TrimSpace(persistedArtifact.ArtifactID), persistedArtifact.JobID, persistedArtifact.ArtifactRef,
			persistedArtifact.OwnerID, persistedArtifact.Format, strings.TrimSpace(persistedArtifact.ContentType),
			append([]byte{}, persistedArtifact.Data...), persistedArtifact.CreatedAt.UTC(), durationSeconds(persistedArtifact.RetentionTTL),
		)
		if err != nil {
			if isDuplicateError(err) {
				if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
					return nil, rollbackErr
				}
				replayedJob, jobErr := getJobFromExecutor(ctx, db, t2Schema, job.JobID, ownerID, internal)
				if jobErr == nil &&
					replayedJob.Status == "succeeded" &&
					strings.TrimSpace(replayedJob.ArtifactID) != "" {
					replayedArtifact, replayErr := getArtifactByJob(ctx, db, replayedJob.JobID)
					if replayErr == nil &&
						strings.TrimSpace(replayedArtifact.ArtifactID) == strings.TrimSpace(replayedJob.ArtifactID) &&
						sameArtifactJob(replayedArtifact, replayedJob) {
						return replayedJob, nil
					}
				}
				return nil, reportstore.ErrAlreadyExists
			}
			return nil, err
		}
	}
	query := `
UPDATE report_export_job
SET status = ?, artifact_id = ?, error_text = NULL, diagnostics_json = ?, completed_at = ?, retention_ttl_sec = ?`
	query += `
WHERE job_id = ? AND status = ?`
	args := []interface{}{
		"succeeded", persistedArtifact.ArtifactID, clonedBytes(diagnostics), completedAt.UTC(),
		durationSeconds(retentionTTL), job.JobID, "running",
	}
	if !internal {
		query += ` AND owner_id = ?`
		args = append(args, ownerID)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		return nil, reportstore.ErrInvalidTransition
	}
	completed, err := getJobFromExecutor(ctx, tx, t2Schema, job.JobID, ownerID, internal)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return completed, nil
}

func (s *Store) FailJob(ctx context.Context, jobID, errorText string, diagnostics []byte, completedAt time.Time) (*reportjob.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	internal := hasInternalAccess(ctx)
	if ownerID == "" && !internal {
		return nil, reportstore.ErrNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return nil, err
	}
	t2Schema, err := hasColumns(ctx, db, "report_export_job", "report_run_id", "report_run_revision", "export_request_id")
	if err != nil {
		return nil, err
	}
	query := `
UPDATE report_export_job
SET status = ?, error_text = ?, diagnostics_json = ?, completed_at = ?`
	query += `
WHERE job_id = ? AND status = ?`
	args := []interface{}{
		"failed", strings.TrimSpace(errorText), clonedBytes(diagnostics), completedAt.UTC(),
		strings.TrimSpace(jobID), "running",
	}
	if !internal {
		query += ` AND owner_id = ?`
		args = append(args, ownerID)
	}
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		if _, getErr := getJobFromExecutor(ctx, db, t2Schema, jobID, ownerID, internal); errors.Is(getErr, sql.ErrNoRows) {
			return nil, reportstore.ErrNotFound
		}
		return nil, reportstore.ErrInvalidTransition
	}
	job, err := getJobFromExecutor(ctx, db, t2Schema, jobID, ownerID, internal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, reportstore.ErrNotFound
	}
	return job, err
}

func (s *Store) ReconcileRunningJobs(ctx context.Context, staleBefore, reconciledAt time.Time, errorText string) ([]*reportjob.Record, error) {
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.ListArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	artifactByJob := make(map[string]*reportartifact.Record, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		jobID := strings.TrimSpace(artifact.JobID)
		if prior := artifactByJob[jobID]; prior != nil &&
			strings.TrimSpace(prior.ArtifactID) != strings.TrimSpace(artifact.ArtifactID) {
			return nil, reportstore.ErrConflict
		}
		artifactByJob[jobID] = artifact
	}
	result := []*reportjob.Record{}
	for _, job := range jobs {
		if job == nil || job.Status != "running" ||
			(job.StartedAt != nil && job.StartedAt.After(staleBefore)) {
			continue
		}
		if artifact := artifactByJob[strings.TrimSpace(job.JobID)]; artifact != nil {
			completed, completeErr := s.CompleteJobWithArtifact(
				ctx, job.JobID, artifact, job.Diagnostics, reconciledAt, job.RetentionTTL,
			)
			if errors.Is(completeErr, reportstore.ErrInvalidTransition) {
				continue
			}
			if completeErr != nil {
				return nil, completeErr
			}
			result = append(result, completed)
			continue
		}
		failed, failErr := s.FailJob(ctx, job.JobID, errorText, job.Diagnostics, reconciledAt)
		if errors.Is(failErr, reportstore.ErrInvalidTransition) {
			continue
		}
		if failErr != nil {
			return nil, failErr
		}
		result = append(result, failed)
	}
	return result, nil
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
	t2Schema, err := hasColumns(ctx, db, "report_export_job", "report_run_id", "report_run_revision", "export_request_id")
	if err != nil {
		return err
	}
	current, err := getJobFromExecutor(ctx, db, t2Schema, job.JobID, ownerID, internal)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	if reportstore.HasRunExportLink(current) || reportstore.HasRunExportLink(job) {
		return reportstore.ErrInvalidTransition
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
	job, err := s.GetJob(ctx, strings.TrimSpace(artifact.JobID))
	if err != nil {
		return err
	}
	if !sameArtifactJob(artifact, job) {
		return reportstore.ErrConflict
	}
	if existing, existingErr := getArtifactByJob(ctx, db, artifact.JobID); existingErr == nil && existing != nil {
		return reportstore.ErrAlreadyExists
	} else if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return existingErr
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

// DeleteSharedArtifact removes an owned shared reporting artifact.
func (s *Store) DeleteSharedArtifact(ctx context.Context, artifactID string) error {
	ownerID := effectiveOwnerID(ctx)
	id := strings.TrimSpace(artifactID)
	if ownerID == "" || id == "" {
		return errNotFound
	}
	current, err := s.GetSharedArtifact(ctx, id)
	if err != nil {
		return err
	}
	if current == nil || strings.TrimSpace(current.OwnerID) != ownerID {
		return errNotFound
	}
	db, err := s.dbHandle()
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `
DELETE FROM report_shared_artifact
WHERE artifact_id = ? AND owner_id = ?`, id, ownerID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errNotFound
	}
	return nil
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

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

func hasColumns(ctx context.Context, db *sql.DB, table string, required ...string) (bool, error) {
	switch table {
	case "report_export_job":
	default:
		return false, fmt.Errorf("reporting sql store: unsupported schema inspection table %q", table)
	}
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+table+" WHERE 1 = 0")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return false, err
	}
	available := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		available[strings.ToLower(strings.TrimSpace(column))] = struct{}{}
	}
	for _, column := range required {
		if _, ok := available[strings.ToLower(strings.TrimSpace(column))]; !ok {
			return false, nil
		}
	}
	return true, nil
}

var (
	runExportJobColumns = []string{
		"job_id", "artifact_ref", "owner_id", "conversation_id", "workspace_id", "auth_context_ref",
		"format", "scope", "status", "report_run_id", "report_run_revision", "export_request_id",
		"report_spec_json", "report_fill_json", "report_print_json", "metadata_json", "artifact_id",
		"error_text", "diagnostics_json", "submitted_at", "started_at", "completed_at", "retention_ttl_sec",
	}
	runExportRunColumns = []string{
		"report_run_id", "owner_id", "conversation_id", "materializer", "origin", "builder_ref", "preset_id",
		"source_kind", "source_id", "requested_params_json", "effective_params_json", "status", "failure_code",
		"failure_text", "started_at", "completed_at", "revision", "ui_run_request_id", "report_spec_json",
		"report_fill_json", "report_print_json", "activation_source", "adoption_source", "actor_id", "created_at",
		"updated_at",
	}
	runExportArtifactColumns = []string{
		"artifact_id", "job_id", "artifact_ref", "owner_id", "format", "content_type", "inline_data",
		"created_at", "retention_ttl_sec",
	}
)

func (s *Store) runExportSchemaReady(ctx context.Context, db *sql.DB) (bool, error) {
	if s == nil || s.dao == nil || db == nil {
		return false, nil
	}
	connector, err := s.dao.Resource().Connector(s.connectorRef)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(connector.Driver)) {
	case "sqlite", "sqlite3":
		return sqliteRunExportSchemaReady(ctx, db)
	case "mysql":
		return mysqlRunExportSchemaReady(ctx, db)
	default:
		return false, nil
	}
}

func sqliteRunExportSchemaReady(ctx context.Context, db *sql.DB) (bool, error) {
	for table, required := range map[string][]string{
		"report_export_job":      runExportJobColumns,
		"report_run":             runExportRunColumns,
		"report_export_artifact": runExportArtifactColumns,
	} {
		ready, err := sqliteTableHasColumns(ctx, db, table, required)
		if err != nil || !ready {
			return ready, err
		}
	}
	for _, requirement := range []struct {
		table   string
		columns []string
	}{
		{table: "report_export_job", columns: []string{"owner_id", "conversation_id", "export_request_id"}},
		{table: "report_export_artifact", columns: []string{"job_id"}},
	} {
		ready, err := sqliteHasUniqueColumns(ctx, db, requirement.table, requirement.columns)
		if err != nil || !ready {
			return ready, err
		}
	}
	for _, requirement := range []struct {
		table, column, referencedTable, referencedColumn string
	}{
		{
			table: "report_export_job", column: "report_run_id",
			referencedTable: "report_run", referencedColumn: "report_run_id",
		},
		{
			table: "report_export_artifact", column: "job_id",
			referencedTable: "report_export_job", referencedColumn: "job_id",
		},
	} {
		ready, err := sqliteHasRestrictForeignKey(
			ctx, db, requirement.table, requirement.column, requirement.referencedTable, requirement.referencedColumn,
		)
		if err != nil || !ready {
			return ready, err
		}
	}
	// SQLite has structured PRAGMA introspection for columns, indexes, and foreign
	// keys, but not for CHECK expressions. Avoid treating parsed CREATE TABLE text
	// as a reliable runtime schema contract.
	return true, nil
}

func mysqlRunExportSchemaReady(ctx context.Context, db *sql.DB) (bool, error) {
	for table, required := range map[string][]string{
		"report_export_job":      runExportJobColumns,
		"report_run":             runExportRunColumns,
		"report_export_artifact": runExportArtifactColumns,
	} {
		ready, err := mysqlTableHasColumns(ctx, db, table, required)
		if err != nil || !ready {
			return ready, err
		}
	}
	for _, requirement := range []struct {
		table   string
		columns []string
	}{
		{table: "report_export_job", columns: []string{"owner_id", "conversation_id", "export_request_id"}},
		{table: "report_export_artifact", columns: []string{"job_id"}},
	} {
		ready, err := mysqlHasUniqueColumns(ctx, db, requirement.table, requirement.columns)
		if err != nil || !ready {
			return ready, err
		}
	}
	for _, requirement := range []struct {
		table, column, referencedTable, referencedColumn string
	}{
		{
			table: "report_export_job", column: "report_run_id",
			referencedTable: "report_run", referencedColumn: "report_run_id",
		},
		{
			table: "report_export_artifact", column: "job_id",
			referencedTable: "report_export_job", referencedColumn: "job_id",
		},
	} {
		ready, err := mysqlHasRestrictForeignKey(
			ctx, db, requirement.table, requirement.column, requirement.referencedTable, requirement.referencedColumn,
		)
		if err != nil || !ready {
			return ready, err
		}
	}
	for constraintName, requiredTerms := range map[string][]string{
		"chk_report_export_job_status": {
			"statusin(", "'queued'", "'running'", "'succeeded'", "'failed'",
		},
		"chk_report_export_job_run_reference": {
			"report_run_idisnull", "report_run_idisnotnull",
			"report_run_revisionisnull", "report_run_revisionisnotnull", "report_run_revision>0",
			"export_request_idisnull", "export_request_idisnotnull", "conversation_idisnotnull",
		},
		"chk_report_export_job_run_pdf": {
			"report_run_idisnull", "format=", "'pdf'", "scope=", "'draft'",
		},
	} {
		ready, err := mysqlHasCheckConstraint(ctx, db, "report_export_job", constraintName, requiredTerms)
		if err != nil || !ready {
			return ready, err
		}
	}
	return true, nil
}

func sqliteTableHasColumns(ctx context.Context, db *sql.DB, table string, required []string) (bool, error) {
	query, ok := sqliteTableInfoQuery(table)
	if !ok {
		return false, nil
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	available := map[string]struct{}{}
	for rows.Next() {
		var (
			position, notNull, primaryKey int
			name, columnType              string
			defaultValue                  interface{}
		)
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		available[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return containsColumns(available, required), nil
}

func sqliteTableInfoQuery(table string) (string, bool) {
	switch table {
	case "report_export_job", "report_run", "report_export_artifact":
		return "PRAGMA table_info(" + table + ")", true
	default:
		return "", false
	}
}

func sqliteIndexListQuery(table string) (string, bool) {
	switch table {
	case "report_export_job", "report_export_artifact":
		return "PRAGMA index_list(" + table + ")", true
	default:
		return "", false
	}
}

func sqliteForeignKeyListQuery(table string) (string, bool) {
	switch table {
	case "report_export_job", "report_export_artifact":
		return "PRAGMA foreign_key_list(" + table + ")", true
	default:
		return "", false
	}
}

func sqliteHasUniqueColumns(ctx context.Context, db *sql.DB, table string, required []string) (bool, error) {
	query, ok := sqliteIndexListQuery(table)
	if !ok {
		return false, nil
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return false, err
	}
	indexNames := []string{}
	for rows.Next() {
		var (
			sequence, unique, partial int
			name, origin              string
		)
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return false, err
		}
		if unique == 1 && partial == 0 {
			indexNames = append(indexNames, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, indexName := range indexNames {
		escapedName := strings.ReplaceAll(indexName, `"`, `""`)
		indexRows, err := db.QueryContext(ctx, `PRAGMA index_info("`+escapedName+`")`)
		if err != nil {
			return false, err
		}
		columns := []string{}
		for indexRows.Next() {
			var sequence, columnID int
			var columnName sql.NullString
			if err := indexRows.Scan(&sequence, &columnID, &columnName); err != nil {
				indexRows.Close()
				return false, err
			}
			if columnName.Valid {
				columns = append(columns, columnName.String)
			}
		}
		if err := indexRows.Err(); err != nil {
			indexRows.Close()
			return false, err
		}
		if err := indexRows.Close(); err != nil {
			return false, err
		}
		if sameColumns(columns, required) {
			return true, nil
		}
	}
	return false, nil
}

func sqliteHasRestrictForeignKey(ctx context.Context, db *sql.DB, table, column, referencedTable, referencedColumn string) (bool, error) {
	query, ok := sqliteForeignKeyListQuery(table)
	if !ok {
		return false, nil
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, sequence int
		var targetTable, sourceColumn, targetColumn, onUpdate, onDelete, match string
		if err := rows.Scan(
			&id, &sequence, &targetTable, &sourceColumn, &targetColumn, &onUpdate, &onDelete, &match,
		); err != nil {
			return false, err
		}
		if strings.EqualFold(sourceColumn, column) &&
			strings.EqualFold(targetTable, referencedTable) &&
			strings.EqualFold(targetColumn, referencedColumn) &&
			strings.EqualFold(onDelete, "RESTRICT") {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func mysqlTableHasColumns(ctx context.Context, db *sql.DB, table string, required []string) (bool, error) {
	rows, err := db.QueryContext(ctx, `
SELECT column_name
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = ?`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	available := map[string]struct{}{}
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return false, err
		}
		available[strings.ToLower(strings.TrimSpace(column))] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return containsColumns(available, required), nil
}

func mysqlHasUniqueColumns(ctx context.Context, db *sql.DB, table string, required []string) (bool, error) {
	rows, err := db.QueryContext(ctx, `
SELECT index_name, non_unique, seq_in_index, column_name, sub_part
FROM information_schema.statistics
WHERE table_schema = DATABASE() AND table_name = ?
ORDER BY index_name, seq_in_index`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	indexColumns := map[string][]string{}
	validIndex := map[string]bool{}
	for rows.Next() {
		var (
			indexName           string
			nonUnique, sequence int
			columnName          sql.NullString
			subPart             sql.NullInt64
		)
		if err := rows.Scan(&indexName, &nonUnique, &sequence, &columnName, &subPart); err != nil {
			return false, err
		}
		if _, seen := validIndex[indexName]; !seen {
			validIndex[indexName] = nonUnique == 0
		}
		if nonUnique != 0 || !columnName.Valid || subPart.Valid {
			validIndex[indexName] = false
			continue
		}
		indexColumns[indexName] = append(indexColumns[indexName], columnName.String)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for indexName, columns := range indexColumns {
		if validIndex[indexName] && sameColumns(columns, required) {
			return true, nil
		}
	}
	return false, nil
}

func mysqlHasRestrictForeignKey(ctx context.Context, db *sql.DB, table, column, referencedTable, referencedColumn string) (bool, error) {
	rows, err := db.QueryContext(ctx, `
SELECT k.column_name, k.referenced_table_name, k.referenced_column_name, r.delete_rule
FROM information_schema.key_column_usage k
JOIN information_schema.referential_constraints r
  ON r.constraint_schema = k.constraint_schema
 AND r.table_name = k.table_name
 AND r.constraint_name = k.constraint_name
WHERE k.constraint_schema = DATABASE()
  AND k.table_name = ?
  AND k.referenced_table_name IS NOT NULL`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var sourceColumn, targetTable, targetColumn, deleteRule string
		if err := rows.Scan(&sourceColumn, &targetTable, &targetColumn, &deleteRule); err != nil {
			return false, err
		}
		if strings.EqualFold(sourceColumn, column) &&
			strings.EqualFold(targetTable, referencedTable) &&
			strings.EqualFold(targetColumn, referencedColumn) &&
			strings.EqualFold(deleteRule, "RESTRICT") {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func mysqlHasCheckConstraint(ctx context.Context, db *sql.DB, table, constraint string, requiredTerms []string) (bool, error) {
	var clause string
	err := db.QueryRowContext(ctx, `
SELECT cc.check_clause
FROM information_schema.table_constraints tc
JOIN information_schema.check_constraints cc
  ON cc.constraint_schema = tc.constraint_schema
 AND cc.constraint_name = tc.constraint_name
WHERE tc.constraint_schema = DATABASE()
  AND tc.table_name = ?
  AND tc.constraint_type = 'CHECK'
  AND tc.constraint_name = ?
LIMIT 1`, table, constraint).Scan(&clause)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	normalized := normalizeCheckClause(clause)
	for _, term := range requiredTerms {
		if !strings.Contains(normalized, strings.ToLower(term)) {
			return false, nil
		}
	}
	return true, nil
}

func normalizeCheckClause(clause string) string {
	normalized := strings.ToLower(clause)
	normalized = strings.NewReplacer(
		" ", "", "\t", "", "\r", "", "\n", "", "`", "",
		"_utf8mb4", "", "_utf8mb3", "", "\\'", "'",
	).Replace(normalized)
	return normalized
}

func containsColumns(available map[string]struct{}, required []string) bool {
	for _, column := range required {
		if _, ok := available[strings.ToLower(strings.TrimSpace(column))]; !ok {
			return false
		}
	}
	return true
}

func sameColumns(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	actualColumns := make(map[string]int, len(actual))
	for _, column := range actual {
		actualColumns[strings.ToLower(strings.TrimSpace(column))]++
	}
	for _, column := range expected {
		key := strings.ToLower(strings.TrimSpace(column))
		if actualColumns[key] == 0 {
			return false
		}
		actualColumns[key]--
	}
	return true
}

func getJobFromExecutor(ctx context.Context, queryer rowQueryer, t2Schema bool, jobID, ownerID string, internal bool) (*reportjob.Record, error) {
	query := jobSelect(t2Schema) + `
WHERE job_id = ?`
	args := []interface{}{strings.TrimSpace(jobID)}
	if !internal {
		query += ` AND owner_id = ?`
		args = append(args, strings.TrimSpace(ownerID))
	}
	query += `
LIMIT 1`
	return scanJob(queryer.QueryRowContext(ctx, query, args...), t2Schema)
}

func getJobByExportRequest(ctx context.Context, queryer rowQueryer, ownerID, conversationID, exportRequestID string) (*reportjob.Record, error) {
	query := jobSelect(true) + `
WHERE owner_id = ? AND conversation_id = ? AND export_request_id = ?
LIMIT 1`
	return scanJob(
		queryer.QueryRowContext(
			ctx,
			query,
			strings.TrimSpace(ownerID),
			strings.TrimSpace(conversationID),
			strings.TrimSpace(exportRequestID),
		),
		true,
	)
}

func getArtifactByJob(ctx context.Context, queryer rowQueryer, jobID string) (*reportartifact.Record, error) {
	return scanArtifact(queryer.QueryRowContext(ctx, `
SELECT artifact_id, job_id, artifact_ref, owner_id, format, content_type, inline_data, created_at, retention_ttl_sec
FROM report_export_artifact
WHERE job_id = ?
LIMIT 1`, strings.TrimSpace(jobID)))
}

func sameRunExportRequest(existing, candidate *reportjob.Record) bool {
	return existing != nil && candidate != nil &&
		strings.TrimSpace(existing.ReportRunID) == strings.TrimSpace(candidate.ReportRunID) &&
		strings.TrimSpace(existing.Format) == strings.TrimSpace(candidate.Format) &&
		strings.TrimSpace(existing.Scope) == strings.TrimSpace(candidate.Scope)
}

func sameArtifactJob(artifact *reportartifact.Record, job *reportjob.Record) bool {
	return artifact != nil && job != nil &&
		strings.TrimSpace(artifact.JobID) == strings.TrimSpace(job.JobID) &&
		strings.TrimSpace(artifact.OwnerID) == strings.TrimSpace(job.OwnerID) &&
		strings.TrimSpace(artifact.ArtifactRef) == strings.TrimSpace(job.ArtifactRef) &&
		strings.EqualFold(strings.TrimSpace(artifact.Format), strings.TrimSpace(job.Format))
}

func cloneArtifact(input *reportartifact.Record) *reportartifact.Record {
	if input == nil {
		return nil
	}
	out := *input
	out.Data = append([]byte(nil), input.Data...)
	return &out
}

func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}

func jobSelect(t2Schema bool) string {
	extra := ""
	if t2Schema {
		extra = "report_run_id, report_run_revision, export_request_id, "
	}
	return `
SELECT job_id, artifact_ref, owner_id, conversation_id, workspace_id, auth_context_ref, format, scope, status,
       ` + extra + `report_spec_json, report_fill_json, report_print_json, metadata_json, artifact_id, error_text, diagnostics_json,
       submitted_at, started_at, completed_at, retention_ttl_sec
FROM report_export_job
`
}

func scanJob(scanner interface {
	Scan(dest ...interface{}) error
}, t2Schema bool) (*reportjob.Record, error) {
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
	dest := []interface{}{
		&record.JobID, &record.ArtifactRef, &record.OwnerID, &conversationID, &workspaceID, &authContextRef,
		&record.Format, &record.Scope, &record.Status,
	}
	var reportRunID, exportRequestID sql.NullString
	var reportRunRevision sql.NullInt64
	if t2Schema {
		dest = append(dest, &reportRunID, &reportRunRevision, &exportRequestID)
	}
	dest = append(dest,
		&reportSpec, &reportFill, &reportPrint, &metadata, &artifactID,
		&errorText, &diagnostics, &record.SubmittedAt, &startedAt, &completedAt, &retentionSec,
	)
	err := scanner.Scan(dest...)
	if err != nil {
		return nil, err
	}
	record.ConversationID = conversationID.String
	record.WorkspaceID = workspaceID.String
	record.AuthContextRef = authContextRef.String
	record.ReportRunID = reportRunID.String
	record.ReportRunRevision = reportRunRevision.Int64
	record.ExportRequestID = exportRequestID.String
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

func scanJobs(rows *sql.Rows, t2Schema bool) ([]*reportjob.Record, error) {
	result := []*reportjob.Record{}
	for rows.Next() {
		record, err := scanJob(rows, t2Schema)
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
