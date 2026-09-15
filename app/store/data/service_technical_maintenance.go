package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/sqlitewrite"
)

// TechnicalMaintenanceScope selects the retention policy associated with a
// technical row. Rows linked to a conversation inherit its interactive or
// scheduled classification. Rows with no live conversation are unclassified.
type TechnicalMaintenanceScope string

const (
	TechnicalMaintenanceInteractive  TechnicalMaintenanceScope = "interactive"
	TechnicalMaintenanceScheduled    TechnicalMaintenanceScope = "scheduled"
	TechnicalMaintenanceUnclassified TechnicalMaintenanceScope = "unclassified"
)

// TechnicalMaintenanceKind identifies the small, bounded unit revalidated by
// technical retention. report_shared_artifact is intentionally absent: saved
// reports are durable user data and must never be selected by this API.
type TechnicalMaintenanceKind string

const (
	TechnicalMaintenanceReportRun       TechnicalMaintenanceKind = "report_run"
	TechnicalMaintenanceReportExportJob TechnicalMaintenanceKind = "report_export_job"
	TechnicalMaintenanceReportAudit     TechnicalMaintenanceKind = "report_audit_event"
	TechnicalMaintenanceSession         TechnicalMaintenanceKind = "session"
)

type TechnicalMaintenanceReason string

const (
	TechnicalMaintenanceEligibleReason         TechnicalMaintenanceReason = "eligible"
	TechnicalMaintenanceDeletedReason          TechnicalMaintenanceReason = "deleted"
	TechnicalMaintenanceNoLongerEligibleReason TechnicalMaintenanceReason = "no_longer_eligible"
)

const technicalMaintenanceCursorSeparator = "\x1c"

// TechnicalMaintenanceCandidateRequest describes one bounded, keyset-paged
// scan. OlderThan is the configured retention cutoff, including for expired
// sessions. EvaluatedAt is captured once per policy pass and is used for
// positive per-row TTLs.
type TechnicalMaintenanceCandidateRequest struct {
	Scope       TechnicalMaintenanceScope
	OlderThan   time.Time
	EvaluatedAt time.Time
	AfterCursor string
	Limit       int
}

// TechnicalMaintenanceCandidate contains identifiers and timestamps only. It
// never loads report JSON, audit metadata, export inline_data, or other blobs.
type TechnicalMaintenanceCandidate struct {
	CursorID   string
	Kind       TechnicalMaintenanceKind
	Scope      TechnicalMaintenanceScope
	RecordID   string
	ObservedAt time.Time
}

type TechnicalMaintenanceRequest struct {
	Kind        TechnicalMaintenanceKind
	Scope       TechnicalMaintenanceScope
	RecordID    string
	OlderThan   time.Time
	EvaluatedAt time.Time
	Mode        ConversationMaintenanceMode
	Lease       MaintenanceLease
}

type TechnicalMaintenanceResult struct {
	Kind        TechnicalMaintenanceKind
	Scope       TechnicalMaintenanceScope
	RecordID    string
	Mode        ConversationMaintenanceMode
	Eligible    bool
	Deleted     bool
	DeletedRows int64
	Reason      TechnicalMaintenanceReason
}

type technicalMaintenanceRule struct {
	Kind     TechnicalMaintenanceKind
	Priority int
}

var technicalMaintenanceRules = []technicalMaintenanceRule{
	{Kind: TechnicalMaintenanceReportRun, Priority: 10},
	{Kind: TechnicalMaintenanceReportExportJob, Priority: 20},
	{Kind: TechnicalMaintenanceReportAudit, Priority: 30},
	{Kind: TechnicalMaintenanceSession, Priority: 40},
}

type technicalMaintenanceQueryer interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
}

