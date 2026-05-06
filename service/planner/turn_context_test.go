package planner

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type plannerQueryInputStub struct {
	ctx map[string]any
}

func (s *plannerQueryInputStub) GetContext() map[string]any {
	if s == nil {
		return nil
	}
	return s.ctx
}

func TestFromQueryInput(t *testing.T) {
	pc := &PlannerContext{
		Trigger:        TriggerCreativePhrase,
		Attempt:        1,
		StrategyFamily: "troubleshoot",
	}

	require.Nil(t, FromQueryInput(nil))
	require.Nil(t, FromQueryInput(&plannerQueryInputStub{}))
	require.Nil(t, FromQueryInput(&plannerQueryInputStub{ctx: map[string]any{ContextKey: "wrong-type"}}))
	require.Same(t, pc, FromQueryInput(&plannerQueryInputStub{ctx: map[string]any{ContextKey: pc}}))
}
