package layoutgate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
)

type menuFacts struct{ roles []string }

func (r *menuFacts) Resolve(context.Context, *permittedview.Request) (*permittedview.Snapshot, error) {
	return &permittedview.Snapshot{AuthorizationVersion: "original", ExpiresAt: time.Now().Add(time.Minute), Principal: map[string]any{"roles": r.roles}}, nil
}

func TestMenuAncestryUsesOriginalGateForIdenticalActions(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":1,"applications":[{"id":"public","menus":[{"id":"public-action","action":{"type":"window","windowKey":"same","parameters":{"opaque":"unchanged"}}}]},{"id":"protected","authorization":{"resourceType":"application"},"visibleWhen":{"source":"authorization","field":"principal.roles","contains":"reader"},"menus":[{"id":"protected-action","action":{"type":"window","windowKey":"same","parameters":{"opaque":"unchanged"}}}]}]}`)
	project := MenuProjection(nil, nil)
	visible, err := project(context.Background(), raw)
	require.NoError(t, err)
	require.Contains(t, string(visible), `"public-action"`)
	require.NotContains(t, string(visible), `"protected-action"`)
	facts := &menuFacts{}
	project = MenuProjection(&permittedview.Runtime{Resolver: facts}, nil)
	visible, err = project(context.Background(), raw)
	require.NoError(t, err)
	require.NotContains(t, string(visible), `"protected-action"`)
	facts.roles = []string{"reader"}
	visible, err = project(context.Background(), raw)
	require.NoError(t, err)
	require.Contains(t, string(visible), `"protected-action"`)
	require.Contains(t, string(visible), `"opaque":"unchanged"`)
	facts.roles = nil
	visible, err = project(context.Background(), raw)
	require.NoError(t, err)
	require.NotContains(t, string(visible), `"protected-action"`)
}

type menuWindowPolicy struct{ calls int }

func (p *menuWindowPolicy) Resolve(context.Context, *policy.Request) (*policy.Decision, error) {
	p.calls++
	return &policy.Decision{PolicyVersion: "original", ExpiresAt: time.Now().Add(time.Minute), Allow: true, AllowedIDs: []string{"one"}}, nil
}
func TestMenuProjectionBatchesOriginalWindowPolicy(t *testing.T) {
	source := json.RawMessage(`{"schemaVersion":1,"applications":[{"id":"app","menus":[{"id":"one","title":"One","action":{"type":"window","windowKey":"ONE"}},{"id":"two","title":"Two","action":{"type":"window","windowKey":"two"}}]}]}`)
	resolver := &menuWindowPolicy{}
	runtime := policy.NewRuntime(resolver, policy.OperationWindowView)
	got, err := MenuProjection(nil, runtime)(context.Background(), source)
	require.NoError(t, err)
	require.Equal(t, 1, resolver.calls)
	require.Contains(t, string(got), `"windowKey":"ONE"`)
	require.NotContains(t, string(got), `"windowKey":"two"`)
	runtime.ExactIDs = true
	got, err = MenuProjection(nil, runtime)(context.Background(), source)
	require.NoError(t, err)
	require.NotContains(t, string(got), `"windowKey":"ONE"`)
}
