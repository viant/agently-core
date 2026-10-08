package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/forge/backend/reporting/registry"
)

const exportResourceMetadataKey = "__agentlyCoreExportResourcePinV1"

type persistedExportMetadata struct {
	DependencyPins  map[string]identity.ResolvedResource `json:"dependencyPins,omitempty"`
	DependencyToken string                               `json:"dependencyToken,omitempty"`
	Version         int                                  `json:"version"`
	ResourcePin     *identity.ResolvedResource           `json:"resourcePin"`
	Payload         json.RawMessage                      `json:"payload,omitempty"`
}

func hasReservedExportMetadata(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return false
	}
	_, found := object[exportResourceMetadataKey]
	return found
}

func encodeExportJobMetadata(payload json.RawMessage, pin *identity.ResolvedResource, dependencies ...*ExportJob) json.RawMessage {
	if pin == nil {
		return cloneJSON(payload)
	}
	envelope := persistedExportMetadata{Version: 1, ResourcePin: cloneResolvedResource(pin), Payload: cloneJSON(payload)}
	if len(dependencies) > 0 && dependencies[0] != nil && len(dependencies[0].DependencyPins) > 0 {
		envelope.Version = 2
		envelope.DependencyPins = cloneDependencyPins(dependencies[0].DependencyPins)
		envelope.DependencyToken = dependencies[0].DependencyToken
	}
	encoded, err := json.Marshal(map[string]any{exportResourceMetadataKey: envelope})
	if err != nil {
		return nil
	}
	return encoded
}

func cloneJSONMap(value map[string]interface{}) map[string]interface{} {
	if len(value) == 0 {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		result := make(map[string]interface{}, len(value))
		for key, item := range value {
			result[key] = item
		}
		return result
	}
	var result map[string]interface{}
	if json.Unmarshal(encoded, &result) != nil {
		return nil
	}
	return result
}

func decodeExportJobMetadata(raw json.RawMessage) (json.RawMessage, *identity.ResolvedResource, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return cloneJSON(raw), nil, nil
	}
	wrapped, found := object[exportResourceMetadataKey]
	if !found {
		return cloneJSON(raw), nil, nil
	}
	var envelope persistedExportMetadata
	if json.Unmarshal(wrapped, &envelope) != nil || (envelope.Version != 1 && envelope.Version != 2) || envelope.ResourcePin == nil || envelope.Version == 1 && (len(envelope.DependencyPins) > 0 || envelope.DependencyToken != "") || envelope.Version == 2 && (len(envelope.DependencyPins) == 0 || envelope.DependencyToken == "") {
		return nil, nil, fmt.Errorf("invalid persisted export resource pin")
	}
	return cloneJSON(envelope.Payload), cloneResolvedResource(envelope.ResourcePin), nil
}
func decodeExportDependencies(raw json.RawMessage) (map[string]identity.ResolvedResource, string) {
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	var envelope persistedExportMetadata
	_ = json.Unmarshal(object[exportResourceMetadataKey], &envelope)
	return cloneDependencyPins(envelope.DependencyPins), envelope.DependencyToken
}

// validateExportResourcePin rechecks a stored export against its exact original
// revision. It never resolves an omitted selector or substitutes a new pin.
func (s *Service) validateExportResourcePin(ctx context.Context, job *ExportJob, action string) error {
	if s == nil || s.resourceResolver == nil {
		return nil
	}
	if ctx == nil || ctx.Err() != nil || job == nil || job.ResourcePin == nil {
		return identity.ErrResourceDenied
	}
	pin := job.ResourcePin
	uri, err := identity.ParseResourceURI(pin.URI)
	if err != nil || uri.Kind != "report" || strings.TrimSpace(job.ArtifactRef) != pin.URI || !exportPinLeaseValid(pin, s.now()) {
		return identity.ErrResourceDenied
	}
	resolver, err := s.resourceResolver(ctx, action)
	if err != nil {
		return err
	}
	if resolver == nil {
		return identity.ErrResourceDenied
	}
	raw, fresh, err := resolver.ReadResolved(ctx, *pin)
	if err != nil {
		return err
	}
	if fresh == nil || fresh.URI != pin.URI || fresh.ResourceCandidate != pin.ResourceCandidate || fresh.AuthorityBinding != pin.AuthorityBinding || !fresh.ValidUntil.Equal(pin.ValidUntil) {
		return identity.ErrResourceDenied
	}
	var definition registry.ReportEnvelope
	if json.Unmarshal(raw, &definition) != nil {
		return identity.ErrResourceDenied
	}
	return s.verifyDependencies(ctx, *pin, definition, job.DependencyPins, job.DependencyToken)
}

func isUnifiedExportService(s *Service) bool { return s != nil && s.resourceResolver != nil }

