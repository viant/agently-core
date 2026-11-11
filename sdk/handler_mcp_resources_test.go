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
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/artifactcohort"
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

type publishedResourceCaller struct{ publisher *artifactcohort.Publisher }

func (c publishedResourceCaller) Discover(context.Context) (artifactcohort.Manifest, error) {
	return c.publisher.PublicSnapshot()
}
func (c publishedResourceCaller) Fetch(_ context.Context, input artifactcohort.FetchInput) (*artifactcohort.Artifact, error) {
	return c.publisher.FetchPublic(input)
}

type forbiddenResourceFacts struct{}

func (forbiddenResourceFacts) Resolve(context.Context) (authz.Facts, error) {
	return authz.Facts{}, errors.New("public resource must not evaluate recipient roles")
}

func TestMCPResourceHTTPReadsAdmittedPublicationWithoutConfiguredTransport(t *testing.T) {
	ctx := context.Background()
	calls := 0
	mgr, err := manager.New(&mcpUIResourceProviderStub{}, manager.WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) {
		calls++
		return nil, errors.New("publication must not use remote metadata")
	}))
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	defer gateway.Close()
	publisher := artifactcohort.NewPublisher()
	raw := json.RawMessage(`{ "schemaVersion": 1, "css": ".surface { color: purple; }" }`)
	artifact, _, err := publisher.Publish(ctx, artifactcohort.RootRef{URI: "style://global/workspace", Kind: "style", Version: 1}, raw, func(context.Context, json.RawMessage) error { return nil })
	require.NoError(t, err)
	_, err = publisher.ReplaceCatalog(ctx, nil, []artifactcohort.Cohort{{Tenant: "tenant", Name: "global", Public: true, Roots: []artifactcohort.RootRef{artifact.Root}}}, publisher.Generation())
	require.NoError(t, err)
	replica, err := artifactcohort.NewReplica(artifactcohort.Config{Enabled: true, Source: "publisher", Tenant: "tenant", Account: "owner", Caller: publishedResourceCaller{publisher}, Facts: forbiddenResourceFacts{}, SessionKey: func(context.Context) (string, error) {
		return "", errors.New("public resource must not require a recipient session")
	}})
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	source, err := artifactcohort.NewMetadataSource(replica, []string{"global"})
	require.NoError(t, err)
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "catalog#global", ProviderIdentity: "publisher"}, []string{"global"}, source))
	backend := &backendClient{mcpMgr: mgr, primitiveProviders: gateway}
	request := httptest.NewRequest(http.MethodGet, "/v1/api/mcp/resources/read?server=catalog%23global&uri="+url.QueryEscape(artifact.Root.URI), nil)
	response := httptest.NewRecorder()
	handleMCPResourceRead(backend)(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var result schema.ReadResourceResult
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Len(t, result.Contents, 1)
	require.Equal(t, string(artifact.Payload), result.Contents[0].Text)
	_, err = backend.ReadMCPResource(ctx, "unrelated", artifact.Root.URI)
	require.Error(t, err, "wrong owner cannot fall through to a transport")
	replica.MarkDisconnected()
	_, err = backend.ReadMCPResource(ctx, "catalog#global", artifact.Root.URI)
	require.ErrorIs(t, err, artifactcohort.ErrUnavailable)
	require.NotErrorIs(t, err, identity.ErrResourceDenied)
	require.Zero(t, calls)
}
