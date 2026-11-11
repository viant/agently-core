package sdk

import (
	"context"
	"encoding/json"
	"errors"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"net/http"
	"strings"

	"github.com/viant/mcp-protocol/schema"
)

// MCPResourceReader uses an existing named connection, never a browser-supplied
// transport URL. The MCP server decides which resources the current user can read.
type MCPResourceReader interface {
	ReadMCPResource(context.Context, string, string) (*schema.ReadResourceResult, error)
}

func handleMCPResourceRead(reader MCPResourceReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		server, uri := strings.TrimSpace(r.URL.Query().Get("server")), strings.TrimSpace(r.URL.Query().Get("uri"))
		if server == "" || uri == "" || len(server) > 2048 || len(uri) > 8192 {
			httpError(w, http.StatusBadRequest, errors.New("server and resource URI are required"))
			return
		}
		result, err := reader.ReadMCPResource(r.Context(), server, uri)
		if err != nil {
			httpError(w, statusForToolExecuteError(err), err)
			return
		}
		// Do not decode/re-encode resource text or binary payloads. Forge's
		// shared resource filesystem consumes the standard MCP contents.
		httpJSON(w, http.StatusOK, result)
	}
}

func (c *backendClient) ReadMCPResource(ctx context.Context, server, uri string) (*schema.ReadResourceResult, error) {
	if c == nil || c.mcpMgr == nil || strings.TrimSpace(server) == "" || strings.TrimSpace(uri) == "" {
		return nil, errors.New("MCP connection and resource URI are required")
	}
	// Published resources use the same admitted local gateway as metadata and
	// windows. A claimed URI never falls through to an unrelated MCP transport.
	if c.primitiveProviders != nil && c.primitiveProviders.OwnsPublishedMetadata(uri) {
		got, err := resources.GetFrom[json.RawMessage](ctx, c.primitiveProviders, resources.Connection{Name: server}, identity.ResourceRef{URI: uri}, func(raw json.RawMessage) (json.RawMessage, error) { return raw, nil })
		if err != nil {
			return nil, err
		}
		if got == nil || got.Resource == nil {
			return nil, identity.ErrResource
		}
		raw := got.Resource.DefinitionBytes
		if got.MenuProjection != nil {
			raw = got.MenuProjection.DefinitionBytes
		} else if len(raw) == 0 {
			raw = got.Resource.Definition
		}
		if len(raw) == 0 {
			return nil, identity.ErrResource
		}
		mime := "application/json"
		return &schema.ReadResourceResult{ResultType: schema.ResultTypeComplete, CacheScope: schema.ReadResourceResultCacheScopePrivate, Contents: []schema.ReadResourceResultContentsElem{{Uri: uri, MimeType: &mime, Text: string(raw)}}}, nil
	}
	ctx = c.mcpMgr.WithAuthTokenContext(ctx, server)
	client, err := c.mcpMgr.Get(ctx, "", server)
	if err != nil {
		return nil, err
	}
	return client.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: uri})
}
