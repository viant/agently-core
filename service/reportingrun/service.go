package reportingrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	authctx "github.com/viant/agently-core/internal/auth"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
)

var (
	ErrNotFound = errors.New("report run: not found")
	ErrInvalid  = errors.New("report run: invalid request")
	ErrConflict = errors.New("report run: conflict")
	ErrCAS      = errors.New("report run: stale revision")
)

const waitTerminalBudget = 300 * time.Second

var waitTerminalBackoff = [...]time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
}

type Options struct {
	Store reportstore.RunClient
	Now   func() time.Time
	NewID func() string
}

// Service owns durable browser report-run lifecycle invariants. It is called
// from the authenticated UI HTTP boundary and is deliberately not a tool
// service.
type Service struct {
	store reportstore.RunClient
	now   func() time.Time
	newID func() string
}

func New(opts Options) *Service {
	if opts.Store == nil {
		panic("reportingrun.New: store is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	newID := opts.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	return &Service{store: opts.Store, now: now, newID: newID}
}

type BeginInput struct {
	ConversationID  string          `json:"conversationId,omitempty"`
	Origin          string          `json:"origin,omitempty"`
	BuilderRef      string          `json:"builderRef,omitempty"`
	PresetID        string          `json:"presetId,omitempty"`
	SourceKind      string          `json:"sourceKind,omitempty"`
	SourceID        string          `json:"sourceId,omitempty"`
	RequestedParams json.RawMessage `json:"requestedParams,omitempty"`
	EffectiveParams json.RawMessage `json:"effectiveParams,omitempty"`
	UIRunRequestID  string          `json:"uiRunRequestId"`
}

type BeginResult struct {
	Run     *reportrun.Record     `json:"run"`
	Context *reportcontext.Record `json:"context,omitempty"`
}

type CompleteInput struct {
	ReportRunID      string          `json:"reportRunId"`
	ConversationID   string          `json:"conversationId,omitempty"`
	ExpectedRevision int64           `json:"expectedRevision"`
	ReportSpec       json.RawMessage `json:"reportSpec"`
	ReportFill       json.RawMessage `json:"reportFill"`
	ReportPrint      json.RawMessage `json:"reportPrint"`
}

type FailInput struct {
	ReportRunID      string `json:"reportRunId"`
	ConversationID   string `json:"conversationId,omitempty"`
	ExpectedRevision int64  `json:"expectedRevision"`
	FailureCode      string `json:"failureCode,omitempty"`
	FailureText      string `json:"failureText,omitempty"`
}

type ActivateInput struct {
	ReportRunID             string `json:"reportRunId"`
	ConversationID          string `json:"conversationId"`
	ExpectedRunRevision     int64  `json:"expectedRunRevision"`
	ExpectedContextRevision int64  `json:"expectedContextRevision"`
	Source                  string `json:"source,omitempty"`
}

type AdoptInput struct {
	ReportRunID             string `json:"reportRunId"`
	ConversationID          string `json:"conversationId"`
	ExpectedRunRevision     int64  `json:"expectedRunRevision"`
	ExpectedContextRevision int64  `json:"expectedContextRevision"`
	Source                  string `json:"source,omitempty"`
}

type AdoptionResult struct {
	Run     *reportrun.Record     `json:"run"`
	Context *reportcontext.Record `json:"context"`
}

func (s *Service) Begin(ctx context.Context, input *BeginInput) (*BeginResult, error) {
	ownerID, err := authenticatedOwner(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, invalid("input is required")
	}
	requestID := strings.TrimSpace(input.UIRunRequestID)
	if requestID == "" {
		return nil, invalid("uiRunRequestId is required")
	}
	origin := strings.ToLower(strings.TrimSpace(input.Origin))
	if origin == "" {
		origin = "manual"
	}
	if origin != "manual" && origin != "prompt" {
		return nil, invalid("origin must be manual or prompt")
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	if origin == "prompt" && conversationID == "" {
		return nil, invalid("prompt run requires a trusted conversation")
	}
	requested, err := normalizeOptionalJSON(input.RequestedParams, "requestedParams")
	if err != nil {
		return nil, err
	}
	effective, err := normalizeOptionalJSON(input.EffectiveParams, "effectiveParams")
	if err != nil {
		return nil, err
	}
	candidate := &reportrun.Record{
		ReportRunID:     strings.TrimSpace(s.newID()),
		OwnerID:         ownerID,
		ConversationID:  conversationID,
		Materializer:    reportrun.MaterializerLegacyBrowser,
		Origin:          origin,
		BuilderRef:      strings.TrimSpace(input.BuilderRef),
		PresetID:        strings.TrimSpace(input.PresetID),
		SourceKind:      strings.TrimSpace(input.SourceKind),
		SourceID:        strings.TrimSpace(input.SourceID),
		RequestedParams: requested,
		EffectiveParams: effective,
		Status:          reportrun.StatusRunning,
		Revision:        1,
		UIRunRequestID:  requestID,
		ActorID:         ownerID,
	}
	if candidate.ReportRunID == "" {
		return nil, fmt.Errorf("%w: ID generator returned an empty ID", ErrInvalid)
	}
	now := s.now().UTC()
	candidate.StartedAt = now
	candidate.CreatedAt = now
	candidate.UpdatedAt = now

	if existing, getErr := s.store.GetReportRunByRequestID(ctx, requestID); getErr == nil {
		if !sameBeginIdentity(existing, candidate) {
			return nil, conflict("uiRunRequestId is already bound to a different run request")
		}
		return s.beginResult(ctx, existing), nil
	} else if !errors.Is(getErr, reportstore.ErrNotFound) {
		return nil, getErr
	}
	if err := s.store.CreateReportRun(ctx, candidate); err != nil {
		if !errors.Is(err, reportstore.ErrAlreadyExists) {
			return nil, translateStoreError(err)
		}
		existing, getErr := s.store.GetReportRunByRequestID(ctx, requestID)
		if getErr != nil {
			return nil, translateStoreError(getErr)
		}
		if !sameBeginIdentity(existing, candidate) {
			return nil, conflict("uiRunRequestId is already bound to a different run request")
		}
		return s.beginResult(ctx, existing), nil
	}
	return s.beginResult(ctx, candidate), nil
}

func (s *Service) beginResult(ctx context.Context, run *reportrun.Record) *BeginResult {
	result := &BeginResult{Run: cloneRun(run)}
	if run != nil && strings.TrimSpace(run.ConversationID) != "" {
		if current, err := s.store.GetConversationReportContext(ctx, run.ConversationID); err == nil {
			result.Context = cloneContext(current)
		}
	}
	return result
}

func (s *Service) Complete(ctx context.Context, input *CompleteInput) (*reportrun.Record, error) {
	if input == nil || strings.TrimSpace(input.ReportRunID) == "" {
		return nil, invalid("reportRunId is required")
	}
	spec, err := requiredJSON(input.ReportSpec, "reportSpec")
	if err != nil {
		return nil, err
	}
	fill, err := requiredJSON(input.ReportFill, "reportFill")
	if err != nil {
		return nil, err
	}
	printPayload, err := requiredJSON(input.ReportPrint, "reportPrint")
	if err != nil {
		return nil, err
	}
	current, err := s.getScopedRun(ctx, input.ReportRunID, input.ConversationID)
	if err != nil {
		return nil, err
	}
	switch current.Status {
	case reportrun.StatusCompleted:
		if jsonEqual(current.ReportSpec, spec) && jsonEqual(current.ReportFill, fill) && jsonEqual(current.ReportPrint, printPayload) {
			return cloneRun(current), nil
		}
		return nil, conflict("completed report snapshot is immutable")
	case reportrun.StatusFailed:
		return nil, conflict("failed report run cannot be completed")
	case reportrun.StatusRunning:
	default:
		return nil, conflict("report run is not running")
	}
	if input.ExpectedRevision != current.Revision {
		return nil, ErrCAS
	}
	next := cloneRun(current)
	now := s.now().UTC()
	next.Status = reportrun.StatusCompleted
	next.ReportSpec = spec
	next.ReportFill = fill
	next.ReportPrint = printPayload
	next.FailureCode = ""
	next.FailureText = ""
	next.CompletedAt = &now
	next.UpdatedAt = now
	next.Revision++
	if err := s.store.UpdateReportRunCAS(ctx, next, current.Revision); err != nil {
		if errors.Is(err, reportstore.ErrCASMismatch) {
			reloaded, getErr := s.getScopedRun(ctx, input.ReportRunID, input.ConversationID)
			if getErr == nil && reloaded.Status == reportrun.StatusCompleted &&
				jsonEqual(reloaded.ReportSpec, spec) && jsonEqual(reloaded.ReportFill, fill) && jsonEqual(reloaded.ReportPrint, printPayload) {
				return cloneRun(reloaded), nil
			}
		}
		return nil, translateStoreError(err)
	}
	return cloneRun(next), nil
}

func (s *Service) Fail(ctx context.Context, input *FailInput) (*reportrun.Record, error) {
	if input == nil || strings.TrimSpace(input.ReportRunID) == "" {
		return nil, invalid("reportRunId is required")
	}
	current, err := s.getScopedRun(ctx, input.ReportRunID, input.ConversationID)
	if err != nil {
		return nil, err
	}
	code := strings.TrimSpace(input.FailureCode)
	text := strings.TrimSpace(input.FailureText)
	switch current.Status {
	case reportrun.StatusCompleted:
		return nil, conflict("completed report run cannot fail")
	case reportrun.StatusFailed:
		if current.FailureCode == code && current.FailureText == text {
			return cloneRun(current), nil
		}
		return nil, conflict("failed report run is terminal")
	case reportrun.StatusRunning:
	default:
		return nil, conflict("report run is not running")
	}
	if input.ExpectedRevision != current.Revision {
		return nil, ErrCAS
	}
	next := cloneRun(current)
	now := s.now().UTC()
	next.Status = reportrun.StatusFailed
	next.FailureCode = code
	next.FailureText = text
	next.CompletedAt = &now
	next.UpdatedAt = now
	next.Revision++
	if err := s.store.UpdateReportRunCAS(ctx, next, current.Revision); err != nil {
		return nil, translateStoreError(err)
	}
	return cloneRun(next), nil
}

func (s *Service) Activate(ctx context.Context, input *ActivateInput) (*reportcontext.Record, error) {
	if input == nil || strings.TrimSpace(input.ReportRunID) == "" || strings.TrimSpace(input.ConversationID) == "" {
		return nil, invalid("reportRunId and conversationId are required")
	}
	run, err := s.getScopedRun(ctx, input.ReportRunID, input.ConversationID)
	if err != nil {
		return nil, err
	}
	if run.Status != reportrun.StatusCompleted {
		return nil, conflict("only a completed report run can be activated")
	}
	if run.Revision != input.ExpectedRunRevision {
		return nil, ErrCAS
	}
	ownerID, err := authenticatedOwner(ctx)
	if err != nil {
		return nil, err
	}
	current, err := s.store.GetConversationReportContext(ctx, input.ConversationID)
	if err == nil && strings.TrimSpace(current.ActiveReportRunID) == strings.TrimSpace(run.ReportRunID) {
		return cloneContext(current), nil
	}
	if err != nil && !errors.Is(err, reportstore.ErrNotFound) {
		return nil, translateStoreError(err)
	}
	actualRevision := int64(0)
	if current != nil {
		actualRevision = current.Revision
	}
	if input.ExpectedContextRevision != actualRevision {
		return nil, ErrCAS
	}
	next := &reportcontext.Record{
		OwnerID:           ownerID,
		ConversationID:    strings.TrimSpace(input.ConversationID),
		ActiveReportRunID: strings.TrimSpace(run.ReportRunID),
		Revision:          actualRevision + 1,
		ActivationSource:  normalizeSource(input.Source, "manual"),
		ActorID:           ownerID,
		UpdatedAt:         s.now().UTC(),
	}
	if err := s.store.PutConversationReportContextCAS(ctx, next, actualRevision); err != nil {
		return nil, translateStoreError(err)
	}
	return cloneContext(next), nil
}

func (s *Service) Adopt(ctx context.Context, input *AdoptInput) (*AdoptionResult, error) {
	if input == nil || strings.TrimSpace(input.ReportRunID) == "" || strings.TrimSpace(input.ConversationID) == "" {
		return nil, invalid("an exact reportRunId and conversationId are required")
	}
	ownerID, err := authenticatedOwner(ctx)
	if err != nil {
		return nil, err
	}
	current, err := s.store.GetReportRun(ctx, strings.TrimSpace(input.ReportRunID))
	if err != nil {
		return nil, translateStoreError(err)
	}
	if current.Status != reportrun.StatusCompleted {
		return nil, conflict("only a completed manual report run can be adopted")
	}
	if current.Origin != "manual" {
		return nil, conflict("only a completed manual report run can be adopted")
	}
	targetConversation := strings.TrimSpace(input.ConversationID)
	if current.ConversationID != "" && current.ConversationID != targetConversation {
		return nil, ErrNotFound
	}
	if current.ConversationID == targetConversation {
		reportCtx, contextErr := s.store.GetConversationReportContext(ctx, targetConversation)
		if contextErr == nil && strings.TrimSpace(reportCtx.ActiveReportRunID) == strings.TrimSpace(current.ReportRunID) {
			return &AdoptionResult{Run: cloneRun(current), Context: cloneContext(reportCtx)}, nil
		}
		if contextErr != nil && !errors.Is(contextErr, reportstore.ErrNotFound) {
			return nil, translateStoreError(contextErr)
		}
		return nil, conflict("adopted report run is missing its active conversation pointer")
	}
	if current.Revision != input.ExpectedRunRevision {
		return nil, ErrCAS
	}
	currentContext, contextErr := s.store.GetConversationReportContext(ctx, targetConversation)
	if contextErr != nil && !errors.Is(contextErr, reportstore.ErrNotFound) {
		return nil, translateStoreError(contextErr)
	}
	actualContextRevision := int64(0)
	if currentContext != nil {
		actualContextRevision = currentContext.Revision
	}
	if actualContextRevision != input.ExpectedContextRevision {
		return nil, ErrCAS
	}
	next := cloneRun(current)
	now := s.now().UTC()
	next.ConversationID = targetConversation
	next.AdoptionSource = normalizeSource(input.Source, "adopt")
	next.ActorID = ownerID
	next.UpdatedAt = now
	next.Revision++
	reportCtx := &reportcontext.Record{
		OwnerID:           ownerID,
		ConversationID:    targetConversation,
		ActiveReportRunID: next.ReportRunID,
		Revision:          actualContextRevision + 1,
		ActivationSource:  normalizeSource(input.Source, "adopt"),
		ActorID:           ownerID,
		UpdatedAt:         now,
	}
	if err := s.store.AdoptReportRunAndContextCAS(ctx, next, current.Revision, reportCtx, actualContextRevision); err != nil {
		if errors.Is(err, reportstore.ErrCASMismatch) {
			reloadedRun, runErr := s.store.GetReportRun(ctx, next.ReportRunID)
			reloadedContext, contextErr := s.store.GetConversationReportContext(ctx, targetConversation)
			if runErr == nil && contextErr == nil &&
				strings.TrimSpace(reloadedRun.ConversationID) == targetConversation &&
				strings.TrimSpace(reloadedContext.ActiveReportRunID) == strings.TrimSpace(reloadedRun.ReportRunID) {
				return &AdoptionResult{Run: cloneRun(reloadedRun), Context: cloneContext(reloadedContext)}, nil
			}
		}
		return nil, translateStoreError(err)
	}
	return &AdoptionResult{Run: cloneRun(next), Context: cloneContext(reportCtx)}, nil
}

func (s *Service) GetRun(ctx context.Context, reportRunID, conversationID string) (*reportrun.Record, error) {
	return s.getScopedRun(ctx, reportRunID, conversationID)
}

// WaitTerminal waits for one exact durable report run to reach completed or
// failed. Identity never falls back to a latest run, time window, active
// pointer, or UI window: every read remains scoped by the authenticated owner,
// trusted conversation, and exact reportRunId.
func (s *Service) WaitTerminal(ctx context.Context, reportRunID, conversationID string) (*reportrun.Record, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reportRunID = strings.TrimSpace(reportRunID)
	conversationID = strings.TrimSpace(conversationID)
	if reportRunID == "" {
		return nil, invalid("reportRunId is required")
	}
	if conversationID == "" {
		return nil, invalid("conversationId is required")
	}

	waitCtx, cancel := context.WithTimeout(ctx, waitTerminalBudget)
	defer cancel()
	for attempt := 0; ; attempt++ {
		if err := waitCtx.Err(); err != nil {
			return nil, err
		}
		run, err := s.getScopedRun(waitCtx, reportRunID, conversationID)
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(strings.TrimSpace(run.Status)) {
		case reportrun.StatusCompleted, reportrun.StatusFailed:
			return cloneRun(run), nil
		case reportrun.StatusRunning:
		default:
			return nil, conflict("report run has unsupported status " + strings.TrimSpace(run.Status))
		}

		timer := time.NewTimer(waitTerminalPollDelay(attempt))
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, waitCtx.Err()
		case <-timer.C:
		}
	}
}

func waitTerminalPollDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(waitTerminalBackoff) {
		return waitTerminalBackoff[len(waitTerminalBackoff)-1]
	}
	return waitTerminalBackoff[attempt]
}

func (s *Service) GetContext(ctx context.Context, conversationID string) (*reportcontext.Record, error) {
	if _, err := authenticatedOwner(ctx); err != nil {
		return nil, err
	}
	record, err := s.store.GetConversationReportContext(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return nil, translateStoreError(err)
	}
	run, err := s.store.GetReportRun(ctx, record.ActiveReportRunID)
	if err != nil || run == nil || strings.TrimSpace(run.ConversationID) != strings.TrimSpace(conversationID) ||
		strings.TrimSpace(run.OwnerID) != strings.TrimSpace(record.OwnerID) || run.Status != reportrun.StatusCompleted {
		return nil, ErrNotFound
	}
	return cloneContext(record), nil
}

// ValidateDurableUIEvent validates the two T1 browser lifecycle events against
// the persisted run. Other report event kinds deliberately remain outside the
// durable T1 contract.
func (s *Service) ValidateDurableUIEvent(ctx context.Context, conversationID, kind string, detail map[string]interface{}) error {
	kind = strings.TrimSpace(kind)
	if kind != "report.run_start" && kind != "report.run" {
		return nil
	}
	ownerID, err := authenticatedOwner(ctx)
	if err != nil {
		return err
	}
	reportRunID := detailString(detail, "reportRunId")
	if reportRunID == "" {
		return invalid("reportRunId is required for durable report lifecycle events")
	}
	revision, ok := detailRevision(detail)
	if !ok || revision <= 0 {
		return invalid("revision is required for durable report lifecycle events")
	}
	status := strings.ToLower(detailString(detail, "status"))
	if status == "" {
		return invalid("status is required for durable report lifecycle events")
	}
	run, err := s.store.GetReportRun(ctx, reportRunID)
	if err != nil {
		return translateStoreError(err)
	}
	if strings.TrimSpace(run.OwnerID) != ownerID ||
		strings.TrimSpace(run.ConversationID) == "" ||
		strings.TrimSpace(run.ConversationID) != strings.TrimSpace(conversationID) {
		return ErrNotFound
	}
	if revision != run.Revision {
		return ErrCAS
	}
	switch kind {
	case "report.run_start":
		if status != reportrun.StatusRunning || run.Status != reportrun.StatusRunning || run.CompletedAt != nil {
			return conflict("report.run_start does not match the persisted run lifecycle")
		}
	case "report.run":
		if status != run.Status || run.CompletedAt == nil ||
			(run.Status != reportrun.StatusCompleted && run.Status != reportrun.StatusFailed) {
			return conflict("report.run does not match the persisted run lifecycle")
		}
		if run.Status == reportrun.StatusCompleted &&
			(len(bytes.TrimSpace(run.ReportSpec)) == 0 || len(bytes.TrimSpace(run.ReportFill)) == 0 || len(bytes.TrimSpace(run.ReportPrint)) == 0) {
			return conflict("completed report run is missing its durable snapshot")
		}
	}
	return nil
}

func (s *Service) getScopedRun(ctx context.Context, reportRunID, conversationID string) (*reportrun.Record, error) {
	ownerID, err := authenticatedOwner(ctx)
	if err != nil {
		return nil, err
	}
	run, err := s.store.GetReportRun(ctx, strings.TrimSpace(reportRunID))
	if err != nil {
		return nil, translateStoreError(err)
	}
	if run == nil || strings.TrimSpace(run.OwnerID) != ownerID {
		return nil, ErrNotFound
	}
	expectedConversation := strings.TrimSpace(conversationID)
	actualConversation := strings.TrimSpace(run.ConversationID)
	if actualConversation != expectedConversation {
		return nil, ErrNotFound
	}
	return run, nil
}

func detailString(detail map[string]interface{}, key string) string {
	if detail == nil {
		return ""
	}
	value, _ := detail[key].(string)
	return strings.TrimSpace(value)
}

func detailRevision(detail map[string]interface{}) (int64, bool) {
	if detail == nil {
		return 0, false
	}
	switch value := detail["revision"].(type) {
	case int:
		return int64(value), true
	case int32:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		converted := int64(value)
		return converted, float64(converted) == value
	case json.Number:
		converted, err := value.Int64()
		return converted, err == nil
	default:
		return 0, false
	}
}

func authenticatedOwner(ctx context.Context) (string, error) {
	ownerID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if ownerID == "" {
		return "", ErrNotFound
	}
	return ownerID, nil
}

func normalizeOptionalJSON(value []byte, field string) ([]byte, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		return nil, nil
	}
	if !json.Valid(value) {
		return nil, invalid(field + " must be valid JSON")
	}
	return append([]byte(nil), value...), nil
}

