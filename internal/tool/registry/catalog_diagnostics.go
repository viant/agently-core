package tool

import (
	"context"
	"log"
	"os"
	"time"

	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

// catalogDiagnosticClient is opt-in metadata-only diagnostics. Never record
// context identities, credentials, URLs, schemas, arguments, or error text.
type catalogDiagnosticClient struct {
	client.Interface
	server string
}

func (c *catalogDiagnosticClient) ListTools(ctx context.Context, cursor *string, options ...client.RequestOption) (*schema.ListToolsResult, error) {
	started := time.Now()
	result, err := c.Interface.ListTools(ctx, cursor, options...)
	tools, ttl, scope := 0, int64(0), ""
	if result != nil {
		tools = len(result.Tools)
		ttl = int64(result.TtlMs)
		scope = string(result.CacheScope)
	}
	proactive, _ := ctx.Value(toolCatalogWarmContextKey{}).(bool)
	log.Printf("[tool-catalog] server=%q warm=%t cursor=%t elapsed_ms=%.3f tools=%d ttl_ms=%d cache_scope=%q failed=%t", c.server, proactive, cursor != nil, float64(time.Since(started))/float64(time.Millisecond), tools, ttl, scope, err != nil)
	return result, err
}
func diagnosticCatalogClient(server string, c client.Interface) client.Interface {
	if os.Getenv("AGENTLY_DEBUG_TOOL_CATALOG") == "" {
		return c
	}
	return &catalogDiagnosticClient{Interface: c, server: server}
}
