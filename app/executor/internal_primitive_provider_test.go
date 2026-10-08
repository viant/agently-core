package executor

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	resourcesvc "github.com/viant/agently-core/service/resource"
)

type internalMountPolicy struct{ actor identity.VerifiedActor }

func (p internalMountPolicy) SelectRevision(_ context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if ref.Revision != "" && ref.Revision != identity.WorkingCandidate || len(candidates) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: p.actor.IdentityRevision, ValidUntil: p.actor.ValidUntil}, nil
}

func TestBuilderMountsInternalMCPWithoutNetworkOrSelfProxy(t *testing.T) {
	ctx := context.Background()
	actor := identity.VerifiedActor{Subject: "alice", Issuer: "fixture", TenantID: "tenant", AccountID: "account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Minute)}
	var revoked atomic.Bool
	actorSource := func(context.Context) (identity.VerifiedActor, error) { return actor, nil }
	verify := func(context.Context, identity.VerifiedActor) error {
		if revoked.Load() {
			return identity.ErrResourceDenied
		}
		return nil
	}
	uri, err := identity.ParseResourceURI("template://platform/deliver/summary")
	require.NoError(t, err)
	local, err := resourcesvc.NewLocalProvider(resourcesvc.LocalConfig{
		ProviderIdentity: "internal", Actor: actorSource, Verify: verify,
		Authorize:  func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil },
		Validators: map[string]resourcesvc.LocalResourceValidator{"template": func(int64, json.RawMessage) error { return nil }},
		Bindings: []resourcesvc.LocalResourceBinding{{URI: uri.String(), FormatVersion: 1, Resolver: func(context.Context, identity.VerifiedActor, string) (*identity.ResourceResolver, error) {
			return &identity.ResourceResolver{ProviderIdentity: "internal", Policy: internalMountPolicy{actor}, Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{"native":"template"}`), nil }}}, nil
		}}},
	})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	runtime := &Runtime{MCPManager: mgr, LocalPrimitiveProvider: local}
	builder := NewBuilder().WithPrimitiveAuthority(actorSource, verify, "local-workspace").WithPrimitiveGatewayIdentity("gateway")
	require.NoError(t, builder.configurePrimitiveProviders(runtime))
	require.NoError(t, builder.configurePrimitiveProviders(runtime))
	defer runtime.PrimitiveProviders.Close()
	names, err := mgr.Names(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"internal"}, names)
	namespaces, err := runtime.PrimitiveProviders.Discover(ctx)
	require.NoError(t, err)
	require.Len(t, namespaces, 1)
	require.Equal(t, "internal", namespaces[0].Connection.Name)
	require.Equal(t, "internal", namespaces[0].Connection.ProviderIdentity)
	resources, err := runtime.PrimitiveProviders.List(ctx, "template", "platform")
	require.NoError(t, err)
	require.Len(t, resources, 1)
	require.Equal(t, uri.String(), resources[0].Resource.URI)
	revoked.Store(true)
	_, err = runtime.PrimitiveProviders.List(ctx, "template", "platform")
	require.Error(t, err)
}
