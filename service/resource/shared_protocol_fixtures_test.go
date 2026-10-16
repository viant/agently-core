package resource

import (
	"context"
	"encoding/json"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

func sameLocalActor(a, b identity.VerifiedActor) bool {
	return a.Subject == b.Subject && a.Issuer == b.Issuer && a.TenantID == b.TenantID && a.AccountID == b.AccountID && a.IdentityRevision == b.IdentityRevision
}
func catalogPrincipalScope(actor identity.VerifiedActor) string {
	raw, _ := json.Marshal([]any{actor.PublicRead, actor.Issuer, actor.Subject, actor.TenantID})
	return string(raw)
}
func toolRaw(ctx context.Context, c mcpclient.Interface, method string, input any) (json.RawMessage, error) {
	raw, e := json.Marshal(input)
	if e != nil {
		return nil, e
	}
	var args map[string]any
	if e = json.Unmarshal(raw, &args); e != nil {
		return nil, e
	}
	r, e := c.CallTool(ctx, &schema.CallToolRequestParams{Name: method, Arguments: args})
	if e != nil {
		return nil, e
	}
	if r == nil || r.IsError != nil && *r.IsError {
		return nil, fmt.Errorf("tool failed")
	}
	if r.StructuredContent != nil {
		return json.Marshal(r.StructuredContent)
	}
	for _, content := range r.Content {
		if text, ok := content.(schema.TextContent); ok && json.Valid([]byte(text.Text)) {
			return json.RawMessage(text.Text), nil
		}
		if object, ok := content.(map[string]any); ok {
			text, _ := object["text"].(string)
			if object["type"] == "text" && json.Valid([]byte(text)) {
				return json.RawMessage(text), nil
			}
		}
	}
	return nil, fmt.Errorf("missing JSON tool result")
}