// ListTechnicalMaintenanceCandidates returns technical rows whose configured
// retention or positive row TTL has elapsed. Status is deliberately not an
// eligibility condition: a row left queued/running for an entire retention
// period is stale technical state, not evidence of a live worker.
func (s *datlyService) ListTechnicalMaintenanceCandidates(ctx context.Context, request TechnicalMaintenanceCandidateRequest) ([]TechnicalMaintenanceCandidate, error) {
	request = normalizeTechnicalMaintenanceCandidateRequest(request)
	if err := validateTechnicalMaintenanceCandidateRequest(request); err != nil {
		return nil, err
	}
	db, driver, err := s.dbWithDriver()
	if err != nil {
		return nil, err
	}
	if _, err = deleteSchemaCapabilitiesForDriver(driver); err != nil {
		return nil, err
	}

	afterPriority, afterKind, afterRecord, err := decodeTechnicalMaintenanceCursor(request.AfterCursor)
	if err != nil {
		return nil, err
	}
	result := make([]TechnicalMaintenanceCandidate, 0, request.Limit)
	for _, rule := range technicalMaintenanceRules {
		if rule.Kind == TechnicalMaintenanceSession && request.Scope != TechnicalMaintenanceUnclassified {
			continue
		}
		if request.AfterCursor != "" && (rule.Priority < afterPriority || (rule.Priority == afterPriority && string(rule.Kind) < string(afterKind))) {
			continue
		}
		recordAfter := ""
		if request.AfterCursor != "" && rule.Priority == afterPriority && rule.Kind == afterKind {
			recordAfter = afterRecord
		}
		page, listErr := listTechnicalMaintenanceRuleCandidates(ctx, db, driver, rule, request, recordAfter, "", request.Limit-len(result))
		if listErr != nil {
			return nil, listErr
		}
		result = append(result, page...)
		if len(result) == request.Limit {
			break
		}
	}
	return result, nil
}

func normalizeTechnicalMaintenanceCandidateRequest(request TechnicalMaintenanceCandidateRequest) TechnicalMaintenanceCandidateRequest {
	request.OlderThan = request.OlderThan.UTC()
	request.EvaluatedAt = request.EvaluatedAt.UTC()
	request.AfterCursor = strings.TrimSpace(request.AfterCursor)
	return request
}

func validateTechnicalMaintenanceCandidateRequest(request TechnicalMaintenanceCandidateRequest) error {
	if !validTechnicalMaintenanceScope(request.Scope) {
		return fmt.Errorf("%w: unsupported technical maintenance scope %q", ErrInvalidConversationMaintenanceRequest, request.Scope)
	}
	if request.OlderThan.IsZero() || request.EvaluatedAt.IsZero() {
		return fmt.Errorf("%w: technical retention cutoff and evaluation time are required", ErrInvalidConversationMaintenanceRequest)
	}
	if request.OlderThan.After(request.EvaluatedAt) {
		return fmt.Errorf("%w: technical retention cutoff cannot be after evaluation time", ErrInvalidConversationMaintenanceRequest)
	}
	if request.Limit <= 0 {
		return fmt.Errorf("%w: technical candidate limit must be positive", ErrInvalidConversationMaintenanceRequest)
	}
	return nil
}

func validTechnicalMaintenanceScope(scope TechnicalMaintenanceScope) bool {
	switch scope {
	case TechnicalMaintenanceInteractive, TechnicalMaintenanceScheduled, TechnicalMaintenanceUnclassified:
		return true
	default:
		return false
	}
}

func validTechnicalMaintenanceKind(kind TechnicalMaintenanceKind) bool {
	for _, rule := range technicalMaintenanceRules {
		if rule.Kind == kind {
			return true
		}
	}
	return false
}

func encodeTechnicalMaintenanceCursor(rule technicalMaintenanceRule, recordID string) string {
	return fmt.Sprintf("%04d%s%s%s%s", rule.Priority, technicalMaintenanceCursorSeparator, rule.Kind, technicalMaintenanceCursorSeparator, recordID)
}

func decodeTechnicalMaintenanceCursor(cursor string) (int, TechnicalMaintenanceKind, string, error) {
	if cursor == "" {
		return 0, "", "", nil
	}
	parts := strings.SplitN(cursor, technicalMaintenanceCursorSeparator, 3)
	if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
		return 0, "", "", fmt.Errorf("%w: invalid technical maintenance cursor", ErrInvalidConversationMaintenanceRequest)
	}
	priority, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", "", fmt.Errorf("%w: invalid technical maintenance cursor", ErrInvalidConversationMaintenanceRequest)
	}
	kind := TechnicalMaintenanceKind(parts[1])
	for _, rule := range technicalMaintenanceRules {
		if rule.Priority == priority && rule.Kind == kind {
			return priority, kind, parts[2], nil
		}
	}
	return 0, "", "", fmt.Errorf("%w: unknown technical maintenance cursor rule", ErrInvalidConversationMaintenanceRequest)
}

