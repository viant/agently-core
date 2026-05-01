package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
)

// reuseConvStub returns a canned conversation. Used to feed
// previousUserMessage without spinning up the full store stack.
type reuseConvStub struct {
	conv *apiconv.Conversation
}

func (r *reuseConvStub) GetConversation(_ context.Context, _ string, _ ...apiconv.Option) (*apiconv.Conversation, error) {
	return r.conv, nil
}
func (r *reuseConvStub) GetConversations(_ context.Context, _ *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}
func (r *reuseConvStub) PatchConversations(_ context.Context, _ *apiconv.MutableConversation) error {
	return nil
}
func (r *reuseConvStub) GetPayload(_ context.Context, _ string) (*apiconv.Payload, error) {
	return nil, nil
}
func (r *reuseConvStub) PatchPayload(_ context.Context, _ *apiconv.MutablePayload) error {
	return nil
}
func (r *reuseConvStub) PatchMessage(_ context.Context, _ *apiconv.MutableMessage) error {
	return nil
}
func (r *reuseConvStub) GetMessage(_ context.Context, _ string, _ ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (r *reuseConvStub) GetMessageByElicitation(_ context.Context, _ string, _ string) (*apiconv.Message, error) {
	return nil, nil
}
func (r *reuseConvStub) PatchModelCall(_ context.Context, _ *apiconv.MutableModelCall) error {
	return nil
}
func (r *reuseConvStub) PatchTurn(_ context.Context, _ *apiconv.MutableTurn) error { return nil }
func (r *reuseConvStub) PatchToolCall(_ context.Context, _ *apiconv.MutableToolCall) error {
	return nil
}
func (r *reuseConvStub) DeleteMessage(_ context.Context, _ string, _ string) error {
	return nil
}
func (r *reuseConvStub) DeleteConversation(_ context.Context, _ string) error { return nil }

// makeReuseConv builds a conversation that has ONE prior turn attributed
// to priorAgent and containing priorUserMsg. The current turn's user
// message is passed separately to tryReuseFromPriorTurn — at the moment
// the workspace-intake reuse check fires, the new user message is not
// yet on the transcript.
func makeReuseConv(convID, priorAgent, priorUserMsg string) *apiconv.Conversation {
	priorAgentPtr := priorAgent
	contentPrior := priorUserMsg
	conv := &apiconv.Conversation{Id: convID}
	conv.Transcript = []*agconv.TranscriptView{
		{
			AgentIdUsed: &priorAgentPtr,
			Message: []*agconv.MessageView{
				{Role: "user", Type: "text", Content: &contentPrior},
				{Role: "assistant", Type: "text"},
			},
		},
	}
	return conv
}

// TestTryReuseFromPriorTurn covers the workspace-intake cross-turn reuse
// rule (intake-impt.md §8.2 / agent_resolution.go:tryReuseFromPriorTurn).
// Reuse fires only when ALL of: prior agent exists, prior agent is in
// authorized candidates, prior agent is not the synthetic capability
// responder, topic-shift divergence < 0.65.
func TestTryReuseFromPriorTurn(t *testing.T) {
	authorized := []*agentmdl.Agent{
		{Identity: agentmdl.Identity{ID: "steward"}},
		{Identity: agentmdl.Identity{ID: "analyst"}},
	}

	t.Run("does NOT reuse when topic shifted", func(t *testing.T) {
		// Prior: "forecast order 2652067", current: "what about line 887?"
		// Token sets are disjoint → divergence 1.0 ≥ 0.65 → no reuse.
		conv := makeReuseConv("c1", "steward", "forecast order 2652067")
		s := &Service{conversation: &reuseConvStub{conv: conv}}
		got := s.tryReuseFromPriorTurn(context.Background(), conv, "what about line 887?", authorized)
		assert.Nil(t, got, "high divergence should NOT reuse")
	})

	t.Run("reuses when query is genuinely similar", func(t *testing.T) {
		// Most tokens match → divergence small → reuse fires.
		conv := makeReuseConv("c1", "steward", "forecast order 2652067 audience coverage")
		s := &Service{conversation: &reuseConvStub{conv: conv}}
		got := s.tryReuseFromPriorTurn(context.Background(), conv,
			"forecast order 2652067 line 887 coverage", authorized)
		if assert.NotNil(t, got, "low divergence should trigger reuse") {
			assert.Equal(t, "steward", got.AgentID)
			assert.Equal(t, "reused", got.RoutingReason)
			assert.True(t, got.AutoSelected)
		}
	})

	t.Run("never reuses agent_selector (capability responder)", func(t *testing.T) {
		// Even with identical strings (zero divergence), capability turns
		// must reclassify fresh.
		conv := makeReuseConv("c1", "agent_selector", "what can you do here")
		s := &Service{conversation: &reuseConvStub{conv: conv}}
		got := s.tryReuseFromPriorTurn(context.Background(), conv, "what can you do here", authorized)
		assert.Nil(t, got, "capability turns must reclassify fresh; never reuse agent_selector")
	})

	t.Run("does not reuse unauthorized agent", func(t *testing.T) {
		conv := makeReuseConv("c1", "decommissioned-agent", "forecast order 2652067 audience coverage")
		s := &Service{conversation: &reuseConvStub{conv: conv}}
		got := s.tryReuseFromPriorTurn(context.Background(), conv,
			"forecast order 2652067 line 887 coverage", authorized)
		assert.Nil(t, got, "prior agent not in authorized list → no reuse")
	})

	t.Run("nil conv returns nil", func(t *testing.T) {
		s := &Service{}
		assert.Nil(t, s.tryReuseFromPriorTurn(context.Background(), nil, "x", authorized))
	})

	t.Run("first turn (empty transcript) returns nil", func(t *testing.T) {
		conv := &apiconv.Conversation{Id: "c1"}
		s := &Service{conversation: &reuseConvStub{conv: conv}}
		got := s.tryReuseFromPriorTurn(context.Background(), conv, "anything", authorized)
		assert.Nil(t, got)
	})
}

// TestResolveTurnRouting_RegressionParity locks in the contract that
// non-auto agentId resolution behaves identically to pre-workspace-intake
// behavior. Workspace intake's enrichments must NOT affect any path that
// already had a deterministic answer:
//
//   - explicit agentId returns immediately with reason="explicit"
//   - non-auto continuity returns reason="continuity"
//   - non-auto default returns reason="default"
//   - empty + missing default returns the existing "agent is required" error
//
// If any of these regress, downstream callers may see different routing
// outcomes for legacy code paths.
func TestResolveTurnRouting_RegressionParity(t *testing.T) {
	t.Run("explicit non-auto agent returns explicit reason", func(t *testing.T) {
		s := &Service{}
		dec, err := s.resolveTurnRouting(context.Background(), nil, "steward", "anything", "")
		if assert.NoError(t, err) && assert.NotNil(t, dec) {
			assert.Equal(t, "steward", dec.AgentID)
			assert.Equal(t, "explicit", dec.RoutingReason)
			assert.False(t, dec.AutoSelected)
			assert.Nil(t, dec.Preset, "explicit path must never carry a workspace-intake preset")
		}
	})

	t.Run("non-auto with prior continuity returns continuity reason", func(t *testing.T) {
		conv := makeReuseConv("c1", "analyst", "prior message")
		s := &Service{conversation: &reuseConvStub{conv: conv}}
		dec, err := s.resolveTurnRouting(context.Background(), conv, "", "", "")
		if assert.NoError(t, err) && assert.NotNil(t, dec) {
			assert.Equal(t, "analyst", dec.AgentID)
			assert.Equal(t, "continuity", dec.RoutingReason)
			assert.False(t, dec.AutoSelected)
		}
	})

	t.Run("missing agent + no default returns 'agent is required'", func(t *testing.T) {
		conv := &apiconv.Conversation{Id: "c1"}
		s := &Service{conversation: &reuseConvStub{conv: conv}}
		_, err := s.resolveTurnRouting(context.Background(), conv, "", "", "")
		assert.Error(t, err, "no agent + no default + no continuity must error")
		if err != nil {
			assert.Contains(t, err.Error(), "agent is required")
		}
	})
}
