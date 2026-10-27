package tool

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
	svc "github.com/viant/agently-core/protocol/tool/service"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	schema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// An authoritative internal registration must not consult a remote manager.
// If it does, these methods fail or alter auth so the regression is observable.
type internalAuthBoundaryManager struct {
	options, get, useID, withAuth, touch atomic.Int32
}

func (m *internalAuthBoundaryManager) Get(context.Context, string, string) (mcpclient.Interface, error) {
	m.get.Add(1)
	return nil, errors.New("unexpected remote client acquisition")
}
func (m *internalAuthBoundaryManager) Reconnect(context.Context, string, string) (mcpclient.Interface, error) {
	return nil, errors.New("unexpected remote reconnect")
}
func (m *internalAuthBoundaryManager) Touch(string, string) { m.touch.Add(1) }
func (m *internalAuthBoundaryManager) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	m.options.Add(1)
	return nil, errors.New("remote configuration unavailable")
}
func (m *internalAuthBoundaryManager) UseIDToken(context.Context, string) bool {
	m.useID.Add(1)
	return true
}
func (m *internalAuthBoundaryManager) WithAuthTokenContext(ctx context.Context, _ string) context.Context {
	m.withAuth.Add(1)
	return authctx.WithBearer(ctx, "unexpected-manager-token")
}
func (m *internalAuthBoundaryManager) assertUnused(t *testing.T) {
	t.Helper()
	if m.options.Load() != 0 || m.get.Load() != 0 || m.useID.Load() != 0 || m.withAuth.Load() != 0 || m.touch.Load() != 0 {
		t.Fatalf("internal service consulted remote manager: Options=%d Get=%d UseIDToken=%d WithAuthTokenContext=%d Touch=%d", m.options.Load(), m.get.Load(), m.useID.Load(), m.withAuth.Load(), m.touch.Load())
	}
}

type internalAuthBoundaryService struct {
	calls   atomic.Int32
	execute func(context.Context, map[string]interface{}) (map[string]string, error)
}

func (*internalAuthBoundaryService) Name() string { return "sample/internal" }
func (*internalAuthBoundaryService) Methods() svc.Signatures {
	return svc.Signatures{
		{Name: "read", Input: reflect.TypeOf(&map[string]interface{}{}), Output: reflect.TypeOf(&map[string]string{})},
		{Name: "inspect", Internal: true, Input: reflect.TypeOf(&map[string]interface{}{}), Output: reflect.TypeOf(&map[string]string{})},
	}
}
func (s *internalAuthBoundaryService) Method(string) (svc.Executable, error) {
	return func(ctx context.Context, in, out interface{}) error {
		s.calls.Add(1)
		result, err := s.execute(ctx, *(in.(*map[string]interface{})))
		if err != nil {
			return err
		}
		*(out.(*map[string]string)) = result
		return nil
	}, nil
}

type internalAuthBoundaryGuard struct {
	claim func(context.Context, string, map[string]interface{}) (toolprotection.Claim, error)
}

func (*internalAuthBoundaryGuard) IsProtected(string) bool { return true }
func (g *internalAuthBoundaryGuard) Claim(ctx context.Context, name string, args map[string]interface{}) (toolprotection.Claim, error) {
	return g.claim(ctx, name, args)
}
func (*internalAuthBoundaryGuard) Finish(context.Context, toolprotection.Claim, toolprotection.State) {
}

type internalAuthBoundaryClient struct {
	mcpclient.Interface
	inspect func(context.Context, *schema.CallToolRequestParams, *mcpclient.RequestOptions)
}

func (c *internalAuthBoundaryClient) CallTool(ctx context.Context, params *schema.CallToolRequestParams, options ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	c.inspect(ctx, params, mcpclient.NewRequestOptions(options))
	return c.Interface.CallTool(ctx, params, options...)
}

type internalAuthBoundaryContextKey struct{}

