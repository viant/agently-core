package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	proto "github.com/viant/agently-core/protocol/skill"
)

func TestSkillListDefaultsToSevenWithoutBodies(t *testing.T) {
	registry := proto.NewRegistry()
	for i := 0; i < 12; i++ {
		registry.Add(&proto.Skill{Frontmatter: proto.Frontmatter{Name: fmt.Sprintf("example-%02d", i), Description: "Synthetic discovery entry"}, Body: "Instruction body must not be listed."}, nil)
	}
	service := &Service{registry: registry}
	ctx := WithRuntimeState(context.Background(), service, &agentmdl.Agent{Skills: []string{"*"}}, nil)
	result := &ListOutput{}
	require.NoError(t, service.list(ctx, &ListInput{}, result))
	require.Len(t, result.Items, 7)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "Instruction body")
	require.Equal(t, 12, result.Total)
	require.False(t, result.Complete)
	query := &ListInput{Cursor: result.NextCursor}
	require.NoError(t, service.list(ctx, query, result))
	require.Len(t, result.Items, 5)
	require.True(t, result.Complete)
	require.NoError(t, service.list(ctx, &ListInput{Limit: 3}, result))
	require.Len(t, result.Items, 3)
}
