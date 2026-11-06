package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	ws "github.com/viant/agently-core/workspace"
	shared "github.com/viant/forge/backend/resourcehost"
)

// Presentation values are synthetic inputs; trusted source ownership and the
// caller context remain fixed regardless of fields supplied in that input map.
func TestLayoutHTTPParametersStayWithinSelectedConfiguredApplication(t *testing.T) {
	previous := ws.Root()
	ws.SetRoot(t.TempDir())
	defer ws.SetRoot(previous)
	handler := NewMetadataHandler(nil, nil, "test")
	handler.SetAuthorizationRuntimes(nil, nil)
	handler.SetLayoutDefault([]byte(menuShell))
	type verifiedContextKey struct{}
	calls := 0
	handler.SetLayoutMenuReader(func(ctx context.Context) (*LayoutMenuRead, error) {
		calls++
		require.Equal(t, "verified-actor", ctx.Value(verifiedContextKey{}))
		selected := shared.MenuParameters(layoutMenuReadContext(ctx, "configured-producer", "selected"))
		require.Equal(t, json.Number("9007199254740993"), selected["entityId"])
		require.Equal(t, false, selected["enabled"])
		require.Contains(t, selected, "optional")
		require.Nil(t, selected["optional"])
		require.Equal(t, "untrusted", selected["sql"], "values remain opaque inputs, never executable SQL")
		require.Nil(t, shared.MenuParameters(layoutMenuReadContext(ctx, "configured-producer", "other")), "another application's context must not leak")
		require.Nil(t, shared.MenuParameters(layoutMenuReadContext(ctx, "other-producer", "selected")), "browser selector cannot substitute a producer")
		require.Nil(t, shared.MenuParameters(layoutMenuReadContext(ctx, "configured-producer", "")), "unlisted source cannot receive a different application's context")
		return &LayoutMenuRead{Applications: json.RawMessage(`[{"id":"selected","title":"Selected","menus":[]}]`), Revision: "context", Revalidate: func(release context.Context) error {
			require.Equal(t, "verified-actor", release.Value(verifiedContextKey{}))
			return nil
		}}, nil
	})
	parameters := `{"entityId":9007199254740993,"enabled":false,"optional":null,"sql":"untrusted","providerIdentity":"untrusted","authorityBinding":"untrusted"}`
	query := url.Values{"applicationId": {"selected"}, "providerIdentity": {"configured-producer"}, "parameters": {parameters}}
	request := httptest.NewRequest(http.MethodGet, "/v1/workspace/layout?"+query.Encode(), nil)
	request = request.WithContext(context.WithValue(request.Context(), verifiedContextKey{}, "verified-actor"))
	response := httptest.NewRecorder()
	handler.handleLayout()(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, calls)
	for _, invalid := range []string{`[]`, `"string"`, `{"x":`, `{} {}`} {
		query.Set("parameters", invalid)
		response = httptest.NewRecorder()
		handler.handleLayout()(response, httptest.NewRequest(http.MethodGet, "/v1/workspace/layout?"+query.Encode(), nil))
		require.Equal(t, http.StatusBadRequest, response.Code)
	}
	require.Equal(t, 1, calls, "malformed JSON never reaches configured menu readers")
}

func TestLayoutTypedParametersRequireExactApplicationAndProducer(t *testing.T) {
	ctx := context.WithValue(context.Background(), layoutMenuParametersKey{}, LayoutRequest{ApplicationID: "one", ProviderIdentity: "owned", Parameters: map[string]any{"entityId": 1}})
	require.Equal(t, map[string]any{"entityId": 1}, shared.MenuParameters(layoutMenuReadContext(ctx, "owned", "one")))
	require.Nil(t, shared.MenuParameters(layoutMenuReadContext(ctx, "owned", "two")))
	require.Nil(t, shared.MenuParameters(layoutMenuReadContext(ctx, "foreign", "one")))
}