func TestInternalExecutionPreservesIngressAndGuardsWithoutRemoteAuthSelection(t *testing.T) {
	mgr := &internalAuthBoundaryManager{}
	reg := &Registry{mgr: mgr, cache: map[string]*toolCacheEntry{}, internal: map[string]mcpclient.Interface{}}
	marker := new(int)
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.WithValue(context.Background(), internalAuthBoundaryContextKey{}, marker), deadline)
	defer cancel()
	ctx = authctx.WithBearer(ctx, "sample-ingress-access")
	ctx = authctx.WithIDToken(ctx, "sample-ingress-id")
	ctx = authctx.WithProvider(ctx, "sample-provider")
	ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "sample-principal"})
	ctx = authctx.WithCanonicalUserID(ctx, "sample-canonical-principal")
	ctx = requestctx.WithConversationID(ctx, "sample-conversation")
	args := map[string]interface{}{"scope": "sample-scope", "nested": map[string]interface{}{"value": "sample"}}
	check := func(current context.Context, currentArgs map[string]interface{}) {
		t.Helper()
		if current.Value(internalAuthBoundaryContextKey{}) != marker || authctx.Bearer(current) != "sample-ingress-access" || authctx.IDToken(current) != "sample-ingress-id" || authctx.Provider(current) != "sample-provider" || authctx.EffectiveUserID(current) != authctx.EffectiveUserID(ctx) || authctx.CanonicalUserID(current) != "sample-canonical-principal" || requestctx.ConversationIDFromContext(current) != "sample-conversation" {
			t.Error("ingress identity or context changed")
		}
		if got, ok := current.Deadline(); !ok || !got.Equal(deadline) {
			t.Error("ingress deadline changed")
		}
		if !reflect.DeepEqual(currentArgs, args) {
			t.Errorf("arguments changed: %#v", currentArgs)
		}
	}
	service := &internalAuthBoundaryService{execute: func(current context.Context, currentArgs map[string]interface{}) (map[string]string, error) {
		check(current, currentArgs)
		return map[string]string{"status": "ok"}, nil
	}}
	if err := reg.AddInternalService(service); err != nil {
		t.Fatal(err)
	}
	clientCalls := 0
	reg.internal[service.Name()] = &internalAuthBoundaryClient{Interface: reg.internal[service.Name()], inspect: func(current context.Context, params *schema.CallToolRequestParams, options *mcpclient.RequestOptions) {
		clientCalls++
		check(current, params.Arguments)
		if params.Name != "read" || options.StringToken != "sample-ingress-access" {
			t.Error("ordinary internal client authentication or method forwarding changed")
		}
	}}
	authorizationCalls, protectionCalls := 0, 0
	reg.SetAuthorizationGuard(func(current context.Context, name string, currentArgs map[string]interface{}) error {
		authorizationCalls++
		check(current, currentArgs)
		if name != "sample/internal:read" {
			t.Error("guard name changed")
		}
		return nil
	})
	reg.SetExecutionProtection(&internalAuthBoundaryGuard{claim: func(current context.Context, _ string, currentArgs map[string]interface{}) (toolprotection.Claim, error) {
		protectionCalls++
		check(current, currentArgs)
		return toolprotection.Claim{}, nil
	}})
	if err := reg.PreflightCredential(ctx, "sample/internal:read"); err != nil {
		t.Fatal(err)
	}
	if _, configured := reg.ToolTimeout("sample/internal:read"); configured {
		t.Fatal("unexpected timeout from remote configuration")
	}
	reg.internalMethodTimeout[service.Name()] = map[string]time.Duration{"read": 30 * time.Second}
	if timeout, configured := reg.ToolTimeout("sample/internal:read"); !configured || timeout != 30*time.Second {
		t.Fatal("declared internal method timeout was lost")
	}
	out, err := reg.Execute(ctx, "sample/internal:read", args)
	if err != nil || out != `{"status":"ok"}` {
		t.Fatalf("internal execution failed: %s %v", out, err)
	}
	if clientCalls != 1 || service.calls.Load() != 1 || authorizationCalls != 1 || protectionCalls != 1 {
		t.Fatal("client or pre-dispatch guard skipped")
	}
	mgr.assertUnused(t)
}

