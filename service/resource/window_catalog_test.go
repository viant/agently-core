package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	service "github.com/viant/agently-core/service/primitiveprovider"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
	"testing"
)

func TestDelegatedWindowCatalogUsesExactProviderAndTarget(t *testing.T) {
	p := provider("studio-a")
	p.definition = localWindowBytes(t)
	g, _, revoked := fixtureGateway(t, map[string]*fixtureProvider{"remote": p})
	proof, e := types.NewWindowTargetHMAC(bytes.Repeat([]byte{1}, 32))
	require.NoError(t, e)
	calls := 0
	catalog := &WindowCatalog{Gateway: g, TargetProof: proof, Admission: func(_ context.Context, pin identity.ResolvedResource, w *types.Window) error {
		calls++
		if *revoked || pin.ProviderIdentity != "studio-a" || w.View.Content.ID != "root" {
			return identity.ErrResourceDenied
		}
		return nil
	}}
	listed, e := catalog.List(context.Background(), nil)
	require.NoError(t, e)
	require.Len(t, listed.Windows, 1)
	input := &service.WindowDefinitionGetInput{WindowID: listed.Windows[0].WindowID}
	opened, e := catalog.Get(context.Background(), input)
	require.NoError(t, e)
	require.Equal(t, 2, calls)
	require.Equal(t, "studio-a", opened.Definition.Resource.ProviderIdentity)
	require.NotEmpty(t, opened.Definition.ResourceTarget.SelectionToken)
	input.ResolvedResource = opened.Definition.Resource
	input.Target = opened.Definition.ResourceTarget
	_, e = catalog.Get(context.Background(), input)
	require.NoError(t, e)
	forged := *input.ResolvedResource
	forged.ProviderIdentity = "studio-b"
	input.ResolvedResource = &forged
	_, e = catalog.Get(context.Background(), input)
	require.ErrorIs(t, e, identity.ErrResourceDenied)
	input.ResolvedResource = opened.Definition.Resource
	phone := *opened.Definition.ResourceTarget
	phone.FormFactor = "phone"
	input.Target = &phone
	_, e = catalog.Get(context.Background(), input)
	require.Error(t, e)
	input.Target = opened.Definition.ResourceTarget
	p.definition = json.RawMessage(`{"view":{"content":{"id":"changed"}}}`)
	_, e = catalog.Get(context.Background(), input)
	require.Error(t, e)
}
func TestDelegatedWindowCatalogDiscardsHostRevocation(t *testing.T) {
	p := provider("studio-a")
	p.definition = localWindowBytes(t)
	g, _, _ := fixtureGateway(t, map[string]*fixtureProvider{"remote": p})
	proof, e := types.NewWindowTargetHMAC(bytes.Repeat([]byte{1}, 32))
	require.NoError(t, e)
	calls := 0
	catalog := &WindowCatalog{Gateway: g, TargetProof: proof, Admission: func(context.Context, identity.ResolvedResource, *types.Window) error {
		calls++
		if calls == 2 {
			return identity.ErrResourceDenied
		}
		return nil
	}}
	rows, e := catalog.List(context.Background(), nil)
	require.NoError(t, e)
	result, e := catalog.Get(context.Background(), &service.WindowDefinitionGetInput{WindowID: rows.Windows[0].WindowID})
	require.ErrorIs(t, e, identity.ErrResourceDenied)
	require.Nil(t, result)
}

type localCatalogStub struct {
	summary service.WindowDefinitionSummary
}

func (localCatalogStub) AuthzReady() bool { return true }
func (s localCatalogStub) List(context.Context, *service.WindowDefinitionListInput) (*service.WindowDefinitionListOutput, error) {
	return &service.WindowDefinitionListOutput{Windows: []service.WindowDefinitionSummary{s.summary}}, nil
}
func (localCatalogStub) Get(context.Context, *service.WindowDefinitionGetInput) (*service.WindowDefinitionGetOutput, error) {
	return &service.WindowDefinitionGetOutput{WindowID: "local"}, nil
}
func TestCompositeWindowCatalogPreservesLocalAndRejectsConflictingURI(t *testing.T) {
	p := provider("studio-a")
	p.definition = localWindowBytes(t)
	g, _, _ := fixtureGateway(t, map[string]*fixtureProvider{"remote": p})
	proof, e := types.NewWindowTargetHMAC(bytes.Repeat([]byte{1}, 32))
	require.NoError(t, e)
	remote := &WindowCatalog{Gateway: g, TargetProof: proof, Admission: func(context.Context, identity.ResolvedResource, *types.Window) error { return nil }}
	local := localCatalogStub{service.WindowDefinitionSummary{WindowID: "local", ResourceURI: "window://local/home"}}
	composite := &CompositeWindowCatalog{Local: local, Remote: remote}
	rows, e := composite.List(context.Background(), nil)
	require.NoError(t, e)
	require.Len(t, rows.Windows, 2)
	result, e := composite.Get(context.Background(), &service.WindowDefinitionGetInput{WindowID: "local"})
	require.NoError(t, e)
	require.Equal(t, "local", result.WindowID)
	local.summary.ResourceURI = "window://example/sales"
	composite.Local = local
	_, e = composite.List(context.Background(), nil)
	require.ErrorIs(t, e, ErrCollision)
}
