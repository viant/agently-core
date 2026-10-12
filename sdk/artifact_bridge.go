package sdk

import (
	"context"
	"net/http"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	registry "github.com/viant/agently-core/internal/tool/registry"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/mcp-protocol/schema"
)

// WithArtifactOwner binds a server-derived owner key to the core artifact store.
// The caller remains responsible for verifying the actor behind that key.
func WithArtifactOwner(ctx context.Context, ownerKey string) context.Context {
	if ctx == nil || strings.TrimSpace(ownerKey) == "" {
		return ctx
	}
	return authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: ownerKey})
}

// CaptureMCPOutputArtifacts publishes only configured binary MCP output and
// returns a descriptor-only result. It never fetches a resource URI.
func CaptureMCPOutputArtifacts(ctx context.Context, result *schema.CallToolResult, policy mcpcfg.OutputArtifact) (*schema.CallToolResult, error) {
	return registry.CaptureOutputArtifacts(ctx, result, &policy, "")
}

// ArtifactDownloadHandler is the same owner-scoped retrieval endpoint used by
// the core SDK. Hosts must attach a verified workspace subject to each request.
func ArtifactDownloadHandler() http.Handler { return handleArtifactDownload() }