func validateUnifiedExportInput(request *SubmitExportRequest) error {
	if request == nil {
		return fmt.Errorf("reporting export: request is required")
	}
	if strings.TrimSpace(request.ReportRunID) != "" {
		return identity.ErrResourceDenied
	}
	if hasReservedExportMetadata(request.Metadata) {
		return identity.ErrResourceDenied
	}
	return nil
}

func (s *Service) prepareUnifiedExportRequest(ctx context.Context, request *SubmitExportRequest) (*SubmitExportRequest, *identity.ResolvedResource, error) {
	if err := validateUnifiedExportInput(request); err != nil {
		return nil, nil, err
	}
	if request.execution != nil && (strings.TrimSpace(request.ArtifactRef) != "" || request.Scope != "" || strings.TrimSpace(request.ConversationID) != "" || strings.TrimSpace(request.WorkspaceID) != "" || request.Source != nil || request.ReportExportRequest != nil || len(bytes.TrimSpace(request.ReportSpec)) != 0 || len(bytes.TrimSpace(request.ReportFill)) != 0 || len(bytes.TrimSpace(request.ReportPrint)) != 0 || len(bytes.TrimSpace(request.Metadata)) != 0 || len(request.Parameters) != 0) {
		return nil, nil, identity.ErrResourceDenied
	}
	if request.execution == nil && (request.Resource == nil && request.ResolvedResource == nil) {
		return nil, nil, identity.ErrResourceDenied
	}
	if request.execution == nil && (strings.TrimSpace(request.ArtifactRef) != "" || request.Scope != "" || strings.TrimSpace(request.ConversationID) != "" || strings.TrimSpace(request.WorkspaceID) != "" || request.Source != nil || request.ReportExportRequest != nil || len(bytes.TrimSpace(request.ReportSpec)) != 0 || len(bytes.TrimSpace(request.ReportFill)) != 0 || len(bytes.TrimSpace(request.ReportPrint)) != 0 || len(bytes.TrimSpace(request.Metadata)) != 0) {
		return nil, nil, identity.ErrResourceDenied
	}
	if request.execution != nil {
		if request.execution.Resource == nil || request.ResolvedResource != nil && !sameResourcePin(request.ResolvedResource, request.execution.Resource) {
			return nil, nil, identity.ErrResourceDenied
		}
		if request.Resource != nil && (request.Resource.URI != request.execution.Resource.URI || request.Resource.Revision != request.execution.Resource.Selector()) {
			return nil, nil, identity.ErrResourceDenied
		}
	}
	execution := request.execution
	if execution == nil {
		var err error
		execution, err = s.ExecuteResource(ctx, &ExecuteResourceRequest{
			DependencyPins: cloneDependencyPins(request.DependencyPins), DependencyToken: request.DependencyToken,
			Resource: cloneResourceRef(request.Resource), ResolvedResource: cloneResolvedResource(request.ResolvedResource),
			Parameters: cloneJSONMap(request.Parameters),
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if execution == nil || execution.Resource == nil || !exportPinLeaseValid(execution.Resource, s.now()) {
		return nil, nil, identity.ErrResourceDenied
	}
	uri, err := identity.ParseResourceURI(execution.Resource.URI)
	if err != nil || uri.Kind != "report" {
		return nil, nil, identity.ErrResourceDenied
	}
	format := request.Format
	if format == "" {
		format = ExportFormatPDF
	}
	conversationID := strings.TrimSpace(requestctx.ConversationIDFromContext(ctx))
	prepared := &SubmitExportRequest{
		DependencyPins: cloneDependencyPins(execution.DependencyPins), DependencyToken: execution.DependencyToken,
		ArtifactRef: execution.Resource.URI, Format: format, Scope: ExportScopeDraft,
		ConversationID: conversationID, ReportSpec: cloneJSON(execution.ReportSpec),
		ReportFill: cloneJSON(execution.ReportFill), ReportPrint: cloneJSON(execution.ReportPrint),
		ResolvedResource: cloneResolvedResource(execution.Resource),
	}
	if err := validateSubmitExportRequest(prepared); err != nil {
		return nil, nil, err
	}
	return prepared, cloneResolvedResource(execution.Resource), nil
}

func sameResourcePin(a, b *identity.ResolvedResource) bool {
	return a != nil && b != nil && a.URI == b.URI && a.ResourceCandidate == b.ResourceCandidate && a.AuthorityBinding == b.AuthorityBinding && a.ValidUntil.Equal(b.ValidUntil)
}

func exportPinLeaseValid(pin *identity.ResolvedResource, now time.Time) bool {
	return pin != nil && pin.ResourceCandidate.Valid() && pin.AuthorityBinding != "" && pin.ValidUntil.After(now)
}
