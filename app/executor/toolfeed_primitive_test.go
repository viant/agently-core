package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	api "github.com/viant/agently-core/sdk/api"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace/codec"
	"github.com/viant/mcp-protocol/schema"
)

func TestRuntimeToolfeedDefinitionsThroughMCPGateway(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	directory := filepath.Join(root, "feeds", "tools")
	require.NoError(t, os.MkdirAll(directory, 0700))
	file := filepath.Join(directory, "terminal.yaml")
	original := []byte("id: legacy-terminal\ntitle: Terminal\nmatch:\n  service: system/exec\n  method: execute\ndataSource:\n  output:\n    source: output\nui:\n  type: container\n")
	require.NoError(t, os.WriteFile(file, original, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "plan.yml"), []byte("match:\n  service: orchestration/plan\n  method: update\n"), 0600))
	var legacy api.FeedSpec
	require.NoError(t, codec.DecodeFile(file, &legacy))
	actor := identity.VerifiedActor{Subject: "alice", Issuer: "fixture", TenantID: "team", AccountID: "account", IdentityRevision: "r1", ValidUntil: time.Now().Add(time.Minute)}
	revoked := false
	actorSource := func(context.Context) (identity.VerifiedActor, error) { return actor, nil }
	verify := func(context.Context, identity.VerifiedActor) error {
		if revoked {
			return identity.ErrResourceDenied
		}
		return nil
	}
	provider, err := resources.NewInternalProvider(ctx, root, []resources.ExtensionDirectory{resources.ToolfeedDirectory("user-alice", "feeds")}, func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return internalMountPolicy{a}, nil
	}, resources.LocalConfig{
		Actor: actorSource, Verify: verify,
		Authorize: func(_ context.Context, _ identity.VerifiedActor, u identity.ResourceURI, _ string) error {
			if u.Namespace != "user-alice" {
				return identity.ErrResourceDenied
			}
			return nil
		},
		Validators: map[string]resources.LocalResourceValidator{primitive.ToolfeedKind: resources.ValidateToolfeedDefinition},
	})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	runtime := &Runtime{MCPManager: mgr, LocalPrimitiveProvider: provider}
	builder := NewBuilder().WithPrimitiveAuthority(actorSource, verify, "internal").WithPrimitiveGatewayIdentity("gateway")
	require.NoError(t, builder.configurePrimitiveProviders(runtime))
	defer runtime.PrimitiveProviders.Close()
	client, err := mgr.Get(ctx, "", "internal")
	require.NoError(t, err)
	tools, err := client.ListTools(ctx, nil)
	require.NoError(t, err)
	names := []string{}
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	require.Contains(t, names, "toolfeeds/list")
	require.Contains(t, names, "toolfeeds/get")
	namespaces, err := runtime.PrimitiveProviders.Discover(ctx)
	require.NoError(t, err)
	require.Len(t, namespaces, 1)
	require.Equal(t, primitive.ToolfeedSchemaURI, namespaces[0].Kinds[0].SchemaURI)
	require.Equal(t, "toolfeeds/get", namespaces[0].Kinds[0].Methods["get"])
	listed, err := runtime.PrimitiveProviders.List(ctx, primitive.ToolfeedKind, "user-alice")
	require.NoError(t, err)
	require.Len(t, listed, 2)
	var uri string
	for _, row := range listed {
		if row.Resource.Name == "tools/terminal" {
			uri = row.Resource.URI
		}
	}
	require.Equal(t, "toolfeed://user-alice/tools/terminal", uri)
	connection := namespaces[0].Connection
	got, err := runtime.PrimitiveProviders.Get(ctx, connection, identity.ResourceRef{URI: uri}, nil)
	require.NoError(t, err)
	var projection api.FeedSpec
	require.NoError(t, json.Unmarshal(got.Resource.Definition, &projection))
	require.Equal(t, legacy, projection)
	require.Equal(t, identity.WorkingCandidate, got.ResolvedResource.Kind)
	require.Empty(t, got.ResolvedResource.Revision)
	fallback, err := runtime.PrimitiveProviders.Get(ctx, connection, identity.ResourceRef{URI: "toolfeed://user-alice/tools/plan", Revision: "working"}, nil)
	require.NoError(t, err)
	def, err := primitive.DecodeToolfeedDefinition(1, fallback.Resource.Definition)
	require.NoError(t, err)
	require.Equal(t, "plan", def.ID)
	for _, ref := range []identity.ResourceRef{{URI: uri, Revision: "1"}, {URI: "toolfeed://user-bob/tools/terminal"}, {URI: "window://user-alice/tools/terminal"}} {
		_, err := runtime.PrimitiveProviders.Get(ctx, connection, ref, nil)
		require.Error(t, err)
	}
	failed, err := client.CallTool(ctx, &schema.CallToolRequestParams{Name: "toolfeeds/get", Arguments: map[string]interface{}{"uri": uri, "feedId": "legacy-terminal"}})
	require.NoError(t, err)
	require.NotNil(t, failed.IsError)
	require.True(t, *failed.IsError)
	require.NoError(t, os.WriteFile(file, []byte("id: legacy-terminal\ntitle: Changed\nmatch:\n  service: system/exec\n"), 0600))
	_, err = runtime.PrimitiveProviders.Get(ctx, connection, identity.ResourceRef{URI: uri}, got.ResolvedResource)
	require.Error(t, err)
	fresh, err := runtime.PrimitiveProviders.Get(ctx, connection, identity.ResourceRef{URI: uri, Revision: "working"}, nil)
	require.NoError(t, err)
	require.NotEqual(t, got.ResolvedResource.ContentFingerprint, fresh.ResolvedResource.ContentFingerprint)
	expiredPin := *fresh.ResolvedResource
	expiredPin.ValidUntil = time.Now().Add(-time.Second)
	_, err = runtime.PrimitiveProviders.Get(ctx, connection, identity.ResourceRef{URI: uri}, &expiredPin)
	require.Error(t, err)
	actor.IdentityRevision = "r2"
	_, err = runtime.PrimitiveProviders.Get(ctx, connection, identity.ResourceRef{URI: uri}, fresh.ResolvedResource)
	require.Error(t, err)
	actor.IdentityRevision = "r1"
	revoked = true
	_, err = runtime.PrimitiveProviders.List(ctx, primitive.ToolfeedKind, "user-alice")
	require.Error(t, err)
	_, err = runtime.PrimitiveProviders.Get(ctx, connection, identity.ResourceRef{URI: uri}, nil)
	require.Error(t, err)
	revoked = false
	actor.ValidUntil = time.Now().Add(-time.Second)
	_, err = runtime.PrimitiveProviders.Get(ctx, connection, identity.ResourceRef{URI: uri}, nil)
	require.Error(t, err)
}
