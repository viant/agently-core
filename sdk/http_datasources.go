package sdk

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/viant/agently-core/sdk/api"
)

// FetchDatasource dispatches POST /v1/api/datasources/{id}/fetch.
func (c *HTTPClient) FetchDatasource(ctx context.Context, in *api.FetchDatasourceInput) (*api.FetchDatasourceOutput, error) {
	if in == nil {
		return nil, errors.New("input is required")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, errors.New("datasource id is required")
	}
	// Wire body: the server reads id from the URL; carry the rest verbatim.
	body := struct {
		Inputs map[string]interface{}    `json:"inputs,omitempty"`
		Cache  *api.DatasourceCacheHints `json:"cache,omitempty"`
	}{Inputs: in.Inputs, Cache: in.Cache}
	var out api.FetchDatasourceOutput
	path := "/v1/api/datasources/" + url.PathEscape(id) + "/fetch"
	if err := c.doJSON(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InvalidateDatasourceCache dispatches DELETE /v1/api/datasources/{id}/cache.
func (c *HTTPClient) InvalidateDatasourceCache(ctx context.Context, in *api.InvalidateDatasourceCacheInput) error {
	if in == nil {
		return errors.New("input is required")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return errors.New("datasource id is required")
	}
	path := "/v1/api/datasources/" + url.PathEscape(id) + "/cache"
	if h := strings.TrimSpace(in.InputsHash); h != "" {
		path += "?inputsHash=" + url.QueryEscape(h)
	}
	// doJSON with nil out ignores the response body.
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// ListLookupRegistry dispatches GET /v1/api/lookups/registry?context=<…>.
func (c *HTTPClient) ListLookupRegistry(ctx context.Context, in *api.ListLookupRegistryInput) (*api.ListLookupRegistryOutput, error) {
	if in == nil {
		return nil, errors.New("input is required")
	}
	ctxParam := strings.TrimSpace(in.Context)
	if ctxParam == "" {
		return nil, errors.New("context is required (e.g. \"template:foo\")")
	}
	path := "/v1/api/lookups/registry?context=" + url.QueryEscape(ctxParam)
	var out api.ListLookupRegistryOutput
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
