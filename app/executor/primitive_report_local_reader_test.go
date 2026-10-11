package executor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	reporting "github.com/viant/agently-core/service/reporting"
	catalog "github.com/viant/agently-core/service/reporting/catalog"
)

type localReaderRemote struct{ calls int }

func (r *localReaderRemote) Resolve(context.Context, identity.ResourceRef) (*identity.ResolvedResource, error) {
	r.calls++
	return nil, identity.ErrResourceDenied
}
func (r *localReaderRemote) ReadResolved(context.Context, identity.ResolvedResource) (json.RawMessage, *identity.ResolvedResource, error) {
	r.calls++
	return nil, nil, identity.ErrResourceDenied
}
func (r *localReaderRemote) ResolveForProvider(context.Context, identity.ResourceRef, string) (*identity.ResolvedResource, error) {
	r.calls++
	return nil, identity.ErrResourceDenied
}

type localReaderPolicy struct{ calls *int }

func (p localReaderPolicy) SelectRevision(context.Context, identity.ResourceRef, []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	*p.calls++
	return identity.ResourceDecision{}, identity.ErrResourceDenied
}

func TestPrimitiveLegacyLocalOwnershipPrecedesDeniedActionWithoutForeignFallback(t *testing.T) {
	ctx := context.Background()
	uri, _ := identity.ParseResourceURI("report://owned/local")
	actor := identity.VerifiedActor{Subject: "owner", Issuer: "issuer", TenantID: "tenant", AccountID: "account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Hour)}
	inventory := []catalog.ReportCatalogCandidate{{URI: uri.String(), Title: "Local"}}
	local := &catalog.ReportCatalogService{Identity: func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, Inventories: []catalog.ReportInventory{catalog.ReportInventoryFunc(func(context.Context, identity.VerifiedActor) ([]catalog.ReportCatalogCandidate, error) {
		return inventory, nil
	})}}
	remote := &localReaderRemote{}
	localCalls := 0
	reader := &primitiveReportLocalReader{local: local, remote: remote, operation: "report.execute", resolve: func(context.Context, string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }}, Policy: localReaderPolicy{&localCalls}}, nil
	}}
	_, err := reader.Resolve(ctx, identity.ResourceRef{URI: uri.String()})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Equal(t, 1, localCalls)
	require.Zero(t, remote.calls, "local action denial never routes the same URI to a foreign provider")
	_, err = reader.Resolve(ctx, identity.ResourceRef{URI: "report://external/other"})
	require.Error(t, err)
	require.Equal(t, 1, remote.calls)
	inventory = append(inventory, inventory[0])
	_, err = reader.Resolve(ctx, identity.ResourceRef{URI: uri.String()})
	require.Error(t, err)
	require.Equal(t, 1, localCalls)
	require.Equal(t, 1, remote.calls, "ambiguous local ownership cannot dispatch")
}

var _ reporting.ProviderResourceReader = (*primitiveReportLocalReader)(nil)
