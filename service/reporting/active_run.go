package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// ActiveReportRunResolver is the narrow, read-only reportingrun.Service
// surface used by the model-facing active-run resolver.
type ActiveReportRunResolver interface {
	GetContext(ctx context.Context, conversationID string) (*reportcontext.Record, error)
	GetRun(ctx context.Context, reportRunID, conversationID string) (*reportrun.Record, error)
}

// GetActiveReportRunInput is deliberately empty. Owner and conversation
// identity come exclusively from authenticated and trusted runtime context.
type GetActiveReportRunInput struct{}

// UnmarshalJSON rejects every model-supplied field. The local MCP registry
// skips decoding for its normal empty argument map, while every non-empty map
// is decoded here and must fail closed.
func (*GetActiveReportRunInput) UnmarshalJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("reporting active run input must be an empty JSON object: %w", err)
	}
	if object == nil || len(object) != 0 {
		return fmt.Errorf("reporting active run input must be exactly an empty JSON object")
	}
	return nil
}

// ActiveReportRun is the sanitized model-facing view of a durable report run.
// It deliberately omits owner identity and all materialized report snapshots.
type ActiveReportRun struct {
	ReportRunID     string                     `json:"reportRunId"`
	Revision        int64                      `json:"revision"`
	Status          string                     `json:"status"`
	ArtifactRef     string                     `json:"artifactRef"`
	Origin          string                     `json:"origin"`
	BuilderRef      string                     `json:"builderRef,omitempty"`
	PresetID        string                     `json:"presetId,omitempty"`
	SourceKind      string                     `json:"sourceKind,omitempty"`
	SourceID        string                     `json:"sourceId,omitempty"`
	RequestedParams map[string]json.RawMessage `json:"requestedParams"`
	EffectiveParams map[string]json.RawMessage `json:"effectiveParams"`
	CompletedAt     string                     `json:"completedAt"`
}

// GetActiveReportRun resolves the active pointer first and then reads that
// exact run through reportingrun.Service. Every invalid or unavailable state
// is intentionally collapsed to ErrNotFound.
func (s *Service) GetActiveReportRun(ctx context.Context) (*ActiveReportRun, error) {
	if s == nil || s.activeRunResolver == nil || ctx == nil {
		return nil, ErrNotFound
	}
	ownerID := effectiveActorID(ctx)
	if ownerID == "" {
		return nil, ErrNotFound
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	if conversationID == "" {
		return nil, ErrNotFound
	}
	reportCtx, err := s.activeRunResolver.GetContext(ctx, conversationID)
	if err != nil || reportCtx == nil {
		return nil, ErrNotFound
	}
	reportRunID := strings.TrimSpace(reportCtx.ActiveReportRunID)
	if reportRunID == "" || reportCtx.OwnerID != ownerID || strings.TrimSpace(reportCtx.ConversationID) != conversationID {
		return nil, ErrNotFound
	}
	run, err := s.activeRunResolver.GetRun(ctx, reportRunID, conversationID)
	if err != nil || run == nil ||
		run.OwnerID != ownerID ||
		strings.TrimSpace(run.ReportRunID) != reportRunID ||
		strings.TrimSpace(run.ConversationID) != conversationID ||
		strings.ToLower(strings.TrimSpace(run.Status)) != reportrun.StatusCompleted ||
		strings.ToLower(strings.TrimSpace(run.Origin)) != "prompt" ||
		strings.TrimSpace(run.AdoptionSource) != "" ||
		run.CompletedAt == nil || run.CompletedAt.IsZero() {
		return nil, ErrNotFound
	}
	requestedParams, ok := sanitizedParams(run.RequestedParams)
	if !ok {
		return nil, ErrNotFound
	}
	effectiveParams, ok := sanitizedParams(run.EffectiveParams)
	if !ok {
		return nil, ErrNotFound
	}
	return &ActiveReportRun{
		ReportRunID:     reportRunID,
		Revision:        run.Revision,
		Status:          reportrun.StatusCompleted,
		ArtifactRef:     "report-run://" + reportRunID,
		Origin:          "prompt",
		BuilderRef:      strings.TrimSpace(run.BuilderRef),
		PresetID:        strings.TrimSpace(run.PresetID),
		SourceKind:      strings.TrimSpace(run.SourceKind),
		SourceID:        strings.TrimSpace(run.SourceID),
		RequestedParams: requestedParams,
		EffectiveParams: effectiveParams,
		CompletedAt:     run.CompletedAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func normalizeActiveReportRunResolver(resolver ActiveReportRunResolver) ActiveReportRunResolver {
	if resolver == nil {
		return nil
	}
	value := reflect.ValueOf(resolver)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		if value.IsNil() {
			return nil
		}
	}
	return resolver
}

func (s *Service) getActiveReportRunTool(ctx context.Context, in, out interface{}) error {
	if _, ok := in.(*GetActiveReportRunInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ActiveReportRun)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	result, err := s.GetActiveReportRun(ctx)
	if err != nil {
		return ErrNotFound
	}
	*output = *result
	return nil
}

func sanitizedParams(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]json.RawMessage{}, true
	}
	result := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &result); err != nil || result == nil {
		return nil, false
	}
	return result, true
}