func TestInternalExecutionRetainsDenialsMethodControlsAndLocalAuthentication(t *testing.T) {
	for _, test := range []string{"authorization", "protection", "duplicate", "internal-method", "local-authentication"} {
		t.Run(test, func(t *testing.T) {
			mgr := &internalAuthBoundaryManager{}
			reg := &Registry{mgr: mgr, cache: map[string]*toolCacheEntry{}, internal: map[string]mcpclient.Interface{}}
			denied := errors.New("sample authorization required")
			service := &internalAuthBoundaryService{execute: func(ctx context.Context, _ map[string]interface{}) (map[string]string, error) {
				if authctx.Bearer(ctx) == "" {
					return nil, denied
				}
				return map[string]string{"status": "ok"}, nil
			}}
			if err := reg.AddInternalService(service); err != nil {
				t.Fatal(err)
			}
			reg.SetAuthorizationGuard(func(context.Context, string, map[string]interface{}) error {
				if test == "authorization" {
					return denied
				}
				return nil
			})
			reg.SetExecutionProtection(&internalAuthBoundaryGuard{claim: func(context.Context, string, map[string]interface{}) (toolprotection.Claim, error) {
				if test == "protection" {
					return toolprotection.Claim{}, denied
				}
				if test == "duplicate" {
					return toolprotection.Claim{Protected: true, Acquired: false}, nil
				}
				return toolprotection.Claim{}, nil
			}})
			name := "sample/internal:read"
			if test == "internal-method" {
				name = "sample/internal:inspect"
			}
			out, err := reg.Execute(context.Background(), name, map[string]interface{}{})
			switch test {
			case "duplicate":
				if err != nil || !strings.Contains(out, `"duplicate_suppressed"`) {
					t.Fatal("duplicate protection bypassed")
				}
			case "internal-method":
				if err == nil || service.calls.Load() != 0 {
					t.Fatal("internal method restriction bypassed")
				}
			default:
				if !errors.Is(err, denied) {
					t.Fatalf("denial lost: %v", err)
				}
			}
			expectedCalls := int32(0)
			if test == "local-authentication" {
				expectedCalls = 1
			}
			if service.calls.Load() != expectedCalls {
				t.Fatalf("unexpected internal calls: %d", service.calls.Load())
			}
			mgr.assertUnused(t)
		})
	}
}

type externalAuthSelectionManager struct {
	executeAuthManagerStub
	optionsCalls, getCalls, delegatedCalls, preflightCalls atomic.Int32
	delegated                                              bool
}

func (m *externalAuthSelectionManager) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	m.optionsCalls.Add(1)
	return nil, nil
}
func (m *externalAuthSelectionManager) Get(context.Context, string, string) (mcpclient.Interface, error) {
	m.getCalls.Add(1)
	return m.client, nil
}
func (m *externalAuthSelectionManager) IsDelegatedAuth(context.Context, string) bool {
	m.delegatedCalls.Add(1)
	return m.delegated
}
func (m *externalAuthSelectionManager) PreflightCredential(context.Context, string) error {
	m.preflightCalls.Add(1)
	return nil
}
func TestExternalExecutionKeepsManagedAndDelegatedAuthSelection(t *testing.T) {
	for _, delegated := range []bool{false, true} {
		t.Run(fmt.Sprint(delegated), func(t *testing.T) {
			client := &authCaptureClient{}
			mgr := &externalAuthSelectionManager{executeAuthManagerStub: executeAuthManagerStub{client: client}, delegated: delegated}
			reg := &Registry{mgr: mgr, cache: map[string]*toolCacheEntry{}, internal: map[string]mcpclient.Interface{}}
			ctx := requestctx.WithConversationID(authctx.WithBearer(context.Background(), "sample-ingress-access"), "sample-conversation")
			if err := reg.PreflightCredential(ctx, "sample-external:read"); err != nil {
				t.Fatal(err)
			}
			if delegated && mgr.preflightCalls.Load() != 1 || !delegated && mgr.preflightCalls.Load() != 0 {
				t.Fatal("delegated preflight changed")
			}
			out, err := reg.Execute(ctx, "sample-external:read", map[string]interface{}{"scope": "sample"})
			if err != nil || out != "ok" {
				t.Fatalf("external execution failed: %s %v", out, err)
			}
			expected := "fresh-access-token"
			if delegated {
				expected = ""
			}
			if client.options == nil || client.options.StringToken != expected {
				t.Fatalf("external/delegated token changed: %+v", client.options)
			}
			if mgr.getCalls.Load() != 1 || mgr.optionsCalls.Load() == 0 || mgr.delegatedCalls.Load() == 0 || mgr.withAuthTokenContextCall != 1 {
				t.Fatal("managed remote auth selection skipped")
			}
		})
	}
}
