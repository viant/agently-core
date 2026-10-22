package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/forge/backend/types"
)

type managedPrincipalFacts struct {
	calls int
	fail  bool
}

func (f *managedPrincipalFacts) Resolve(_ context.Context, input *permittedview.Request) (*permittedview.Snapshot, error) {
	f.calls++
	if !input.IncludePrincipal || input.ResourceType != "" || len(input.ResourceIDs) > 0 || len(input.StringResourceIDs) > 0 || len(input.RequestedCapabilities) > 0 || len(input.RequestedGlobalCapabilities) > 0 {
		panic("managed presentation requested resource permission")
	}
	if f.fail {
		return nil, errors.New("optional facts unavailable")
	}
	return &permittedview.Snapshot{AuthorizationVersion: "display", ExpiresAt: time.Now().Add(time.Minute), Principal: map[string]any{"roles": []string{"reader"}, "exposures": []string{"REPORTS_UI"}}, Resources: map[string]*permittedview.Resource{"42": {ID: 42, Capabilities: map[string]bool{"read": true}}}}, nil
}
func managedPresentationFixture(t *testing.T) *publishedWindowBindingFixture {
	t.Helper()
	f := &publishedWindowBindingFixture{emissionHandoffFixture: &emissionHandoffFixture{selectionHTTPFixture: selectionHTTP(t)}}
	require.NoError(t, json.Unmarshal([]byte(`{"authorization":{"scope":"resource","resource":{"type":"entity","id":{"source":"resource","selector":"EntityId.0"}},"requestedCapabilities":["read"]},"view":{"content":{"id":"root","containers":[{"id":"reader","visibleWhen":{"source":"authorization","field":"principal.roles","contains":"reader"}},{"id":"reports","visibleWhen":{"source":"authorization","field":"principal.exposures","contains":"REPORTS_UI"}},{"id":"admin","visibleWhen":{"source":"authorization","field":"principal.roles","contains":"admin"}}]}}}`), f.window))
	return f
}
func getManagedPresentation(t *testing.T, f WindowResourceProvider, resolver permittedview.Resolver) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, permittedview.NewRuntime(resolver), WithWindowResourceProvider(f))
	request := httptest.NewRequest(http.MethodGet, "/window/view?clientId=client-one&windowId=record-321&windowParams=%7B%22EntityId%22%3A%5B42%5D%7D", nil)
	request.Header.Set("Mcp-Session-Id", "negotiated-session")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type configuredManagedPresentation struct {
	*publishedWindowBindingFixture
	facts       *managedPrincipalFacts
	projections int
}

func (*configuredManagedPresentation) HasWindowUIProjection() bool { return true }
func (f *configuredManagedPresentation) ProjectWindowWithInput(ctx context.Context, input *permittedview.BoundView) (*types.Window, error) {
	f.projections++
	if input.WindowID != "" || len(input.WindowForm) > 0 || len(input.ResourceData) > 0 {
		panic("ordinary presentation GET supplied resource-binding inputs")
	}
	result, err := permittedview.NewRuntime(f.facts).ProjectPrincipal(ctx, input.Window)
	if result == nil {
		return nil, err
	}
	return result.Window, err
}

