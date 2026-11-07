package llm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	schema "github.com/viant/mcp-protocol/schema"
	"testing"
)

var benchmarkConvertedTool *ToolDefinition
var benchmarkSchemaDigest [32]byte

func BenchmarkMCPToolSchemaPreparation(b *testing.B) {
	for _, size := range []int{10, 100, 500} {
		tool := schema.Tool{Name: "synthetic_lookup", InputSchema: schema.ToolInputSchema{Type: "object", Properties: schema.ToolInputSchemaProperties{}}}
		for i := 0; i < size; i++ {
			tool.InputSchema.Properties[fmt.Sprintf("field_%d", i)] = map[string]any{"type": "string", "description": "Synthetic lookup input"}
		}
		b.Run(fmt.Sprintf("convert_normalize/%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				d := ToolDefinitionFromMcpTool(&tool)
				d.Normalize()
				benchmarkConvertedTool = d
			}
		})
		b.Run(fmt.Sprintf("canonical_hash/%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				raw, err := json.Marshal(tool.InputSchema)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkSchemaDigest = sha256.Sum256(raw)
			}
		})
	}
}
