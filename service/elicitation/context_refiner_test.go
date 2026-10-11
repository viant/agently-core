package elicitation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	preset "github.com/viant/agently-core/service/elicitation/refiner"
	"github.com/viant/mcp-protocol/schema"
)

func TestContextRefinerIsInstanceScopedAndPreservesFailure(t *testing.T) {
	type actorKey struct{}
	original := func() *schema.ElicitRequestParamsRequestedSchema {
		return &schema.ElicitRequestParamsRequestedSchema{Properties: map[string]interface{}{"account": map[string]interface{}{"type": "string"}}}
	}
	preset.SetOverlayHook(func(props map[string]interface{}) { props["legacy"] = true })
	defer preset.SetOverlayHook(nil)
	a, b := &Service{}, &Service{}
	for _, instance := range []*Service{a, b} {
		instance.ConfigureContextRefiner(func(ctx context.Context, rs *schema.ElicitRequestParamsRequestedSchema) error {
			actor, _ := ctx.Value(actorKey{}).(string)
			if actor == "" {
				return errors.New("missing declared owner")
			}
			preset.RefineWithHook(rs, func(props map[string]interface{}) { props["account"].(map[string]interface{})["owner"] = actor })
			return nil
		})
	}
	ra, rb := original(), original()
	require.NoError(t, a.RefineRequestedSchemaContext(context.WithValue(context.Background(), actorKey{}, "alice"), ra))
	require.NoError(t, b.RefineRequestedSchemaContext(context.WithValue(context.Background(), actorKey{}, "bob"), rb))
	require.Equal(t, "alice", ra.Properties["account"].(map[string]interface{})["owner"])
	require.Equal(t, "bob", rb.Properties["account"].(map[string]interface{})["owner"])
	require.NotContains(t, ra.Properties, "legacy")
	require.NotContains(t, rb.Properties, "legacy")
	require.Error(t, a.RefineRequestedSchemaContext(context.Background(), original()))
	legacy := original()
	require.NoError(t, (&Service{}).RefineRequestedSchemaContext(context.Background(), legacy))
	require.Contains(t, legacy.Properties, "legacy", "legacy signature remains backward compatible")
}
