package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/agently-core/workspace"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	forgeTypes "github.com/viant/forge/backend/types"
	"github.com/viant/jsonrpc/transport/client/http/streamable"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type localFixtureSource struct {
	mu     sync.Mutex
	raw    json.RawMessage
	onRead func()
}

func (s *localFixtureSource) bytes() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.raw...)
}
func (s *localFixtureSource) Candidates(_ context.Context, uri identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	raw := s.bytes()
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(raw)}}, nil
}
func (s *localFixtureSource) ReadCandidate(_ context.Context, _ identity.ResourceURI, c identity.ResourceCandidate) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onRead != nil {
		s.onRead()
	}
	return append(json.RawMessage(nil), s.raw...), nil
}

type localFixturePolicy struct {
	actor         identity.VerifiedActor
	now           func() time.Time
	denyAfterRead *bool
	denyOmitted   bool
	seenRevision  *string
}

func (p localFixturePolicy) SelectRevision(_ context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if p.seenRevision != nil {
		*p.seenRevision = ref.Revision
	}
	if ref.Revision != "" && ref.Revision != identity.WorkingCandidate || ref.Revision == "" && p.denyOmitted || len(candidates) != 1 || candidates[0].Kind != identity.WorkingCandidate {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	if p.denyAfterRead != nil && *p.denyAfterRead {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "fixture-authority", ValidUntil: p.actor.ValidUntil}, nil
}
func localWindowBytes(t *testing.T) json.RawMessage {
	t.Helper()
	w := &forgeTypes.Window{View: forgeTypes.View{Content: &forgeTypes.Container{ID: "root"}}}
	v := forgeTypes.WindowResourceVariant{Window: w, DataSources: map[string]json.RawMessage{}}
	fp, err := forgeTypes.WindowVariantFingerprint(v)
	require.NoError(t, err)
	e := forgeTypes.WindowResourceEnvelope{SchemaVersion: 2, Format: forgeTypes.WindowBundleFormat, Targets: []forgeTypes.WindowTargetBinding{{Target: forgeTypes.WindowTarget{}, Variant: fp}}, Variants: map[string]forgeTypes.WindowResourceVariant{fp: v}}
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	require.NoError(t, e.Validate())
	return raw
}
func localFixture(t *testing.T) (*LocalProvider, *localFixtureSource, *identity.VerifiedActor, *bool) {
	t.Helper()
	actor := &identity.VerifiedActor{Subject: "alice", Issuer: "https://idp.example", TenantID: "team", AccountID: "acct-opaque", IdentityRevision: "facts-1", ValidUntil: time.Now().Add(time.Minute)}
	source := &localFixtureSource{raw: localWindowBytes(t)}
	revoked := new(bool)
	actorResolver := func(context.Context) (identity.VerifiedActor, error) { return *actor, nil }
	verify := func(_ context.Context, expected identity.VerifiedActor) error {
		if *revoked || expected.Subject != actor.Subject || expected.AccountID != actor.AccountID || !expected.Valid(time.Now()) {
			return identity.ErrResourceDenied
		}
		return nil
	}
	policyFactory := func(_ context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: "local-workspace", Source: source, Policy: localFixturePolicy{actor: a}}, nil
	}
	p, err := NewLocalProvider(LocalConfig{ProviderIdentity: "local-workspace", Actor: actorResolver, Verify: verify, Authorize: func(_ context.Context, _ identity.VerifiedActor, u identity.ResourceURI, _ string) error {
		if u.Namespace != "team" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: []LocalResourceBinding{{URI: "window://team/sales", Title: "Sales", FormatVersion: 2, Resolver: policyFactory}, {URI: "window://elsewhere/secret", Title: "Secret", FormatVersion: 2, Resolver: policyFactory}}, Validators: map[string]LocalResourceValidator{"window": ValidateWindowBundle}})
	require.NoError(t, err)
	return p, source, actor, revoked
}

func TestLocalProviderIsSingleWorkingCatalogAndValidatesBindings(t *testing.T) {
	p, _, _, _ := localFixture(t)
	got, err := p.Get(context.Background(), "window", primitive.GetRequest{URI: "window://team/sales"})
	require.NoError(t, err)
	require.Equal(t, "local-workspace", got.ResolvedResource.ProviderIdentity)
	require.Equal(t, identity.WorkingCandidate, got.Resource.Revision)
	require.Equal(t, int64(2), got.Resource.FormatVersion)
	_, err = p.Get(context.Background(), "window", primitive.GetRequest{URI: "window://team/sales", Revision: "3"})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	_, err = NewLocalProvider(LocalConfig{ProviderIdentity: "local", Actor: func(context.Context) (identity.VerifiedActor, error) { return identity.VerifiedActor{}, nil }, Verify: func(context.Context, identity.VerifiedActor) error { return nil }, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: []LocalResourceBinding{{URI: "window://team/a", FormatVersion: 2, Resolver: func(context.Context, identity.VerifiedActor, string) (*identity.ResourceResolver, error) {
		return nil, nil
	}}}})
	require.Error(t, err, "validator registration is mandatory")
}

func TestLocalProviderPreservesOmittedRevisionForHostSelection(t *testing.T) {
	p, source, _, _ := localFixture(t)
	seen := "not-called"
	binding := p.bindings["window://team/sales"]
	binding.Resolver = func(_ context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: p.identity, Source: source, Policy: localFixturePolicy{actor: a, denyOmitted: true, seenRevision: &seen}}, nil
	}
	p.bindings[binding.URI] = binding
	_, err := p.Get(context.Background(), "window", primitive.GetRequest{URI: binding.URI})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Equal(t, "", seen, "omitted selector must reach trusted host policy unchanged")
	seen = "not-called"
	result, err := p.Get(context.Background(), "window", primitive.GetRequest{URI: binding.URI, Revision: identity.WorkingCandidate})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, identity.WorkingCandidate, seen)
}

