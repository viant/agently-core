package reporting

import (
	"context"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/protocol/primitive/observe"
	identity "github.com/viant/agently-core/protocol/resource"
	svc "github.com/viant/agently-core/protocol/tool/service"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	dssvc "github.com/viant/agently-core/service/datasource"
	sharedds "github.com/viant/forge/backend/datasource"
	shared "github.com/viant/forge/backend/reporting/engine"
)

type ExecuteResourceRequest = shared.ExecuteResourceRequest
type ExecuteResourceResult = shared.ExecuteResourceResult
type ResourceDatasetExecutor = shared.ResourceDatasetExecutor

// The engine is composed once by New; setters are pre-serving configuration.
func (s *Service) SetResourceDatasetExecutor(execute ResourceDatasetExecutor) {
	if s != nil && s.execution != nil {
		s.execution.SetResourceDatasetExecutor(execute)
	}
}
func (s *Service) ExecuteResource(ctx context.Context, request *ExecuteResourceRequest) (out *ExecuteResourceResult, resultErr error) {
	span := observe.Start(ctx, "report.execute", observe.Labels{Kind: "report"})
	defer func() { span.End(observe.OutcomeFor(resultErr), observe.CacheUnknown) }()
	if s == nil || s.execution == nil {
		return nil, identity.ErrResourceDenied
	}
	return s.execution.ExecuteResource(ctx, request)
}
func (s *Service) AuthorizeResourceDataset(ctx context.Context, descriptor *dsproto.DataSource, inputs map[string]interface{}) error {
	if s == nil || s.execution == nil {
		return identity.ErrResourceDenied
	}
	return s.execution.AuthorizeResourceDataset(ctx, descriptor, inputs)
}
func NewResourceDatasetExecutor(executor dssvc.ToolExecutor, authorize dssvc.DefinitionAuthorizer, dispatchers ...windowprotocol.ComponentDispatcher) ResourceDatasetExecutor {
	return shared.NewResourceDatasetExecutor(executor, sharedds.DefinitionAuthorizer(authorize), dispatchers...)
}
func NewResourceDatasetExecutorWithProvider(executor dssvc.ToolExecutor, authorize dssvc.DefinitionAuthorizer, provider dssvc.ProviderExecutor, dispatchers ...windowprotocol.ComponentDispatcher) ResourceDatasetExecutor {
	return shared.NewResourceDatasetExecutorWithProvider(executor, sharedds.DefinitionAuthorizer(authorize), sharedds.ProviderExecutor(provider), dispatchers...)
}

func (s *Service) executeResourceTool(ctx context.Context, in, out interface{}) error {
	request, ok := in.(*ExecuteResourceRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ExecuteResourceResult)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	*output = ExecuteResourceResult{}
	result, err := s.ExecuteResource(ctx, request)
	if err != nil {
		return err
	}
	*output = *result
	return nil
}
