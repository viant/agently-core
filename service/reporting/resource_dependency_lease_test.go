package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/reporting/registry"
	"github.com/viant/forge/backend/types"
)

func TestNarrowedReportOperationProofPreservesOriginalChildAuthority(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	first := now
	s := New(Options{Now: func() time.Time { return now }, Store: NewMemoryStore()})
	proof, err := types.NewWindowTargetHMAC(bytes.Repeat([]byte{11}, 32))
	require.NoError(t, err)
	child := identity.ResolvedResource{ProviderIdentity: "library", URI: "datasource://platform/orders", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("descriptor"))}, AuthorityBinding: "original-child", ValidUntil: now.Add(30 * time.Second)}
	parent := identity.ResolvedResource{ProviderIdentity: "reports", URI: "report://platform/orders", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("definition"))}, AuthorityBinding: "original-parent", ValidUntil: now.Add(time.Minute)}
	definition := registry.ReportEnvelope{DataSourceResources: map[string]primitive.DataSourceReference{"orders": {Resource: identity.ResourceRef{URI: child.URI, Revision: child.Selector()}, ProviderIdentity: child.ProviderIdentity, ContentFingerprint: child.ContentFingerprint}}}
	reselected := 0
	s.SetResourceDependencies(func(_ context.Context, _ identity.ResolvedResource, _ map[string]primitive.DataSourceReference, _ map[string]json.RawMessage, held map[string]identity.ResolvedResource) (map[string]identity.ResolvedResource, error) {
		if held == nil {
			reselected++
		}
		return map[string]identity.ResolvedResource{"orders": child}, nil
	}, proof)
	pins, token, err := s.bindDependencies(ctx, &parent, definition, nil, "")
	require.NoError(t, err)
	require.Equal(t, child.ValidUntil, parent.ValidUntil)
	original := parent
	narrowed := parent
	narrowed.ValidUntil = first.Add(10 * time.Second)
	// A fresh child response may have a longer lease, but the original child
	// is retained when a host action narrows the parent operation deadline.
	child.ValidUntil = first.Add(time.Minute)
	carried, rebound, err := s.bindOperationDependencies(ctx, &original, &narrowed, definition, pins, token)
	require.NoError(t, err)
	require.Equal(t, 1, reselected)
	require.Equal(t, pins["orders"], carried["orders"])
	require.Equal(t, first.Add(10*time.Second), narrowed.ValidUntil)
	require.NoError(t, proof.Verify(ctx, narrowed, types.WindowTarget{DependencyPins: carried}, reportDependencyDomain, rebound))
	require.Error(t, proof.Verify(ctx, narrowed, types.WindowTarget{DependencyPins: carried}, reportDependencyDomain, token))
	child.AuthorityBinding = "revoked-child"
	_, _, err = s.bindOperationDependencies(ctx, &original, &narrowed, definition, pins, token)
	require.Error(t, err)
	child.AuthorityBinding = "original-child"
	now = original.ValidUntil.Add(time.Nanosecond)
	_, _, err = s.bindOperationDependencies(ctx, &original, &narrowed, definition, pins, token)
	require.Error(t, err, "fresh child lease must not renew original expired authority")
}