func TestLocalProviderBuffersAndRevalidatesNamespaceActorAndExactBytes(t *testing.T) {
	ctx := context.Background()
	t.Run("denied namespace never enters list or get", func(t *testing.T) {
		p, _, _, _ := localFixture(t)
		listed, err := p.List(ctx, "window", primitive.ListRequest{Namespace: "elsewhere"})
		require.NoError(t, err)
		require.Empty(t, listed.Resources)
		namespaces, err := p.NamespaceList(ctx, primitive.NamespaceListRequest{})
		require.NoError(t, err)
		require.Len(t, namespaces.Namespaces, 1)
		require.Equal(t, "team", namespaces.Namespaces[0].Name)
		_, err = p.Get(ctx, "window", primitive.GetRequest{URI: "window://elsewhere/secret"})
		require.ErrorIs(t, err, identity.ErrResourceDenied)
	})
	t.Run("source drift fails exact pin", func(t *testing.T) {
		p, source, _, _ := localFixture(t)
		source.mu.Lock()
		source.onRead = func() { source.raw = json.RawMessage(`{"schemaVersion":2,"format":"window.bundle","changed":true}`) }
		source.mu.Unlock()
		_, err := p.Get(ctx, "window", primitive.GetRequest{URI: "window://team/sales"})
		require.ErrorIs(t, err, identity.ErrResourceStale)
	})
	t.Run("identity revocation discards read", func(t *testing.T) {
		p, source, _, revoked := localFixture(t)
		source.mu.Lock()
		source.onRead = func() { *revoked = true }
		source.mu.Unlock()
		result, err := p.Get(ctx, "window", primitive.GetRequest{URI: "window://team/sales"})
		require.Nil(t, result)
		require.ErrorIs(t, err, identity.ErrResourceDenied)
	})
	t.Run("identity revocation discards buffered list", func(t *testing.T) {
		p, source, _, revoked := localFixture(t)
		source.mu.Lock()
		source.onRead = func() { *revoked = true }
		source.mu.Unlock()
		listed, err := p.List(ctx, "window", primitive.ListRequest{Namespace: "team"})
		require.Nil(t, listed)
		require.ErrorIs(t, err, identity.ErrResourceDenied)
	})
	t.Run("source lease expiring during read discards bytes", func(t *testing.T) {
		p, source, actor, _ := localFixture(t)
		source.mu.Lock()
		source.onRead = func() { actor.ValidUntil = time.Now().Add(-time.Second) }
		source.mu.Unlock()
		result, err := p.Get(ctx, "window", primitive.GetRequest{URI: "window://team/sales"})
		require.Nil(t, result)
		require.ErrorIs(t, err, identity.ErrResourceDenied)
	})
}

