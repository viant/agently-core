package sdk

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectRenderTurns_IncludesPlannerOnlyTurn(t *testing.T) {
	state := &ConversationState{
		ConversationID: "conv-1",
		Turns: []*TurnState{
			{
				TurnID: "turn-planner",
				Status: TurnStatusCompleted,
				Planner: &PlannerState{
					Status:         "failed",
					Trigger:        "low_confidence",
					StrategyFamily: "troubleshoot",
				},
			},
			{
				TurnID: "turn-empty",
				Status: TurnStatusCompleted,
			},
		},
	}

	got := SelectRenderTurns(state)
	require.Len(t, got, 1)
	require.Equal(t, "turn-planner", got[0].TurnID)
	require.NotNil(t, got[0].Planner)
}
