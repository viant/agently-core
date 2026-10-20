package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

type authenticatedResourceClient struct {
	*mcpUIResourceClientStub
	tokens []string
}

func (c *authenticatedResourceClient) ReadResource(ctx context.Context, p *schema.ReadResourceRequestParams, _ ...mcpclient.RequestOption) (*schema.ReadResourceResult, error) {
	token, _ := ctx.Value(authtransport.ContextAuthTokenKey).(string)
	c.tokens = append(c.tokens, token)
	return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Uri: p.Uri, Blob: "AA=="}}}, nil
}
func TestMCPResourceReadForwardsCurrentOAuthTokenOnEveryCall(t *testing.T) {
	remote := &authenticatedResourceClient{mcpUIResourceClientStub: &mcpUIResourceClientStub{}}
	mgr, err := manager.New(&mcpUIResourceProviderStub{}, manager.WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) { return remote, nil }))
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	backend := &backendClient{mcpMgr: mgr}
	for _, token := range []string{"first-access-token", "refreshed-access-token"} {
		_, err = backend.ReadMCPResource(authctx.WithBearer(context.Background(), token), "studio", "resource-archive://demo/dialogs.tar.gz")
		require.NoError(t, err)
	}
	require.Equal(t, []string{"first-access-token", "refreshed-access-token"}, remote.tokens)
}

type resourceReadFunc func(context.Context, string, string) (*schema.ReadResourceResult, error)

func (f resourceReadFunc) ReadMCPResource(ctx context.Context, server, uri string) (*schema.ReadResourceResult, error) {
	return f(ctx, server, uri)
}

func TestMCPResourceReadPreservesArchiveAndContext(t *testing.T) {
	type contextKey struct{}
	mimeType := "application/gzip"
	want := &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Uri: "resource-archive://demo/dialogs.tar.gz", MimeType: &mimeType, Blob: "H4sIAAAAAAAA"}}}
	reader := resourceReadFunc(func(ctx context.Context, server, uri string) (*schema.ReadResourceResult, error) {
		require.Equal(t, "current-user", ctx.Value(contextKey{}))
		require.Equal(t, "studio", server)
		require.Equal(t, want.Contents[0].Uri, uri)
		return want, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/api/mcp/resources/read?server=studio&uri="+url.QueryEscape(want.Contents[0].Uri), nil).WithContext(context.WithValue(context.Background(), contextKey{}, "current-user"))
	w := httptest.NewRecorder()
	handleMCPResourceRead(reader)(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var got struct {
		Contents []struct {
			URI  string `json:"uri"`
			MIME string `json:"mimeType"`
			Blob string `json:"blob"`
		} `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Contents, 1)
	require.Equal(t, want.Contents[0].Uri, got.Contents[0].URI)
	require.Equal(t, mimeType, got.Contents[0].MIME)
	require.Equal(t, want.Contents[0].Blob, got.Contents[0].Blob)
}

func TestMCPResourceReadRejectsMissingLocatorAndPropagatesFailure(t *testing.T) {
	calls := 0
	reader := resourceReadFunc(func(context.Context, string, string) (*schema.ReadResourceResult, error) {
		calls++
		return nil, errors.New("provider unavailable")
	})
	for _, query := range []string{"?server=studio", "?uri=resource://demo/a"} {
		w := httptest.NewRecorder()
		handleMCPResourceRead(reader)(w, httptest.NewRequest(http.MethodGet, "/read"+query, nil))
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	require.Zero(t, calls)
	w := httptest.NewRecorder()
	handleMCPResourceRead(reader)(w, httptest.NewRequest(http.MethodGet, "/read?server=studio&uri=resource://demo/a", nil))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, 1, calls)
}
