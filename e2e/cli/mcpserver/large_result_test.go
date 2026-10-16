package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/viant/jsonrpc"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpproto "github.com/viant/mcp-protocol/server"
)

func TestLargeResultFixtureProtocol(t *testing.T) {
	h := &handler{DefaultHandler: mcpproto.NewDefaultHandler(nil, nil, nil), mode: "large_result"}
	if err := h.register(); err != nil {
		t.Fatal(err)
	}
	result, rpcErr := h.CallTool(context.Background(), &jsonrpc.TypedRequest[*mcpschema.CallToolRequest]{Request: &mcpschema.CallToolRequest{Params: mcpschema.CallToolRequestParams{Name: "large_result", Arguments: map[string]interface{}{}}}})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if result == nil || result.IsError != nil && *result.IsError {
		t.Fatal("fixture tool failed")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out largeResultOut
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Body) < 131072 || !strings.Contains(out.Body, "CHUNK-1500 LARGE_RESULT_SENTINEL") {
		t.Fatal("fixture does not satisfy oversized-result contract")
	}
	if len(result.Content) != 1 {
		t.Fatal("missing text result")
	}
	text, ok := result.Content[0].(mcpschema.TextContent)
	if !ok {
		t.Fatal("missing text carrier")
	}
	var textOut largeResultOut
	if err = json.Unmarshal([]byte(text.Text), &textOut); err != nil {
		t.Fatal(err)
	}
	if textOut.Body != out.Body {
		t.Fatal("result carriers differ")
	}
}