func requiredJSON(value []byte, field string) ([]byte, error) {
	normalized, err := normalizeOptionalJSON(value, field)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 || bytes.Equal(bytes.TrimSpace(normalized), []byte("null")) {
		return nil, invalid(field + " is required")
	}
	return normalized, nil
}

func jsonEqual(left, right []byte) bool {
	if bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right)) {
		return true
	}
	var a, b interface{}
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

func sameBeginIdentity(left, right *reportrun.Record) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.TrimSpace(left.OwnerID) == strings.TrimSpace(right.OwnerID) &&
		strings.TrimSpace(left.ConversationID) == strings.TrimSpace(right.ConversationID) &&
		strings.TrimSpace(left.Materializer) == strings.TrimSpace(right.Materializer) &&
		strings.TrimSpace(left.Origin) == strings.TrimSpace(right.Origin) &&
		strings.TrimSpace(left.BuilderRef) == strings.TrimSpace(right.BuilderRef) &&
		strings.TrimSpace(left.PresetID) == strings.TrimSpace(right.PresetID) &&
		strings.TrimSpace(left.SourceKind) == strings.TrimSpace(right.SourceKind) &&
		strings.TrimSpace(left.SourceID) == strings.TrimSpace(right.SourceID) &&
		jsonEqual(left.RequestedParams, right.RequestedParams) &&
		jsonEqual(left.EffectiveParams, right.EffectiveParams)
}