func listTechnicalMaintenanceRuleCandidates(ctx context.Context, queryer technicalMaintenanceQueryer, driver string, rule technicalMaintenanceRule, request TechnicalMaintenanceCandidateRequest, afterRecord, exactRecord string, limit int) ([]TechnicalMaintenanceCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	query, args, err := technicalMaintenanceCandidateSQL(driver, rule.Kind, request, afterRecord, exactRecord, limit)
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]TechnicalMaintenanceCandidate, 0, limit)
	for rows.Next() {
		var recordID string
		var observedRaw sql.NullString
		if err = rows.Scan(&recordID, &observedRaw); err != nil {
			return nil, err
		}
		observedAt, ok := parseDBTime(observedRaw.String)
		if !observedRaw.Valid || !ok {
			return nil, fmt.Errorf("technical maintenance kind=%s record=%q returned invalid timestamp %q", rule.Kind, recordID, observedRaw.String)
		}
		result = append(result, TechnicalMaintenanceCandidate{
			CursorID: encodeTechnicalMaintenanceCursor(rule, recordID), Kind: rule.Kind,
			Scope: request.Scope, RecordID: recordID, ObservedAt: observedAt,
		})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func technicalMaintenanceCandidateSQL(driver string, kind TechnicalMaintenanceKind, request TechnicalMaintenanceCandidateRequest, afterRecord, exactRecord string, limit int) (string, []interface{}, error) {
	scopePredicate := func(conversationExpr string) string {
		return technicalMaintenanceScopePredicate(request.Scope, conversationExpr)
	}
	recordPredicate := func(recordExpr string, args []interface{}) (string, []interface{}) {
		if exactRecord != "" {
			return " AND " + recordExpr + " = ?", append(args, exactRecord)
		}
		if afterRecord != "" {
			if strings.Contains(strings.ToLower(driver), "mysql") {
				return " AND BINARY CAST(" + recordExpr + " AS CHAR) > BINARY ?", append(args, afterRecord)
			}
			return " AND CAST(" + recordExpr + " AS TEXT) COLLATE BINARY > ? COLLATE BINARY", append(args, afterRecord)
		}
		return "", args
	}
	finish := func(query string, args []interface{}, recordExpr string) (string, []interface{}, error) {
		predicate, args := recordPredicate(recordExpr, args)
		query += predicate + " ORDER BY " + recordExpr + " ASC LIMIT ?"
		args = append(args, limit)
		return query, args, nil
	}

	switch kind {
	case TechnicalMaintenanceReportRun:
		// updated_at is NOT NULL and is the report run's last lifecycle activity.
		// Keeping this predicate direct also allows the retention index to be used.
		runAge := "rr.updated_at"
		runExpired := technicalBeforePredicate(driver, runAge)
		contextRecent := technicalAfterPredicate(driver, "crc.updated_at")
		jobExpired := technicalTTLExpiredPredicate(driver, "dep_job", "COALESCE(dep_job.completed_at, dep_job.started_at, dep_job.submitted_at)")
		artifactExpired := technicalTTLExpiredPredicate(driver, "dep_artifact", "dep_artifact.created_at")
		auditRecent := technicalAfterPredicate(driver, "dep_audit.occurred_at")
		query := fmt.Sprintf(`SELECT rr.report_run_id, CAST(%s AS CHAR)
FROM report_run rr
WHERE %s
  AND %s
  AND NOT EXISTS (
      SELECT 1 FROM conversation_report_context crc
      WHERE crc.owner_id = rr.owner_id
        AND crc.active_report_run_id = rr.report_run_id
        AND %s
  )
  AND NOT EXISTS (
      SELECT 1 FROM report_export_job dep_job
      WHERE dep_job.report_run_id = rr.report_run_id
        AND (
            NOT (%s)
            OR EXISTS (
                SELECT 1 FROM report_export_artifact dep_artifact
                WHERE dep_artifact.job_id = dep_job.job_id
                  AND NOT (%s)
            )
            OR EXISTS (
                SELECT 1 FROM report_audit_event dep_audit
                WHERE (dep_audit.job_id = dep_job.job_id
                       OR EXISTS (
                           SELECT 1 FROM report_export_artifact audit_artifact
                           WHERE audit_artifact.job_id = dep_job.job_id
                             AND audit_artifact.artifact_id = dep_audit.artifact_id
                       ))
                  AND %s
            )
        )
  )`, runAge, runExpired, scopePredicate("rr.conversation_id"), contextRecent, jobExpired, artifactExpired, auditRecent)
		args := []interface{}{request.OlderThan, request.OlderThan, request.EvaluatedAt, request.OlderThan, request.EvaluatedAt, request.OlderThan, request.OlderThan}
		return finish(query, args, "rr.report_run_id")

	case TechnicalMaintenanceReportExportJob:
		jobAge := "COALESCE(rej.completed_at, rej.started_at, rej.submitted_at)"
		jobExpired := technicalTTLExpiredPredicate(driver, "rej", jobAge)
		artifactExpired := technicalTTLExpiredPredicate(driver, "dep_artifact", "dep_artifact.created_at")
		auditRecent := technicalAfterPredicate(driver, "dep_audit.occurred_at")
		conversationExpr := "COALESCE(NULLIF(rej.conversation_id, ''), NULLIF(rr.conversation_id, ''))"
		query := fmt.Sprintf(`SELECT rej.job_id, CAST(%s AS CHAR)
FROM report_export_job rej
LEFT JOIN report_run rr ON rr.report_run_id = rej.report_run_id
WHERE %s
  AND %s
  AND NOT EXISTS (
      SELECT 1 FROM report_export_artifact dep_artifact
      WHERE dep_artifact.job_id = rej.job_id
        AND NOT (%s)
  )
  AND NOT EXISTS (
      SELECT 1 FROM report_audit_event dep_audit
      WHERE (dep_audit.job_id = rej.job_id
             OR EXISTS (
                 SELECT 1 FROM report_export_artifact audit_artifact
                 WHERE audit_artifact.job_id = rej.job_id
                   AND audit_artifact.artifact_id = dep_audit.artifact_id
             ))
        AND %s
  )`, jobAge, jobExpired, scopePredicate(conversationExpr), artifactExpired, auditRecent)
		args := []interface{}{request.EvaluatedAt, request.OlderThan, request.EvaluatedAt, request.OlderThan, request.OlderThan}
		return finish(query, args, "rej.job_id")

	case TechnicalMaintenanceReportAudit:
		conversationExpr := `COALESCE(
    NULLIF(direct_job.conversation_id, ''), NULLIF(direct_run.conversation_id, ''),
    NULLIF(artifact_job.conversation_id, ''), NULLIF(artifact_run.conversation_id, '')
)`
		query := fmt.Sprintf(`SELECT rae.event_id, CAST(rae.occurred_at AS CHAR)
FROM report_audit_event rae
LEFT JOIN report_export_job direct_job ON direct_job.job_id = rae.job_id
LEFT JOIN report_run direct_run ON direct_run.report_run_id = direct_job.report_run_id
LEFT JOIN report_export_artifact linked_artifact ON linked_artifact.artifact_id = rae.artifact_id
LEFT JOIN report_export_job artifact_job ON artifact_job.job_id = linked_artifact.job_id
LEFT JOIN report_run artifact_run ON artifact_run.report_run_id = artifact_job.report_run_id
WHERE %s
  AND %s`, technicalBeforePredicate(driver, "rae.occurred_at"), scopePredicate(conversationExpr))
		return finish(query, []interface{}{request.OlderThan}, "rae.event_id")

	case TechnicalMaintenanceSession:
		if request.Scope != TechnicalMaintenanceUnclassified {
			return "", nil, fmt.Errorf("%w: sessions belong to unclassified technical maintenance", ErrInvalidConversationMaintenanceRequest)
		}
		query := fmt.Sprintf(`SELECT sess.id, CAST(sess.expires_at AS CHAR)
FROM session sess
WHERE %s`, technicalBeforePredicate(driver, "sess.expires_at"))
		// expires_at must be older than the retention cutoff, not merely older
		// than the current pass time. This keeps an expired session for the same
		// configured retention period as other unclassified technical state.
		return finish(query, []interface{}{request.OlderThan}, "sess.id")
	default:
		return "", nil, fmt.Errorf("%w: unsupported technical maintenance kind %q", ErrInvalidConversationMaintenanceRequest, kind)
	}
}

func technicalMaintenanceScopePredicate(scope TechnicalMaintenanceScope, conversationExpr string) string {
	scheduled := `(
    COALESCE(technical_conversation.scheduled, 0) <> 0
    OR TRIM(COALESCE(technical_conversation.schedule_id, '')) <> ''
    OR TRIM(COALESCE(technical_conversation.schedule_run_id, '')) <> ''
    OR TRIM(COALESCE(technical_conversation.schedule_kind, '')) <> ''
    OR EXISTS (
        SELECT 1 FROM run technical_run
        WHERE technical_run.conversation_id = technical_conversation.id
          AND LOWER(TRIM(COALESCE(technical_run.conversation_kind, ''))) = 'scheduled'
    )
)`
	switch scope {
	case TechnicalMaintenanceInteractive:
		return fmt.Sprintf("EXISTS (SELECT 1 FROM conversation technical_conversation WHERE technical_conversation.id = %s AND NOT %s)", conversationExpr, scheduled)
	case TechnicalMaintenanceScheduled:
		return fmt.Sprintf("EXISTS (SELECT 1 FROM conversation technical_conversation WHERE technical_conversation.id = %s AND %s)", conversationExpr, scheduled)
	case TechnicalMaintenanceUnclassified:
		return fmt.Sprintf("NOT EXISTS (SELECT 1 FROM conversation technical_conversation WHERE technical_conversation.id = %s)", conversationExpr)
	default:
		return "0 = 1"
	}
}

func technicalBeforePredicate(driver, ageExpr string) string {
	if strings.Contains(strings.ToLower(driver), "sqlite") {
		return ageExpr + " <= ?"
	}
	return ageExpr + " <= ?"
}

func technicalAfterPredicate(driver, ageExpr string) string {
	if strings.Contains(strings.ToLower(driver), "sqlite") {
		return ageExpr + " > ?"
	}
	return ageExpr + " > ?"
}

func technicalTTLExpiredPredicate(driver, alias, ageExpr string) string {
	ttlExpr := "COALESCE(" + alias + ".retention_ttl_sec, 0)"
	if strings.Contains(strings.ToLower(driver), "sqlite") {
		return fmt.Sprintf(`((%s > 0 AND datetime(%s, printf('+%%d seconds', %s)) <= ?)
 OR (%s <= 0 AND %s <= ?))`, ttlExpr, ageExpr, ttlExpr, ttlExpr, ageExpr)
	}
	return fmt.Sprintf(`((%s > 0 AND TIMESTAMPADD(SECOND, %s, %s) <= ?)
 OR (%s <= 0 AND %s <= ?))`, ttlExpr, ttlExpr, ageExpr, ttlExpr, ageExpr)
}

// MaintainTechnicalCandidate rechecks one candidate under the distributed
// lease and removes only database-resident technical state. External artifact
// objects are intentionally outside this operation.
func (s *datlyService) MaintainTechnicalCandidate(ctx context.Context, request TechnicalMaintenanceRequest) (*TechnicalMaintenanceResult, error) {
	request.RecordID = strings.TrimSpace(request.RecordID)
	request.OlderThan = request.OlderThan.UTC()
	request.EvaluatedAt = request.EvaluatedAt.UTC()
	if err := validateTechnicalMaintenanceRequest(request); err != nil {
		return nil, err
	}
	result := &TechnicalMaintenanceResult{Kind: request.Kind, Scope: request.Scope, RecordID: request.RecordID, Mode: request.Mode}
	_, err := sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) {
		return struct{}{}, s.maintainTechnicalCandidateDirect(ctx, request, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validateTechnicalMaintenanceRequest(request TechnicalMaintenanceRequest) error {
	if !validTechnicalMaintenanceKind(request.Kind) {
		return fmt.Errorf("%w: unsupported technical maintenance kind %q", ErrInvalidConversationMaintenanceRequest, request.Kind)
	}
	if !validTechnicalMaintenanceScope(request.Scope) {
		return fmt.Errorf("%w: unsupported technical maintenance scope %q", ErrInvalidConversationMaintenanceRequest, request.Scope)
	}
	if request.Kind == TechnicalMaintenanceSession && request.Scope != TechnicalMaintenanceUnclassified {
		return fmt.Errorf("%w: sessions belong to unclassified technical maintenance", ErrInvalidConversationMaintenanceRequest)
	}
	if request.RecordID == "" || request.OlderThan.IsZero() || request.EvaluatedAt.IsZero() {
		return fmt.Errorf("%w: technical record id, retention cutoff and evaluation time are required", ErrInvalidConversationMaintenanceRequest)
	}
	if request.OlderThan.After(request.EvaluatedAt) {
		return fmt.Errorf("%w: technical retention cutoff cannot be after evaluation time", ErrInvalidConversationMaintenanceRequest)
	}
	if request.Mode != ConversationMaintenanceDryRun && request.Mode != ConversationMaintenanceDelete {
		return fmt.Errorf("%w: unsupported technical maintenance mode %q", ErrInvalidConversationMaintenanceRequest, request.Mode)
	}
	if request.Mode == ConversationMaintenanceDelete {
		if err := validateMaintenanceLease(normalizeMaintenanceLease(request.Lease)); err != nil {
			return fmt.Errorf("%w: technical delete requires maintenance lease: %v", ErrInvalidConversationMaintenanceRequest, err)
		}
	}
	return nil
}

func (s *datlyService) maintainTechnicalCandidateDirect(ctx context.Context, request TechnicalMaintenanceRequest, result *TechnicalMaintenanceResult) error {
	db, driver, err := s.dbWithDriver()
	if err != nil {
		return err
	}
	if _, err = deleteSchemaCapabilitiesForDriver(driver); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if request.Mode == ConversationMaintenanceDelete {
		if _, err = lockMaintenanceLeaseTx(ctx, tx, driver, request.Lease); err != nil {
			return err
		}
		if err = lockTechnicalMaintenanceRecord(ctx, tx, driver, request.Kind, request.RecordID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				result.Reason = TechnicalMaintenanceNoLongerEligibleReason
				if err = tx.Commit(); err != nil {
					return err
				}
				committed = true
				return nil
			}
			return err
		}
	}
	rule := technicalMaintenanceRuleByKind(request.Kind)
	candidates, err := listTechnicalMaintenanceRuleCandidates(ctx, tx, driver, rule, TechnicalMaintenanceCandidateRequest{
		Scope: request.Scope, OlderThan: request.OlderThan, EvaluatedAt: request.EvaluatedAt, Limit: 1,
	}, "", request.RecordID, 1)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		result.Reason = TechnicalMaintenanceNoLongerEligibleReason
		if err = tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}
	result.Eligible = true
	if request.Mode == ConversationMaintenanceDryRun {
		result.Reason = TechnicalMaintenanceEligibleReason
		if err = tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}

	result.DeletedRows, err = deleteTechnicalMaintenanceRecord(ctx, tx, request.Kind, request.RecordID)
	if err != nil {
		return err
	}
	if result.DeletedRows <= 0 {
		return fmt.Errorf("technical maintenance kind=%s record=%q deleted no rows after successful recheck", request.Kind, request.RecordID)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	result.Deleted = true
	result.Reason = TechnicalMaintenanceDeletedReason
	return nil
}

func technicalMaintenanceRuleByKind(kind TechnicalMaintenanceKind) technicalMaintenanceRule {
	for _, rule := range technicalMaintenanceRules {
		if rule.Kind == kind {
			return rule
		}
	}
	return technicalMaintenanceRule{Kind: kind}
}

func lockTechnicalMaintenanceRecord(ctx context.Context, tx *sql.Tx, driver string, kind TechnicalMaintenanceKind, recordID string) error {
	table, key, err := technicalMaintenanceTableKey(kind)
	if err != nil {
		return err
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?", key, table, key)
	if strings.Contains(strings.ToLower(driver), "mysql") {
		query += " FOR UPDATE"
	}
	var found string
	return tx.QueryRowContext(ctx, query, recordID).Scan(&found)
}

func technicalMaintenanceTableKey(kind TechnicalMaintenanceKind) (string, string, error) {
	switch kind {
	case TechnicalMaintenanceReportRun:
		return "report_run", "report_run_id", nil
	case TechnicalMaintenanceReportExportJob:
		return "report_export_job", "job_id", nil
	case TechnicalMaintenanceReportAudit:
		return "report_audit_event", "event_id", nil
	case TechnicalMaintenanceSession:
		return "session", "id", nil
	default:
		return "", "", fmt.Errorf("%w: unsupported technical maintenance kind %q", ErrInvalidConversationMaintenanceRequest, kind)
	}
}

func deleteTechnicalMaintenanceRecord(ctx context.Context, tx *sql.Tx, kind TechnicalMaintenanceKind, recordID string) (int64, error) {
	switch kind {
	case TechnicalMaintenanceReportRun:
		jobIDs, err := queryStringsForColumn(ctx, tx, "SELECT job_id FROM report_export_job WHERE report_run_id IN (%s)", []string{recordID})
		if err != nil {
			return 0, err
		}
		deleted, err := deleteTechnicalReportJobs(ctx, tx, jobIDs)
		if err != nil {
			return 0, err
		}
		count, err := execTechnicalIDs(ctx, tx, "DELETE FROM conversation_report_context WHERE active_report_run_id IN (%s)", []string{recordID})
		deleted += count
		if err != nil {
			return 0, err
		}
		count, err = execTechnicalIDs(ctx, tx, "DELETE FROM report_run WHERE report_run_id IN (%s)", []string{recordID})
		return deleted + count, err
	case TechnicalMaintenanceReportExportJob:
		return deleteTechnicalReportJobs(ctx, tx, []string{recordID})
	case TechnicalMaintenanceReportAudit:
		return execTechnicalIDs(ctx, tx, "DELETE FROM report_audit_event WHERE event_id IN (%s)", []string{recordID})
	case TechnicalMaintenanceSession:
		return execTechnicalIDs(ctx, tx, "DELETE FROM session WHERE id IN (%s)", []string{recordID})
	default:
		return 0, fmt.Errorf("%w: unsupported technical maintenance kind %q", ErrInvalidConversationMaintenanceRequest, kind)
	}
}

func deleteTechnicalReportJobs(ctx context.Context, tx *sql.Tx, jobIDs []string) (int64, error) {
	artifactIDs, err := queryStringsForColumn(ctx, tx, "SELECT artifact_id FROM report_export_artifact WHERE job_id IN (%s)", jobIDs)
	if err != nil {
		return 0, err
	}
	var deleted int64
	count, err := execTechnicalIDs(ctx, tx, "DELETE FROM report_audit_event WHERE job_id IN (%s)", jobIDs)
	deleted += count
	if err != nil {
		return 0, err
	}
	count, err = execTechnicalIDs(ctx, tx, "DELETE FROM report_audit_event WHERE artifact_id IN (%s)", artifactIDs)
	deleted += count
	if err != nil {
		return 0, err
	}
	count, err = execTechnicalIDs(ctx, tx, "DELETE FROM report_export_artifact WHERE artifact_id IN (%s)", artifactIDs)
	deleted += count
	if err != nil {
		return 0, err
	}
	count, err = execTechnicalIDs(ctx, tx, "DELETE FROM report_export_job WHERE job_id IN (%s)", jobIDs)
	return deleted + count, err
}

func execTechnicalIDs(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string) (int64, error) {
	var affected int64
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		result, err := tx.ExecContext(ctx, fmt.Sprintf(queryTemplate, placeholders(len(chunk))), stringArgs(chunk)...)
		if err != nil {
			return affected, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return affected, err
		}
		affected += rows
	}
	return affected, nil
}
