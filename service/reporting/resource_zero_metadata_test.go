package reporting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/reporting/registry"
	"github.com/viant/forge/backend/types"
)

func TestReportDependencyProofZeroMetadataPreservesFiniteNarrowing(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s := New(Options{Now: func() time.Time { return now }, Store: NewMemoryStore()})
	signer, err := types.NewWindowTargetHMAC(make([]byte, 32))
	require.NoError(t, err)
	child := identity.ResolvedResource{ProviderIdentity: "data", URI: "datasource://platform/orders", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("descriptor"))}, AuthorityBinding: "child"}
	parent := identity.ResolvedResource{ProviderIdentity: "reports", URI: "report://platform/orders", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("report"))}, AuthorityBinding: "parent"}
	definition := registry.ReportEnvelope{DataSourceResources: map[string]primitive.DataSourceReference{"orders": {Resource: identity.ResourceRef{URI: child.URI, Revision: child.Selector()}, ProviderIdentity: child.ProviderIdentity, ContentFingerprint: child.ContentFingerprint}}}
	s.SetResourceDependencies(func(context.Context, identity.ResolvedResource, map[string]primitive.DataSourceReference, map[string]json.RawMessage, map[string]identity.ResolvedResource) (map[string]identity.ResolvedResource, error) {
		return map[string]identity.ResolvedResource{"orders": child}, nil
	}, signer)
	pins, token, err := s.bindDependencies(ctx, &parent, definition, nil, "")
	require.NoError(t, err)
	require.True(t, parent.ValidUntil.IsZero())
	require.NoError(t, s.verifyDependencies(ctx, parent, definition, pins, token))
	original := parent
	narrowed := parent
	narrowed.ValidUntil = now.Add(time.Minute)
	held, rebound, err := s.bindOperationDependencies(ctx, &original, &narrowed, definition, pins, token)
	require.NoError(t, err)
	require.Equal(t, now.Add(time.Minute), narrowed.ValidUntil)
	require.True(t, held["orders"].ValidUntil.IsZero())
	require.NoError(t, signer.Verify(ctx, narrowed, types.WindowTarget{DependencyPins: held}, reportDependencyDomain, rebound))
	unlimited := narrowed
	unlimited.ValidUntil = time.Time{}
	_, _, err = s.bindOperationDependencies(ctx, &narrowed, &unlimited, definition, held, rebound)
	require.Error(t, err, "event-bound metadata cannot remove an already finite constraint")
	require.True(t, exportPinLeaseValid(&original, now))
	expired := original
	expired.ValidUntil = now.Add(-time.Second)
	require.False(t, exportPinLeaseValid(&expired, now))
}
