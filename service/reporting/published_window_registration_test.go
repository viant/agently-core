package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	uihttp "github.com/viant/agently-core/adapter/http/ui"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/requestctx"
	authsvc "github.com/viant/agently-core/service/auth"
	ui "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/artifactcohort"
	identity "github.com/viant/forge/backend/resource"
	"github.com/viant/forge/backend/types"
)

// The fixture publishes independently constructed metadata through the real
// artifact replica and gateway. The browser transports only observation data.
type publishedReportWindowCatalog struct{ ui.WindowDefinitionCatalog }

func (*publishedReportWindowCatalog) UsesResourceResolution() bool { return true }

func TestPublishedReportRegistrationAfterCredentialRotation(t *testing.T) {
	f := newPublishedPinFixture(t, "account-one")
	owner := authsvc.InjectUser(context.Background(), "reader")
	before := context.WithValue(owner, publishedPinSessionKey{}, "verified-before")
	after := context.WithValue(owner, publishedPinSessionKey{}, "verified-after")
	bridge := ui.NewService(&ui.Config{ReportCatalog: f.service.reportCatalog, WindowDefinitions: &publishedReportWindowCatalog{}, NamespaceResolver: func(ctx context.Context) (ui.NamespaceIdentity, error) {
		return ui.NamespaceIdentity{Namespace: authsvc.EffectiveUserID(ctx), SessionBound: true}, nil
	}, LocalStateOwnerResolver: func(ctx context.Context) (*types.LocalStateOwner, error) {
		return &types.LocalStateOwner{Binding: ctx.Value(publishedPinSessionKey{}).(string), ValidUntil: time.Now().Add(time.Hour)}, nil
	}})
	metadata := uihttp.NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, uihttp.WithWindowResourceProvider(bridge))
	// Negotiate the actual HTTP UI session rather than constructing a binding.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(authsvc.InjectUser(r.Context(), "reader"), publishedPinSessionKey{}, r.Header.Get("Test-Credential"))
		if strings.HasPrefix(r.URL.Path, "/window/") {
			metadata.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		bridge.Hub().ServeHTTPRPC(w, r.WithContext(ctx))
	}))
	defer server.Close()
	rpc := func(credential, session, method string) (int, string) {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{"clientId": "owner-client"}})
		request, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader(body))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("Test-Credential", credential)
		if session != "" {
			request.Header.Set("Mcp-Session-Id", session)
		}
		response, err := server.Client().Do(request)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		return response.StatusCode, response.Header.Get("Mcp-Session-Id")
	}
	status, session := rpc("verified-before", "", "ui.hello")
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, session)
	before = requestctx.WithUIBridgeSessionID(before, session)
	original, err := bridge.ReportDefinition(before, &ui.ReportDefinitionInput{Resource: identity.ResourceRef{URI: "report://example/summary"}})
	require.NoError(t, err)
	encoded, _ := json.Marshal(original.Resource)
	query := url.Values{"applyPermission": {"true"}, "clientId": {"owner-client"}, "windowId": {"kept-report"}, "resolvedResource": {string(encoded)}}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/window/"+url.PathEscape(original.Resource.URI)+"?"+query.Encode(), nil)
	require.NoError(t, err)
	request.Header.Set("Test-Credential", "verified-before")
	request.Header.Set("Mcp-Session-Id", session)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
	handled := false
	observed := json.RawMessage(`{"conversationId":"chat","selected":{"windowId":"kept-report"},"windows":[{"windowId":"kept-report","windowKey":"report://example/summary","parameters":{"scope":"applied"},"viewState":{"report":{"status":"ready","fill":{"rows":[{"value":7}]}}}}]}`)
	filtered, err := bridge.FilterResourceSnapshot(before, "reader", "owner-client", observed)
	require.NoError(t, err)
	require.Contains(t, string(filtered), `"kept-report"`)
	require.Contains(t, string(filtered), `"value":7`)
	// Current admission changes the authority, not the selected report bytes.
	current, err := bridge.ReportDefinition(after, &ui.ReportDefinitionInput{Resource: identity.ResourceRef{URI: "report://example/summary"}})
	require.NoError(t, err)
	require.Equal(t, original.Resource.ResourceCandidate, current.Resource.ResourceCandidate)
	require.NotEqual(t, original.Resource.AuthorityBinding, current.Resource.AuthorityBinding)
	_, err = bridge.ReportDefinition(after, &ui.ReportDefinitionInput{Resource: identity.ResourceRef{URI: "report://example/summary"}, ResolvedResource: original.Resource})
	require.Error(t, err, "a direct caller cannot replay the old credential authority")
	filtered, err = bridge.FilterResourceSnapshot(after, "reader", "owner-client", observed)
	require.NoError(t, err)
	require.Contains(t, string(filtered), `"kept-report"`, "current owner re-admits the exact report without losing observed state")
	// The unchanged browser uses normal 404 recovery and gets a new native
	// session before the existing report loader readmits its mounted instance.
	status, _ = rpc("verified-after", session, "ui.snapshot.status")
	require.Equal(t, http.StatusNotFound, status)
	status, renewedSession := rpc("verified-after", "", "ui.hello")
	require.Equal(t, http.StatusOK, status)
	require.NotEqual(t, session, renewedSession)
	after = requestctx.WithUIBridgeSessionID(after, renewedSession)
	// The existing authenticated restoration path gets a freshly admitted pin.
	handled, err = bridge.BindAdmittedNativePresentationWindow(after, "owner-client", "kept-report", current.Resource.URI, &types.Window{Resource: current.Resource})
	require.True(t, handled)
	require.NoError(t, err, "renew the same native instance after fresh exact-candidate admission")
	filtered, err = bridge.FilterResourceSnapshot(after, "reader", "owner-client", observed)
	require.NoError(t, err)
	require.Contains(t, string(filtered), `"kept-report"`)
	require.Contains(t, string(filtered), `"value":7`, "renewal preserves the original observed fill")
	require.Zero(t, f.calls, "registration must not execute report data")
	_, err = bridge.BindAdmittedNativePresentationWindow(after, "foreign-client", "kept-report", current.Resource.URI, &types.Window{Resource: current.Resource})
	require.Error(t, err, "a foreign client cannot borrow native readmission")
	foreign := authsvc.InjectUser(after, "another-reader")
	_, err = bridge.BindAdmittedNativePresentationWindow(foreign, "owner-client", "kept-report", current.Resource.URI, &types.Window{Resource: current.Resource})
	require.Error(t, err, "a foreign owner cannot borrow the existing native session")
	f.publish(t, 2, "Changed")
	require.NoError(t, f.replica.Sync(after))
	changed, err := bridge.ReportDefinition(after, &ui.ReportDefinitionInput{Resource: identity.ResourceRef{URI: original.Resource.URI}})
	require.NoError(t, err)
	_, err = bridge.BindAdmittedNativePresentationWindow(after, "owner-client", "kept-report", changed.Resource.URI, &types.Window{Resource: changed.Resource})
	require.ErrorIs(t, err, identity.ErrResourceStale, "readmission cannot silently replace the selected report candidate")
	bind := func(client, instance string) int {
		encoded, marshalErr := json.Marshal(changed.Resource)
		require.NoError(t, marshalErr)
		query.Set("clientId", client)
		query.Set("windowId", instance)
		query.Set("resolvedResource", string(encoded))
		req, requestErr := http.NewRequest(http.MethodGet, server.URL+"/window/"+url.PathEscape(changed.Resource.URI)+"?"+query.Encode(), nil)
		require.NoError(t, requestErr)
		req.Header.Set("Test-Credential", "verified-after")
		req.Header.Set("Mcp-Session-Id", renewedSession)
		res, callErr := server.Client().Do(req)
		require.NoError(t, callErr)
		_, readErr := io.Copy(io.Discard, res.Body)
		require.NoError(t, readErr)
		require.NoError(t, res.Body.Close())
		return res.StatusCode
	}
	require.Equal(t, http.StatusConflict, bind("owner-client", "kept-report"))
	require.Equal(t, http.StatusForbidden, bind("foreign-client", "kept-report"))
	require.Equal(t, http.StatusOK, bind("owner-client", "fresh-report"))
	require.Equal(t, http.StatusConflict, bind("owner-client", "kept-report"), "opening a fresh instance must not rewrite the old binding")
	require.Zero(t, f.calls)
	// Revocation cannot be converted into another successful restoration.
	f.policies[0].Revision++
	f.policies[0].Policies[artifactcohort.PolicyAction] = authz.Policy{Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "administrator"}}
	f.replace(t)
	require.NoError(t, f.replica.Sync(after))
	_, err = bridge.ReportDefinition(after, &ui.ReportDefinitionInput{Resource: identity.ResourceRef{URI: original.Resource.URI}})
	require.Error(t, err)
	filtered, err = bridge.FilterResourceSnapshot(after, "reader", "owner-client", observed)
	require.NoError(t, err)
	require.NotContains(t, string(filtered), `"kept-report"`)
	require.Zero(t, f.calls)
}
