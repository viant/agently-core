package tool

import (
	"context"
	"errors"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcp/config"
	discovery "github.com/viant/agently-core/runtime/discovery"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
	"reflect"
	"testing"
	"time"
)

func TestEmptyAnonymousCatalogAllowsAuthenticatedRecovery(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "reconnect"}[reconnect], func(t *testing.T) {
			anonymousCalls := 0
			expected := schema.Tool{Name: "ForecastingCube", InputSchema: schema.ToolInputSchema{Type: "object", Required: []string{"From"}, Properties: schema.ToolInputSchemaProperties{"From": {"type": "string"}}}}
			stub := &discoveryManagerStub{options: &config.MCPClient{}, reconnectFunc: func(string, string) (client.Interface, error) { return &discoveryListClient{}, nil }}
			stub.getFunc = func(scope, server string) (client.Interface, error) {
				if scope == "mcp-discovery:steward:background" {
					anonymousCalls++
					if anonymousCalls == 1 {
						return nil, errors.New("dial tcp: connection refused")
					}
					if reconnect && anonymousCalls == 2 {
						return &discoveryListClient{listErr: errors.New("connection reset by peer")}, nil
					}
					return &discoveryListClient{}, nil
				}
				if scope == "conv-owner" {
					return &discoveryListClient{tools: []schema.Tool{expected}}, nil
				}
				return &discoveryListClient{tools: []schema.Tool{{Name: "OtherPrincipal"}}}, nil
			}
			reg := &Registry{mgr: stub, cache: map[string]*toolCacheEntry{}, discoveryFailTTL: time.Nanosecond}
			bg := discovery.WithBackground(context.Background())
			if _, err := reg.listServerTools(bg, "steward"); err == nil {
				t.Fatal("initial transport failure missing")
			}
			time.Sleep(time.Millisecond)
			if tools, err := reg.listServerTools(bg, "steward"); err != nil || len(tools) != 0 {
				t.Fatalf("empty recovery: %v %v", tools, err)
			}
			if got := reg.toolCatalogVisibility(bg, "steward"); got != "" {
				t.Fatalf("empty anonymous catalog classified %q", got)
			}
			owner := requestctx.WithConversationID(discoveryUserContext(), "conv-owner")
			tools, err := reg.listServerTools(owner, "steward")
			if err != nil || len(tools) != 1 || !reflect.DeepEqual(tools[0].InputSchema, expected.InputSchema) {
				t.Fatalf("authenticated schema recovery: %v %v", tools, err)
			}
			other := requestctx.WithConversationID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "other-user"}), "conv-other")
			otherTools, err := reg.listServerTools(other, "steward")
			if err != nil || len(otherTools) != 1 || otherTools[0].Name != "OtherPrincipal" {
				t.Fatalf("principal isolation: %v %v", otherTools, err)
			}
			if len(reg.cache) != 0 {
				t.Fatal("private discovery entered global cache")
			}
		})
	}
}