func TestLocalHTTPMCPToolsAdvertiseAndReadOnlyCapabilities(t *testing.T) {
	p, source, _, _ := localFixture(t)
	var pretty bytes.Buffer
	require.NoError(t, json.Indent(&pretty, source.bytes(), "", "  "))
	source.mu.Lock()
	source.raw = append(json.RawMessage(nil), pretty.Bytes()...)
	source.mu.Unlock()
	protected, err := NewMCPHandler(p, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer fixture" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), localTestRequest{}, true)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	require.NoError(t, err)
	server := httptest.NewServer(protected)
	defer server.Close()
	unauthorized, err := http.Post(server.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, unauthorized.StatusCode)
	_ = unauthorized.Body.Close()
	ctx := context.Background()
	httpClient := &http.Client{Transport: localAuthRoundTripper{next: http.DefaultTransport}}
	transport, err := streamable.New(ctx, server.URL+"/mcp", streamable.WithHTTPClient(httpClient), streamable.WithStateless(), streamable.WithProtocolVersion(schema.LatestProtocolVersion), streamable.WithRequestHeaderProvider(func(_ context.Context, body []byte, header http.Header) error {
		var request struct {
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return err
		}
		header.Set(schema.HeaderMethod, request.Method)
		if request.Method == schema.MethodToolsCall {
			var params struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(request.Params["name"], &params.Name); err != nil {
				return err
			}
			header.Set(schema.HeaderName, params.Name)
		}
		return nil
	}))
	require.NoError(t, err)
	cli := mcpclient.New("fixture", "1", transport, mcpclient.WithProtocolVersion(schema.LatestProtocolVersion))
	defer cli.Close()
	initialized, err := cli.Initialize(ctx)
	require.NoError(t, err)
	require.Equal(t, "local-workspace", initialized.Capabilities.Extensions[primitive.AuthoringExtension]["providerIdentity"])
	listedTools, err := cli.ListTools(ctx, nil)
	require.NoError(t, err)
	methods := map[string]schema.Tool{}
	for _, tool := range listedTools.Tools {
		methods[tool.Name] = tool
		require.Equal(t, "local-workspace", tool.Meta[primitive.AuthoringExtension].(map[string]interface{})["providerIdentity"])
	}
	require.Contains(t, methods, "namespaces/list")
	require.Contains(t, methods, "windows/list")
	require.Contains(t, methods, "windows/get")
	require.NotContains(t, methods, "windows/create")
	require.NotContains(t, methods, "windows/stamp")
	capabilities, err := cli.CallTool(ctx, &schema.CallToolRequestParams{Name: "namespaces/get", Arguments: map[string]interface{}{"namespace": "team"}})
	require.NoError(t, err)
	require.False(t, *capabilities.IsError)
	list, err := cli.CallTool(ctx, &schema.CallToolRequestParams{Name: "namespaces/list", Arguments: map[string]interface{}{}})
	require.NoError(t, err)
	require.False(t, *list.IsError)
	body, _ := json.Marshal(list.StructuredContent)
	var namespaceResult primitive.NamespaceListResult
	require.NoError(t, json.Unmarshal(body, &namespaceResult))
	require.Equal(t, "local-workspace", namespaceResult.ProviderIdentity)
	require.Equal(t, []string{"team"}, []string{namespaceResult.Namespaces[0].Name})
	read, err := cli.CallTool(ctx, &schema.CallToolRequestParams{Name: "windows/get", Arguments: map[string]interface{}{"uri": "window://team/sales"}})
	require.NoError(t, err)
	require.False(t, *read.IsError)
	var got primitive.GetResult
	encoded, _ := json.Marshal(read.StructuredContent)
	require.NoError(t, json.Unmarshal(encoded, &got))
	require.Equal(t, "window://team/sales", got.Resource.URI)
	require.Equal(t, "local-workspace", got.ResolvedResource.ProviderIdentity)
	require.Equal(t, []byte(source.bytes()), got.Resource.DefinitionBytes)
	windowList, err := cli.CallTool(ctx, &schema.CallToolRequestParams{Name: "windows/list", Arguments: map[string]interface{}{"namespace": "team"}})
	require.NoError(t, err)
	require.False(t, *windowList.IsError)
	denied, err := cli.CallTool(ctx, &schema.CallToolRequestParams{Name: "windows/get", Arguments: map[string]interface{}{"uri": "window://other/secret"}})
	require.NoError(t, err)
	require.True(t, *denied.IsError)
}

type localTestRequest struct{}
type localAuthRoundTripper struct{ next http.RoundTripper }

func (r localAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header = req.Header.Clone()
	copy.Header.Set("Authorization", "Bearer fixture")
	return r.next.RoundTrip(copy)
}

func TestLocalReportValidatorRequiresTypedDefinition(t *testing.T) {
	bad := json.RawMessage(`{"schemaVersion":1,"reportDocument":{},"format":"unknown"}`)
	require.Error(t, ValidateReportEnvelope(1, bad))
	require.Error(t, ValidateReportEnvelope(2, json.RawMessage(`{}`)))
	good := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Demo"},"reportSpec":{"version":1}}`)
	require.NoError(t, ValidateReportEnvelope(1, good))
}

func TestLocalProviderRejectsValidatorFailureBeforeReturningDefinition(t *testing.T) {
	p, source, _, _ := localFixture(t)
	source.mu.Lock()
	source.raw = json.RawMessage(`{"schemaVersion":2,"format":"window.bundle","targets":[],"variants":{}}`)
	source.mu.Unlock()
	result, err := p.Get(context.Background(), "window", primitive.GetRequest{URI: "window://team/sales"})
	require.Nil(t, result)
	require.Error(t, err)
}

func TestLocalProviderRequiresLiveActorLease(t *testing.T) {
	p, _, actor, _ := localFixture(t)
	actor.ValidUntil = time.Now().Add(-time.Second)
	result, err := p.Get(context.Background(), "window", primitive.GetRequest{URI: "window://team/sales"})
	require.Nil(t, result)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
}

func TestWorkspaceWindowBindingsUseCanonicalYAMLLoader(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, workspace.KindForgeWindow, "sales.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("windowKey: sales\nview:\n  content: {id: sales}\n"), 0600))
	actor := identity.VerifiedActor{Subject: "alice", Issuer: "https://idp", TenantID: "team", AccountID: "account", IdentityRevision: "r1", ValidUntil: time.Now().Add(time.Minute)}
	bindings, err := WorkspaceWindowBindings(root, "local-workspace", []workspacewindow.ResourceBinding{{WindowKey: "sales", URI: "window://team/sales"}}, func(_ context.Context, _ identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return localFixturePolicy{actor: actor}, nil
	}, nil)
	require.NoError(t, err)
	p, err := NewLocalProvider(LocalConfig{ProviderIdentity: "local-workspace", Actor: func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, Verify: func(context.Context, identity.VerifiedActor) error { return nil }, Authorize: func(_ context.Context, _ identity.VerifiedActor, u identity.ResourceURI, _ string) error {
		if u.Namespace != "team" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: bindings, Validators: map[string]LocalResourceValidator{"window": ValidateWindowBundle}})
	require.NoError(t, err)
	result, err := p.Get(context.Background(), "window", primitive.GetRequest{URI: "window://team/sales"})
	require.NoError(t, err)
	require.Equal(t, "window://team/sales", result.Resource.URI)
}
