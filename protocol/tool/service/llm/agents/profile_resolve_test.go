package agents

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	agentsvc "github.com/viant/agently-core/service/agent"
)

func TestResolveProfile_CopiesPromptProfileIDToChildQueryInput(t *testing.T) {
	svc := &Service{}
	runInput := &RunInput{PromptProfileId: "diagnostic_baseline"}
	queryInput := &agentsvc.QueryInput{}

	err := svc.resolveProfile(context.Background(), runInput, queryInput, "child-convo")
	require.NoError(t, err)
	require.Equal(t, "diagnostic_baseline", queryInput.PromptProfileId)
}
