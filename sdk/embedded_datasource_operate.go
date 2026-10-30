package sdk

import (
	"context"
	"encoding/json"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/sdk/api"
	dssvc "github.com/viant/agently-core/service/datasource"
	"strings"
)

func (c *backendClient) OperateDatasource(ctx context.Context, in *api.OperateDatasourceInput) (*api.OperateDatasourceOutput, error) {
	if c.datasourceSvc == nil {
		return nil, ErrDatasourceStackNotConfigured
	}
	if in == nil || strings.TrimSpace(in.ID) == "" || strings.TrimSpace(in.Operation) == "" || in.Owner != nil {
		return nil, identity.ErrResourceDenied
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var request api.FetchDatasourceInput
	if err = json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	resolved, err := c.resolveDatasourceRequest(ctx, &request)
	if err != nil {
		return nil, err
	}
	result, err := c.datasourceSvc.Operate(ctx, resolved.ID, in.Operation, resolved.Inputs, dssvc.FetchOptions{SelectionBinding: resolved.SelectionBinding, Resource: resolved.Resource, Target: resolved.Target, ResourceProof: resolved.ResourceProof, WindowParameters: resolved.WindowParameters})
	if err != nil {
		return nil, err
	}
	return &api.OperateDatasourceOutput{Result: result.Result}, nil
}
