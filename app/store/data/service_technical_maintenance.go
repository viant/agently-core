package data

import (
	"context"
	"errors"
	"fmt"
	"github.com/viant/agently-core/internal/sqlitewrite"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
	technical "github.com/viant/agently-core/internal/store/technicalmaintenance"
	"strconv"
	"strings"
	"time"
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

// ListTechnicalMaintenanceCandidates delegates retention predicates, bounded
// projection, cursor ordering and limits to the canonical generated readers.
func (s *datlyService) ListTechnicalMaintenanceCandidates(ctx context.Context, request TechnicalMaintenanceCandidateRequest) ([]TechnicalMaintenanceCandidate, error) {
	request = normalizeTechnicalMaintenanceCandidateRequest(request)
	if err := validateTechnicalMaintenanceCandidateRequest(request); err != nil {
		return nil, err
	}
	if _, _, _, err := decodeTechnicalMaintenanceCursor(request.AfterCursor); err != nil {
		return nil, err
	}
	if s == nil || s.native == nil {
		return nil, fmt.Errorf("native technical maintenance runtime is required")
	}
	rows, err := (&technical.Store{Invoker: s.native}).List(ctx, technical.CandidateRequest{Scope: string(request.Scope), OlderThan: request.OlderThan, EvaluatedAt: request.EvaluatedAt, AfterCursor: request.AfterCursor, Limit: request.Limit})
	if err != nil {
		return nil, mapTechnicalMaintenanceError(err)
	}
	result := make([]TechnicalMaintenanceCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, TechnicalMaintenanceCandidate{CursorID: row.CursorID, Kind: TechnicalMaintenanceKind(row.Kind), Scope: TechnicalMaintenanceScope(row.Scope), RecordID: row.RecordID, ObservedAt: row.ObservedAt})
	}
	return result, nil
}

// MaintainTechnicalCandidate enters the lease-fenced private managed unit.
func (s *datlyService) MaintainTechnicalCandidate(ctx context.Context, request TechnicalMaintenanceRequest) (*TechnicalMaintenanceResult, error) {
	request.RecordID = strings.TrimSpace(request.RecordID)
	request.OlderThan = request.OlderThan.UTC()
	request.EvaluatedAt = request.EvaluatedAt.UTC()
	if err := validateTechnicalMaintenanceRequest(request); err != nil {
		return nil, err
	}
	if s == nil || s.native == nil {
		return nil, fmt.Errorf("native technical maintenance runtime is required")
	}
	result, err := sqlitewrite.Do(ctx, s.writeGate, func() (*technical.Result, error) {
		return (&technical.Store{Invoker: s.native}).Maintain(ctx, technical.Request{Kind: string(request.Kind), Scope: string(request.Scope), RecordID: request.RecordID, OlderThan: request.OlderThan, EvaluatedAt: request.EvaluatedAt, Mode: string(request.Mode), Lease: maintenance.Lease{Key: request.Lease.Key, OwnerID: request.Lease.OwnerID, Token: request.Lease.Token, LeaseUntil: request.Lease.LeaseUntil}})
	})
	if err != nil {
		return nil, mapTechnicalMaintenanceError(err)
	}
	return &TechnicalMaintenanceResult{Kind: TechnicalMaintenanceKind(result.Kind), Scope: TechnicalMaintenanceScope(result.Scope), RecordID: result.RecordID, Mode: ConversationMaintenanceMode(result.Mode), Eligible: result.Eligible, Deleted: result.Deleted, DeletedRows: result.DeletedRows, Reason: TechnicalMaintenanceReason(result.Reason)}, nil
}
func mapTechnicalMaintenanceError(err error) error {
	if errors.Is(err, maintenance.ErrLeaseLost) {
		return ErrMaintenanceLeaseLost
	}
	if errors.Is(err, technical.ErrInvalidRequest) {
		return fmt.Errorf("%w: %v", ErrInvalidConversationMaintenanceRequest, err)
	}
	return err
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
