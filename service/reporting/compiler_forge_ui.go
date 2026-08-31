package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/forge/backend/reporting/forgeui"
)

type ResolvedForgeUIView struct {
	UI          json.RawMessage
	DataSources map[string]json.RawMessage
}

type ForgeUIViewResolver interface {
	ResolveForgeUIView(context.Context, string, ForgeTargetContext, []string) (*ResolvedForgeUIView, error)
}

func normalizeForgeUIViewResolver(resolver ForgeUIViewResolver) ForgeUIViewResolver {
	if resolver == nil {
		return newWorkspaceForgeUIViewResolver()
	}
	return resolver
}

func (s *Service) CompileAndExportForgeUI(ctx context.Context, request *CompileAndExportForgeUIRequest) (*CompileAndExportForgeUIResult, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting forge UI export: request is required")
	}
	resolved := &ResolvedForgeUIView{}
	if ref := strings.TrimSpace(request.ViewRef); ref != "" {
		value, err := s.forgeUIViewResolver.ResolveForgeUIView(ctx, ref, request.Target, request.DataSourceRefs)
		if err != nil {
			return nil, fmt.Errorf("reporting forge UI export resolve %s: %w", ref, err)
		}
		resolved = value
	}
	if len(resolved.UI) == 0 {
		resolved.UI = cloneJSON(request.UI)
	}
	if len(resolved.UI) == 0 {
		return nil, fmt.Errorf("reporting forge UI export: viewRef or ui is required")
	}
	dataSources := map[string]json.RawMessage{}
	for key, value := range resolved.DataSources {
		dataSources[key] = cloneJSON(value)
	}
	for key, value := range request.DataSourceOverrides {
		dataSources[key] = cloneJSON(value)
	}
	for _, ref := range request.DataSourceRefs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if len(dataSources[ref]) == 0 {
			return nil, fmt.Errorf("reporting forge UI export: datasource %s is unavailable from backend and has no inline override", ref)
		}
	}
	fences, err := forgeui.BuildFences(forgeui.Request{
		ReportID: request.ReportID, Title: request.Title, UI: resolved.UI, DataSources: dataSources,
	})
	if err != nil {
		return nil, fmt.Errorf("reporting forge UI export convert: %w", err)
	}
	input := &CompileAndExportFencedReportRequest{
		ReportID: request.ReportID, Format: request.Format,
		ConversationID: request.ConversationID, WorkspaceID: request.WorkspaceID,
		Fences: make([]FencedReportFence, 0, len(fences)),
	}
	for _, fence := range fences {
		input.Fences = append(input.Fences, FencedReportFence{Kind: fence.Kind, Index: fence.Index, Payload: cloneJSON(fence.Payload)})
	}
	return s.CompileAndExportFencedReport(ctx, input)
}

func (s *Service) compileAndExportForgeUITool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*CompileAndExportForgeUIRequest)
	if !ok {
		return fmt.Errorf("invalid reporting forge UI export input %T", in)
	}
	output, ok := out.(*CompileAndExportForgeUIResult)
	if !ok {
		return fmt.Errorf("invalid reporting forge UI export output %T", out)
	}
	result, err := s.CompileAndExportForgeUI(ctx, input)
	if err != nil {
		return err
	}
	*output = *result
	return nil
}
