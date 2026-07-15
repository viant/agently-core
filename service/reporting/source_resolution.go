package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func cloneExportSource(input *ExportSource) *ExportSource {
	if input == nil {
		return nil
	}
	return &ExportSource{
		Kind:        strings.TrimSpace(input.Kind),
		ArtifactID:  strings.TrimSpace(input.ArtifactID),
		ArtifactRef: strings.TrimSpace(input.ArtifactRef),
		ReportID:    strings.TrimSpace(input.ReportID),
		WindowKey:   strings.TrimSpace(input.WindowKey),
		PresetID:    strings.TrimSpace(input.PresetID),
		ReportSpec:  cloneJSON(input.ReportSpec),
		ReportFill:  cloneJSON(input.ReportFill),
		ReportPrint: cloneJSON(input.ReportPrint),
		Metadata:    cloneJSON(input.Metadata),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func mergeJSONObjectBytes(base, overlay []byte) []byte {
	if len(bytes.TrimSpace(base)) == 0 {
		return cloneJSON(overlay)
	}
	if len(bytes.TrimSpace(overlay)) == 0 {
		return cloneJSON(base)
	}
	var baseMap map[string]any
	if err := json.Unmarshal(base, &baseMap); err != nil {
		return cloneJSON(overlay)
	}
	var overlayMap map[string]any
	if err := json.Unmarshal(overlay, &overlayMap); err != nil {
		return cloneJSON(base)
	}
	for key, value := range overlayMap {
		baseMap[key] = value
	}
	merged, err := json.Marshal(baseMap)
	if err != nil {
		return cloneJSON(overlay)
	}
	return merged
}

func buildExportContextMetadata(conversationID, workspaceID string, base, overlay []byte) []byte {
	merged := mergeJSONObjectBytes(base, overlay)
	if strings.TrimSpace(conversationID) == "" && strings.TrimSpace(workspaceID) == "" {
		return merged
	}
	payload := map[string]any{}
	if len(bytes.TrimSpace(merged)) > 0 {
		_ = json.Unmarshal(merged, &payload)
	}
	if strings.TrimSpace(conversationID) != "" {
		payload["conversationId"] = strings.TrimSpace(conversationID)
	}
	if strings.TrimSpace(workspaceID) != "" {
		payload["workspaceId"] = strings.TrimSpace(workspaceID)
	}
	if len(payload) == 0 {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return merged
	}
	return data
}

func buildPresetSourceMetadata(conversationID, workspaceID string, source *ExportSource, overlay []byte) []byte {
	base := cloneJSON(overlay)
	payload := map[string]any{}
	if len(bytes.TrimSpace(base)) > 0 {
		_ = json.Unmarshal(base, &payload)
	}
	if source != nil {
		payload["sourceKind"] = "preset"
		if value := strings.TrimSpace(source.WindowKey); value != "" {
			payload["windowKey"] = value
		}
		if value := strings.TrimSpace(source.PresetID); value != "" {
			payload["presetId"] = value
		}
	}
	if len(payload) == 0 {
		return buildExportContextMetadata(conversationID, workspaceID, nil, nil)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return buildExportContextMetadata(conversationID, workspaceID, nil, overlay)
	}
	return buildExportContextMetadata(conversationID, workspaceID, data, nil)
}

func buildPresetArtifactRef(source *ExportSource, newID func() string) string {
	if source == nil {
		return "report://preset/" + strings.TrimSpace(newID())
	}
	windowKey := strings.TrimSpace(source.WindowKey)
	presetID := strings.TrimSpace(source.PresetID)
	switch {
	case windowKey != "" && presetID != "":
		return "report://preset/" + windowKey + "/" + presetID
	case presetID != "":
		return "report://preset/" + presetID
	case windowKey != "":
		return "report://preset/" + windowKey + "/" + strings.TrimSpace(newID())
	default:
		return "report://preset/" + strings.TrimSpace(newID())
	}
}

func (s *Service) resolveSubmitExportRequest(ctx context.Context, request *SubmitExportRequest) (*SubmitExportRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting export: request is required")
	}
	if request.Source == nil {
		return normalizeSubmitExportRequest(request)
	}
	if request.ReportExportRequest != nil {
		return nil, fmt.Errorf("reporting export: source and reportExportRequest are mutually exclusive")
	}
	next := cloneSubmitExportRequest(request)
	source := cloneExportSource(request.Source)
	next.Source = nil
	switch strings.ToLower(strings.TrimSpace(source.Kind)) {
	case "report":
		report, err := s.GetReport(ctx, &GetReportInput{
			ArtifactID:  strings.TrimSpace(source.ArtifactID),
			ArtifactRef: strings.TrimSpace(source.ArtifactRef),
			ReportID:    strings.TrimSpace(source.ReportID),
		})
		if err != nil {
			return nil, err
		}
		if report == nil {
			return nil, ErrNotFound
		}
		next.ArtifactRef = firstNonEmpty(next.ArtifactRef, report.ArtifactRef, source.ArtifactRef)
		if next.Scope == "" {
			next.Scope = ExportScopeSavedPayload
		}
		if len(bytes.TrimSpace(next.ReportSpec)) == 0 {
			next.ReportSpec = cloneJSON(report.ReportSpec)
		}
		if len(bytes.TrimSpace(next.ReportFill)) == 0 {
			next.ReportFill = cloneJSON(report.ReportFill)
		}
		if len(bytes.TrimSpace(next.ReportPrint)) == 0 {
			next.ReportPrint = cloneJSON(report.ReportPrint)
		}
		next.Metadata = buildExportContextMetadata(next.ConversationID, next.WorkspaceID, report.Metadata, next.Metadata)
	case "inline":
		next.ArtifactRef = firstNonEmpty(next.ArtifactRef, source.ArtifactRef)
		if next.ArtifactRef == "" {
			next.ArtifactRef = "report://inline/" + strings.TrimSpace(s.newID())
		}
		if next.Scope == "" {
			next.Scope = ExportScopeDraft
		}
		if len(bytes.TrimSpace(next.ReportSpec)) == 0 {
			next.ReportSpec = cloneJSON(source.ReportSpec)
		}
		if len(bytes.TrimSpace(next.ReportFill)) == 0 {
			next.ReportFill = cloneJSON(source.ReportFill)
		}
		if len(bytes.TrimSpace(next.ReportPrint)) == 0 {
			next.ReportPrint = cloneJSON(source.ReportPrint)
		}
		next.Metadata = buildExportContextMetadata(next.ConversationID, next.WorkspaceID, source.Metadata, next.Metadata)
	case "preset":
		next.ArtifactRef = firstNonEmpty(next.ArtifactRef, source.ArtifactRef)
		if next.ArtifactRef == "" {
			next.ArtifactRef = buildPresetArtifactRef(source, s.newID)
		}
		if next.Scope == "" {
			next.Scope = ExportScopeDraft
		}
		if len(bytes.TrimSpace(next.ReportSpec)) == 0 {
			next.ReportSpec = cloneJSON(source.ReportSpec)
		}
		if len(bytes.TrimSpace(next.ReportFill)) == 0 {
			next.ReportFill = cloneJSON(source.ReportFill)
		}
		if len(bytes.TrimSpace(next.ReportPrint)) == 0 {
			next.ReportPrint = cloneJSON(source.ReportPrint)
		}
		next.Metadata = buildPresetSourceMetadata(next.ConversationID, next.WorkspaceID, source, mergeJSONObjectBytes(source.Metadata, next.Metadata))
		switch next.Format {
		case ExportFormatPDF:
			if len(bytes.TrimSpace(next.ReportPrint)) == 0 {
				return nil, fmt.Errorf("reporting export: preset source requires a materialized reportPrint for pdf export")
			}
		case ExportFormatCSV, ExportFormatXLSX:
			if len(bytes.TrimSpace(next.ReportFill)) == 0 {
				return nil, fmt.Errorf("reporting export: preset source requires a materialized reportFill for tabular export")
			}
		}
	default:
		return nil, fmt.Errorf("reporting export: unsupported source.kind %q", strings.TrimSpace(source.Kind))
	}
	return normalizeSubmitExportRequest(next)
}
