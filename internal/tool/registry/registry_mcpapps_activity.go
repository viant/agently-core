package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/mcpapps"
	"github.com/viant/agently-core/runtime/requestctx"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

// The renderer resource comes from the configured tool catalog under the
// effective principal. Response metadata and client JSON cannot select it.
func (r *Registry) resolveMCPAppBinding(ctx context.Context, server, method string) (*mcpapps.AppBinding, bool, error) {
	if r.mgr == nil {
		return nil, false, nil
	}
	var tools []mcpschema.Tool
	if r.isInternalServer(server) || r.isPublicToolCatalog(ctx, server) {
		r.mu.RLock()
		for _, entry := range r.cache {
			if entry != nil && entry.mcpDef.Name == method && entry.def.Name == server+"/"+method {
				tools = append(tools, entry.mcpDef)
				break
			}
		}
		r.mu.RUnlock()
	} else {
		scoped := r.mgr.WithAuthTokenContext(ctx, server)
		useID := r.mgr.UseIDToken(scoped, server)
		token := authctx.MCPAuthToken(scoped, useID)
		if r.isDelegatedAuthServer(scoped, server) {
			token = ""
		}
		tools, _ = r.loadPrincipalDiscoveryTools(server, authctx.EffectiveUserID(scoped), token, useID)
	}
	resourceURI := ""
	for _, tool := range tools {
		if tool.Name != method {
			continue
		}
		data, _ := json.Marshal(tool)
		var catalog struct {
			Meta struct {
				UI struct {
					URI string `json:"resourceUri"`
				} `json:"ui"`
			} `json:"_meta"`
		}
		if json.Unmarshal(data, &catalog) == nil {
			resourceURI = catalog.Meta.UI.URI
		}
		break
	}
	if !strings.HasPrefix(resourceURI, "ui://") {
		return nil, false, nil
	}
	// Privacy applies even for ordinary native clients without an AG-UI renderer.
	if !mcpapps.HasActivityEmitter(ctx) || mcpapps.Active(ctx) {
		return nil, true, nil
	}
	options, err := r.mgr.Options(ctx, server)
	if err != nil || options == nil || options.ClientOptions == nil {
		return nil, true, fmt.Errorf("configured MCP app server unavailable")
	}
	hash, err := mcpapps.ServerHash(options.Transport.Type, options.Transport.URL)
	if err != nil {
		// The pinned public renderer identifies HTTP/SSE servers. Other native
		// transports keep their tool behavior and private result separation.
		return nil, true, nil
	}
	turn, ok := requestctx.TurnMetaFromContext(ctx)
	if !ok || turn.ConversationID == "" {
		return nil, true, fmt.Errorf("MCP app requires trusted native turn")
	}
	app := &mcpapps.AppBinding{ThreadID: turn.ConversationID, NativeTurnID: turn.TurnID, InvocationID: requestctx.InvocationIDFromContext(ctx), ServerID: server, ServerHash: hash, ResourceURI: resourceURI}
	return app, true, nil
}

func publishMCPAppActivity(ctx context.Context, app *mcpapps.AppBinding, result *mcpschema.CallToolResult, args map[string]interface{}) error {
	if app == nil || result == nil {
		return nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(raw) > 4<<20 {
		return fmt.Errorf("MCP Apps host result exceeds 4MiB")
	}
	input, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return mcpapps.EmitActivity(ctx, *app, raw, input)
}