func TestManagedMetadataUsesConfiguredPresentationWithoutSeparateCapabilityRuntime(t *testing.T) {
	f := &configuredManagedPresentation{publishedWindowBindingFixture: managedPresentationFixture(t), facts: &managedPrincipalFacts{}}
	response := getManagedPresentation(t, f, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, 1, f.projections)
	require.Equal(t, 1, f.facts.calls)
	require.Equal(t, 1, f.bindings)
	require.Zero(t, f.admits)
	var delivered struct {
		Data *types.Window `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &delivered))
	require.Len(t, delivered.Data.View.Content.Containers, 2)
	require.Equal(t, "reader", delivered.Data.View.Content.Containers[0].ID)
	require.Equal(t, "reports", delivered.Data.View.Content.Containers[1].ID)
}
func TestManagedMetadataOrdinaryGetSuppliesPrincipalDisplayFactsOnly(t *testing.T) {
	f := managedPresentationFixture(t)
	facts := &managedPrincipalFacts{}
	response := getManagedPresentation(t, f, facts)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, 1, facts.calls)
	require.Equal(t, 1, f.gets)
	require.Equal(t, 1, f.bindings)
	require.Zero(t, f.admits)
	require.Zero(t, f.signs)
	var result struct {
		Data *types.Window `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Len(t, result.Data.View.Content.Containers, 2)
	require.Equal(t, "reader", result.Data.View.Content.Containers[0].ID)
	require.Equal(t, "reports", result.Data.View.Content.Containers[1].ID)
	require.Nil(t, result.Data.AuthorizationSnapshot["resources"], "principal display facts must not carry entity grants")
	require.Len(t, f.window.View.Content.Containers, 3, "producer tree must remain unchanged")
	f = managedPresentationFixture(t)
	f.failGet = 1
	facts.calls = 0
	denied := getManagedPresentation(t, f, facts)
	require.Equal(t, 404, denied.Code)
	require.Zero(t, facts.calls)
	require.Zero(t, f.bindings)
	require.NotContains(t, denied.Body.String(), "authorizationSnapshot")
}
func TestManagedMetadataPreservesProducerFactsAndOptionalMissingFacts(t *testing.T) {
	f := managedPresentationFixture(t)
	facts := &managedPrincipalFacts{}
	f.window.AuthorizationSnapshot = map[string]any{"principal": map[string]any{"roles": []string{"producer-role"}, "exposures": []string{"PRODUCER_UI"}}}
	response := getManagedPresentation(t, f, facts)
	require.Equal(t, 200, response.Code)
	require.Zero(t, facts.calls)
	require.Contains(t, response.Body.String(), "producer-role")
	var delivered struct {
		Data *types.Window `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &delivered))
	wantSnapshot, err := json.Marshal(f.window.AuthorizationSnapshot)
	require.NoError(t, err)
	actualSnapshot, err := json.Marshal(delivered.Data.AuthorizationSnapshot)
	require.NoError(t, err)
	require.JSONEq(t, string(wantSnapshot), string(actualSnapshot))
	f = managedPresentationFixture(t)
	facts.fail = true
	response = getManagedPresentation(t, f, facts)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, 1, f.bindings)
	require.Contains(t, response.Body.String(), "visibleWhen")
	require.NotContains(t, response.Body.String(), "authorizationSnapshot")
}
func TestManagedMetadataConfiguredMCPReceivesOnlyPrincipalFactsRequest(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "available", true: "optional unavailable"}[unavailable], func(t *testing.T) {
			calls := 0
			resolver := &permittedview.MCPResolver{ToolName: "configured:DisplayFacts", Executor: managedLegacyExecutor(func(_ context.Context, name string, args map[string]interface{}) (string, error) {
				calls++
				require.Equal(t, "configured:DisplayFacts", name)
				require.Equal(t, map[string]interface{}{
					"IncludePrincipal": true, "ResourceType": "", "ResourceIDs": []int(nil),
					"RequestedCapabilities": []string(nil), "RequestedGlobalCapabilities": []string(nil),
				}, args, "the configured UI facts source must never receive entity permission inputs")
				if unavailable {
					return "", errors.New("optional principal facts unavailable")
				}
				return `{"authorizationVersion":"display","expiresAt":"2999-01-01T00:00:00Z","principal":{"roles":["reader"],"exposures":["REPORTS_UI"]}}`, nil
			})}
			f := managedPresentationFixture(t)
			response := getManagedPresentation(t, f, resolver)
			require.Equal(t, 200, response.Code, response.Body.String())
			require.Equal(t, 1, calls)
			require.Equal(t, 1, f.bindings)
			var delivered struct {
				Data *types.Window `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &delivered))
			if unavailable {
				require.Len(t, delivered.Data.View.Content.Containers, 3)
				require.Nil(t, delivered.Data.AuthorizationSnapshot)
			} else {
				require.Len(t, delivered.Data.View.Content.Containers, 2)
				require.Equal(t, "reader", delivered.Data.View.Content.Containers[0].ID)
				require.Equal(t, "reports", delivered.Data.View.Content.Containers[1].ID)
				require.Nil(t, delivered.Data.AuthorizationSnapshot["resources"])
			}
		})
	}
}

type managedLegacyExecutor func(context.Context, string, map[string]interface{}) (string, error)

func (f managedLegacyExecutor) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return f(ctx, name, args)
}