func cloneRun(input *reportrun.Record) *reportrun.Record {
	if input == nil {
		return nil
	}
	out := *input
	out.RequestedParams = append([]byte(nil), input.RequestedParams...)
	out.EffectiveParams = append([]byte(nil), input.EffectiveParams...)
	out.ReportSpec = append([]byte(nil), input.ReportSpec...)
	out.ReportFill = append([]byte(nil), input.ReportFill...)
	out.ReportPrint = append([]byte(nil), input.ReportPrint...)
	if input.CompletedAt != nil {
		value := *input.CompletedAt
		out.CompletedAt = &value
	}
	return &out
}

func cloneContext(input *reportcontext.Record) *reportcontext.Record {
	if input == nil {
		return nil
	}
	out := *input
	return &out
}

func normalizeSource(value, fallback string) string {
	if normalized := strings.ToLower(strings.TrimSpace(value)); normalized != "" {
		return normalized
	}
	return fallback
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, message)
}

func conflict(message string) error {
	return fmt.Errorf("%w: %s", ErrConflict, message)
}

func translateStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, reportstore.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, reportstore.ErrCASMismatch):
		return ErrCAS
	case errors.Is(err, reportstore.ErrAlreadyExists):
		return ErrConflict
	case errors.Is(err, reportstore.ErrImmutable):
		return ErrConflict
	default:
		return err
	}
}
