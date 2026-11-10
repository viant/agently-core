package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/sdk/api"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/service/ui/window"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/artifactcohort"
	lookup "github.com/viant/forge/backend/lookup/contract"
	primitive "github.com/viant/forge/backend/provider/contract"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type starterLookupPublisher struct{ *artifactcohort.Publisher }

func (p starterLookupPublisher) Discover(_ context.Context) (artifactcohort.Manifest, error) {
	return p.Snapshot()
}

func (p starterLookupPublisher) Fetch(_ context.Context, input artifactcohort.FetchInput) (*artifactcohort.Artifact, error) {
	return p.Get(input.Root)
}

type starterLookupRoleKey struct{}
type starterLookupFacts struct{}

func (starterLookupFacts) Resolve(ctx context.Context) (authz.Facts, error) {
	role, _ := ctx.Value(starterLookupRoleKey{}).(string)
	return authz.Facts{Subject: role, Issuer: "fixture", Tenant: "fixture", Roles: []string{role}, ValidUntil: time.Now().Add(time.Hour)}, nil
}

func TestPublishedStarterLookupRegistryScopesAttachmentsToSelectedTask(t *testing.T) {
	ctx := t.Context()
	const studio = "studio-owner"
	const framework = "framework-owner"
	const libraryURI = "template://measurement/record-library"
	const pickerURI = "window://measurement/record-picker"
	const tasksURI = "startertask://measurement/default"

	actor, verify := resources.PublicReadActors()
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "extension", "forge"), 0700))
	chat := window.ResourceBinding{WindowKey: "chat/new", URI: "window://system/chat/new"}
	snapshot, err := resources.NewNativeAssetSnapshot(ctx, root, []resources.NativeSnapshotDefinition{
		{URI: chat.URI, Title: "Chat", File: "embedded/window", FormatVersion: 2, Load: resources.NativeComposedWindowLoader(chat, func(context.Context, *types.WindowTarget) (*types.Window, error) {
			return &types.Window{WindowKey: "chat/new", View: types.View{Content: &types.Container{ID: "chat", Chat: &types.Chat{}}}}, nil
		}, nil)},
		{URI: "lookup://system/ordinary", Title: "Ordinary", File: "ordinary.json", FormatVersion: 1, Load: func(context.Context, *resources.ExtensionReader, string) (json.RawMessage, error) {
			return json.RawMessage(`{"id":"ordinary","target":{"kind":"chat-composer"},"bindings":[{"lookup":{"dataSource":"records","windowId":"window://system/ordinary"},"named":{"name":"ordinary"}}]}`), nil
		}},
	})
	require.NoError(t, err)
	bindings, err := snapshot.Bindings(framework, func(_ context.Context, actor identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return lookupTestPolicy{actor}, nil
	})
	require.NoError(t, err)
	provider, err := resources.NewLocalProvider(resources.LocalConfig{
		ProviderIdentity: framework, Actor: actor, Verify: verify,
		Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil },
		Bindings:  bindings, Validators: map[string]resources.LocalResourceValidator{
			"lookup": resources.ValidateLookupDefinition, "window": resources.ValidateWindowBundle,
		},
	})
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, framework, &cfg.MCPClient{
		ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: framework,
	}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(provider) }))

	publisher := starterLookupPublisher{artifactcohort.NewPublisher()}
	publish := func(uri, kind string, version uint64, definition any) artifactcohort.RootRef {
		raw, err := json.Marshal(definition)
		require.NoError(t, err)
		artifact, _, err := publisher.Publish(ctx, artifactcohort.RootRef{URI: uri, Kind: kind, Version: version}, raw, func(context.Context, json.RawMessage) error { return nil })
		require.NoError(t, err)
		return artifact.Root
	}
	picker := publish(pickerURI, "window", 1, json.RawMessage(`{"schemaVersion":2,"title":"Record picker"}`))
	libraryDefinition := json.RawMessage(`{"schemaVersion":1,"lookupOverlays":[{"id":"record","target":{"kind":"chat-composer"},"bindings":[{"lookup":{"windowId":"window://measurement/record-picker"},"named":{"name":"record","required":true,"store":"${id}","display":"${name}","modelForm":"${name} (ID ${id})"}}]}]}`)
	library := publish(libraryURI, "template", 1, libraryDefinition)
	tasks := primitive.StarterTaskEnvelope{SchemaVersion: 1, Tasks: []primitive.StarterTask{
		{ID: "review", Title: "Review", Prompt: "Review /record.", LookupLibraries: []lookup.LibraryReference{{Resource: identity.ResourceRef{URI: libraryURI, Revision: "1"}, ProviderIdentity: studio, ContentFingerprint: library.Hash}}},
		{ID: "legacy", Title: "Legacy", Prompt: "Review available records."},
	}}
	selected := publish(tasksURI, primitive.StarterTaskKind, 1, tasks)
	role := authz.Resource{Tenant: "fixture", Kind: "cohort", ID: "reader", Version: "logical"}
	policy := authz.Document{Resource: role, Revision: 1, Policies: map[string]authz.Policy{artifactcohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}
	activate := func(roots ...artifactcohort.RootRef) {
		cohorts := []artifactcohort.Cohort{{Tenant: "fixture", Name: "readers", PolicyResource: role, Roots: roots}}
		_, err := publisher.ReplaceCatalog(ctx, []authz.Document{policy}, cohorts, publisher.Generation())
		require.NoError(t, err)
	}
	activate(selected, library, picker)
	replica, err := artifactcohort.NewReplica(artifactcohort.Config{Enabled: true, Source: studio, Tenant: "fixture", Account: "publisher", Caller: publisher, Facts: starterLookupFacts{}, SessionKey: func(ctx context.Context) (string, error) {
		role, _ := ctx.Value(starterLookupRoleKey{}).(string)
		return role, nil
	}})
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	source, err := artifactcohort.NewMetadataSource(replica, []string{"measurement"})
	require.NoError(t, err)
	gateway := resources.NewGateway(mgr, actor, verify, "consumer")
	t.Cleanup(gateway.Close)
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "studio-cohort", ProviderIdentity: studio}, []string{"measurement"}, source))
	backend := &backendClient{primitiveProviders: gateway}
	registry := func(role, task, fingerprint string, status int) api.ListLookupRegistryOutput {
		t.Helper()
		query := url.Values{"context": {"chat-composer:fixture"}, "owner": {chat.URI}, "providerIdentity": {framework}}
		if task != "" {
			query.Set("starterTaskId", tasksURI+"#"+task)
			query.Set("starterTaskFingerprint", fingerprint)
		}
		request := httptest.NewRequest(http.MethodGet, "/v1/api/lookups/registry?"+query.Encode(), nil)
		response := httptest.NewRecorder()
		handleListLookupRegistry(backend).ServeHTTP(response, request.WithContext(context.WithValue(ctx, starterLookupRoleKey{}, role)))
		require.Equal(t, status, response.Code, response.Body.String())
		var result api.ListLookupRegistryOutput
		if status == http.StatusOK {
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		}
		return result
	}

	ordinary := registry("reader", "", "", http.StatusOK)
	require.Len(t, ordinary.Entries, 1)
	require.Equal(t, "ordinary", ordinary.Entries[0].Name)
	require.Empty(t, ordinary.StarterEntries)

	withStarter := registry("reader", "review", selected.Hash, http.StatusOK)
	require.Equal(t, ordinary.Entries, withStarter.Entries)
	require.Len(t, withStarter.StarterEntries, 1)
	entry := withStarter.StarterEntries[0]
	require.True(t, entry.Required)
	require.Equal(t, "record", entry.Name)
	require.Equal(t, studio, entry.ProviderIdentity)
	require.Equal(t, pickerURI, entry.PickerResource.URI)
	require.Equal(t, "${name} (ID ${id})", entry.Token.ModelForm)
	templates, err := resources.NewTemplateReader(gateway).LoadAll(context.WithValue(ctx, starterLookupRoleKey{}, "reader"))
	require.NoError(t, err)
	require.Empty(t, templates, "a lookup library must not become an output template suggestion")
	registry("other", "review", selected.Hash, http.StatusForbidden)
	registry("reader", "legacy", selected.Hash, http.StatusForbidden)
	registry("reader", "missing", selected.Hash, http.StatusForbidden)
	registry("reader", "review", library.Hash, http.StatusForbidden)
	activate(selected, library)
	require.NoError(t, replica.Sync(ctx))
	registry("reader", "review", selected.Hash, http.StatusForbidden)
	activate(selected, picker)
	require.NoError(t, replica.Sync(ctx))
	registry("reader", "review", selected.Hash, http.StatusForbidden)
}
