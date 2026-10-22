package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	service "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/forge/backend/types"
)

type principalInlineCatalog struct{ window *types.Window }

func (c *principalInlineCatalog) UsesResourceResolution() bool { return true }
func (c *principalInlineCatalog) List(context.Context, *service.WindowDefinitionListInput) (*service.WindowDefinitionListOutput, error) {
	return &service.WindowDefinitionListOutput{}, nil
}
func (c *principalInlineCatalog) Get(context.Context, *service.WindowDefinitionGetInput) (*service.WindowDefinitionGetOutput, error) {
	return &service.WindowDefinitionGetOutput{WindowID: "view", Definition: c.window}, nil
}
func (c *principalInlineCatalog) RevalidateResource(_ context.Context, key string, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
	if key != "view" || !identity.SameResolvedResource(pin, *c.window.Resource) || !identity.MetadataExpiryValid(pin.ValidUntil, time.Now()) {
		return nil, identity.ErrResourceDenied
	}
	fresh := *c.window.Resource
	return &fresh, nil
}

type principalInlineResolver struct{ calls int }

func (p *principalInlineResolver) Resolve(_ context.Context, r *permittedview.Request) (*permittedview.Snapshot, error) {
	p.calls++
	if !r.IncludePrincipal || r.ResourceType != "" || len(r.ResourceIDs) > 0 || len(r.RequestedCapabilities) > 0 || len(r.RequestedGlobalCapabilities) > 0 {
		panic("native open requested entity permissions")
	}
	return &permittedview.Snapshot{AuthorizationVersion: "display", ExpiresAt: time.Now().Add(time.Minute), Principal: map[string]any{"roles": []string{"reader"}, "exposures": []string{"REPORTS_UI"}}}, nil
}
func TestNativeOpenInlineMetadataContainsPrincipalDisplayFacts(t *testing.T) {
	window := &types.Window{Resource: &identity.ResolvedResource{URI: "window://owner/view", ProviderIdentity: "owner", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte(`{}`))}, AuthorityBinding: "actor", ValidUntil: time.Now().Add(time.Minute)}}
	require.NoError(t, json.Unmarshal([]byte(`{"authorization":{"scope":"resource","resource":{"type":"entity","id":{"source":"resource","selector":"EntityId.0"}},"requestedCapabilities":["read"]},"view":{"content":{"id":"root","containers":[{"id":"reader","visibleWhen":{"source":"authorization","field":"principal.roles","contains":"reader"}},{"id":"reports","visibleWhen":{"source":"authorization","field":"principal.exposures","contains":"REPORTS_UI"}},{"id":"admin","visibleWhen":{"source":"authorization","field":"principal.roles","contains":"admin"}}]}}}`), window))
	facts := &principalInlineResolver{}
	projection := permittedview.NewRuntime(facts)
	bridge := service.NewService(&service.Config{WindowDefinitions: &principalInlineCatalog{window}, WindowUIProjection: func(ctx context.Context, input *permittedview.BoundView) (*types.Window, error) {
		result, err := projection.ProjectPrincipal(ctx, input.Window)
		if result == nil {
			return nil, err
		}
		return result.Window, err
	}})
	_, err := principalInlineRPC(bridge, "ui.hello", map[string]any{"clientId": "client"})
	require.NoError(t, err)
	delivered := make(chan json.RawMessage, 1)
	worker := make(chan error, 1)
	go func() {
		result, err := principalInlineRPC(bridge, "ui.poll", map[string]any{"clientId": "client", "timeoutMs": 1000})
		if err != nil {
			worker <- err
			return
		}
		command, ok := result["params"].(map[string]any)
		if !ok {
			worker <- fmt.Errorf("missing command")
			return
		}
		raw, err := json.Marshal(command["params"])
		if err != nil {
			worker <- err
			return
		}
		delivered <- raw
		_, err = principalInlineRPC(bridge, "ui.response", map[string]any{"id": command["id"], "ok": true, "result": map[string]any{"windowId": "instance"}})
		worker <- err
	}()
	opened, err := bridge.UICommand(context.Background(), &service.UICommandInput{ClientID: "client", Method: "ui.window.open", Params: map[string]any{"windowKey": "view", "parameters": map[string]any{"EntityId": []int{42}}}})
	require.NoError(t, err)
	require.True(t, opened.OK)
	require.NoError(t, <-worker)
	var command struct {
		Options struct {
			InlineMetadata types.Window `json:"inlineMetadata"`
		} `json:"options"`
	}
	require.NoError(t, json.Unmarshal(<-delivered, &command))
	metadata := command.Options.InlineMetadata
	require.Equal(t, 1, facts.calls)
	require.Len(t, metadata.View.Content.Containers, 2)
	require.Equal(t, "reader", metadata.View.Content.Containers[0].ID)
	require.Equal(t, "reports", metadata.View.Content.Containers[1].ID)
	require.NotNil(t, metadata.AuthorizationSnapshot["principal"])
	require.Nil(t, metadata.AuthorizationSnapshot["resources"])
}

func principalInlineRPC(bridge *service.Service, method string, params map[string]any) (map[string]any, error) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "test-" + method, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	request := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	bridge.Hub().ServeHTTPRPC(response, request)
	var envelope map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		return nil, err
	}
	if response.Code != 200 || envelope["error"] != nil {
		return nil, fmt.Errorf("UI RPC %s failed: %d", method, response.Code)
	}
	result, _ := envelope["result"].(map[string]any)
	return result, nil
}
