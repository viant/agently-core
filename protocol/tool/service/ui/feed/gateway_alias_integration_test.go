package feed

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type aliasPolicy struct{ actor identity.VerifiedActor }

func (p aliasPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, available []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	return identity.ResourceDecision{Candidate: available[0], AuthorityBinding: "owned", ValidUntil: p.actor.ValidUntil}, nil
}
func TestGatewayAliasNativeGetUpdatePreservesOriginalModelContract(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "feeds"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "feeds/media.yaml"), []byte("id: media-plan\nmatch: {service: media/plan, method: get}\ndataSource: {editDraft: {source: output}}\nui: {draftOnly: true}\n"), 0600))
	actor := identity.VerifiedActor{Subject: "owner", Issuer: "synthetic", TenantID: "team", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Minute)}
	local, err := resources.NewInternalProvider(context.Background(), root, []resources.ExtensionDirectory{resources.ToolfeedDirectory("ownedfeeds", "feeds")}, func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) (identity.ResourceRevisionPolicy, error) {
		return aliasPolicy{actor}, nil
	}, resources.LocalConfig{Actor: func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, Verify: func(context.Context, identity.VerifiedActor) error { return nil }, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Validators: map[string]resources.LocalResourceValidator{primitive.ToolfeedKind: resources.ValidateToolfeedDefinition}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	require.NoError(t, mgr.RegisterLocal(context.Background(), "internal", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: local.ProviderIdentity()}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(local) }))
	g := resources.NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "host")
	defer g.Close()
	hub := bridge.NewService(&bridge.Config{NamespaceResolver: func(context.Context) (bridge.NamespaceIdentity, error) {
		return bridge.NamespaceIdentity{Namespace: "owner", ValidUntil: actor.ValidUntil}, nil
	}})
	session := ""
	rpc := func(method string, params map[string]any) map[string]any {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "owned", "method": method, "params": params})
		r := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if session != "" {
			r.Header.Set("Mcp-Session-Id", session)
		}
		w := httptest.NewRecorder()
		hub.Hub().ServeHTTPRPC(w, r)
		if header := w.Header().Get("Mcp-Session-Id"); header != "" {
			session = header
		}
		var result map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.Nil(t, result["error"])
		if value, ok := result["result"].(map[string]any); ok {
			return value
		}
		return nil
	}
	rpc("ui.hello", map[string]any{"clientId": "mounted"})
	rpc("ui.snapshot", map[string]any{"clientId": "mounted", "data": map[string]any{"conversationId": "owned-conversation", "windows": []any{}}})
	canonical := "toolfeed://ownedfeeds/media"
	service := New(hub, WithIdentityResolver(func(ctx context.Context, requested string) (string, error) {
		got, err := resources.ResolveDefinition[primitive.ToolfeedDefinition](ctx, g, primitive.ToolfeedKind, requested, func(d primitive.ToolfeedDefinition) string { return d.ID })
		if err != nil {
			return "", err
		}
		return got.Resource.URI, nil
	}))
	ctx := requestctx.WithConversationID(context.Background(), "owned-conversation")
	budget := 120000.0
	for _, method := range []string{"get", "update", "get"} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			command := rpc("ui.poll", map[string]any{"clientId": "mounted", "timeoutMs": 1000})
			require.NotNil(t, command)
			request := command["params"].(map[string]any)
			params := request["params"].(map[string]any)
			require.Equal(t, canonical, params["feedId"])
			require.Equal(t, "owned-conversation", params["conversationId"])
			result := map[string]any{"feedId": canonical, "conversationId": "owned-conversation", "dataSources": map[string]any{"editDraft": map[string]any{"form": map[string]any{"totalBudget": budget}}}}
			if method == "update" {
				operations := params["operations"].([]any)
				budget = operations[0].(map[string]any)["value"].(float64)
				result = map[string]any{"ok": true}
			}
			rpc("ui.response", map[string]any{"id": command["id"], "ok": true, "result": result})
		}()
		executable, err := service.Method(method)
		require.NoError(t, err)
		if method == "get" {
			out := &GetOutput{}
			require.NoError(t, executable(ctx, &GetInput{FeedID: "media-plan", DataSourceRefs: []string{"editDraft"}}, out))
			data := out.Data.(map[string]any)
			require.Equal(t, "media-plan", data["feedId"])
			require.Equal(t, canonical, data["resourceURI"])
		} else {
			require.NoError(t, executable(ctx, &UpdateInput{FeedID: "media-plan", Operations: []Operation{{DataSourceRef: "editDraft", Op: "replace", Path: "/form/totalBudget", Value: 130000}}}, &UpdateOutput{}))
		}
		<-done
	}
	require.Equal(t, 130000.0, budget)
}
