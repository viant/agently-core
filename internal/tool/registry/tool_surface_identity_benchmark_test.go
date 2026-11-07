package tool

import (
	"context"
	"fmt"
	"testing"

	"github.com/viant/agently-core/protocol/mcp/config"
	schema "github.com/viant/mcp-protocol/schema"
)

func BenchmarkPublicToolSurfaceIdentity(b *testing.B) {
	for _, count := range []int{10, 100, 500} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			m := &discoveryManagerStub{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPublic}}
			r := &Registry{mgr: m, cache: map[string]*toolCacheEntry{}}
			var tools []schema.Tool
			for i := 0; i < count; i++ {
				item := schema.Tool{Name: fmt.Sprintf("lookup_%04d", i), InputSchema: schema.ToolInputSchema{Type: "object", Properties: schema.ToolInputSchemaProperties{}}}
				for j := 0; j < 20; j++ {
					item.InputSchema.Properties[fmt.Sprintf("field_%d", j)] = map[string]any{"type": "string", "description": "Synthetic catalog input"}
				}
				tools = append(tools, item)
			}
			r.mergeServerTools("public", tools)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, identity := r.ToolSurfaceCacheIdentity(ctx, "public:lookup_0001")
				if identity == "" {
					b.Fatal("missing catalog identity")
				}
			}
		})
	}
}
