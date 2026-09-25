package reportdefinition

import (
	"testing"

	agnproto "github.com/viant/agently-core/protocol/datasource"
)

func TestReportLocalMCPBindingIsExplicitAndNonOverlapping(t *testing.T) {
	definition, err := ParseStrict(authored(t))
	if err != nil {
		t.Fatal(err)
	}
	source := definition.DataSources["operations"]
	source.MCPRequest = &agnproto.MCPRequestBinding{QueryPath: "request.query", AuthContextPath: "request.auth.Context", HasMorePath: "meta.hasMore"}
	definition.DataSources["operations"] = source
	if err := definition.Validate(); err != nil {
		t.Fatalf("valid explicit report-local MCP binding: %v", err)
	}
	for _, queryPath := range []string{"", "request.auth", "request.__proto__.query", "request/query"} {
		invalid := source
		copy := *source.MCPRequest
		copy.QueryPath = queryPath
		invalid.MCPRequest = &copy
		definition.DataSources["operations"] = invalid
		if err := definition.Validate(); err == nil {
			t.Fatalf("accepted unsafe or overlapping query path %q", queryPath)
		}
	}
	definition.DataSources["operations"] = source
	source.MCPRequest = &agnproto.MCPRequestBinding{QueryPath: "request.query", CompleteResult: true}
	definition.DataSources["operations"] = source
	if err := definition.Validate(); err != nil {
		t.Fatalf("explicit complete-result binding: %v", err)
	}
}
