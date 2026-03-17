package agent

import (
	"context"
)

// maybeForceInitialRepoAnalysisDelegation previously bypassed the reactor/LLM
// to directly execute llm/agents:run for repo analysis requests. This broke
// the streaming event pipeline (no model_started/model_completed, no execution
// groups for tool steps). Removed — the normal reactor flow handles delegation
// correctly via the LLM planning llm/agents:run as a tool call.
func (s *Service) maybeForceInitialRepoAnalysisDelegation(ctx context.Context, input *QueryInput, output *QueryOutput) (bool, error) {
	return false, nil
}
