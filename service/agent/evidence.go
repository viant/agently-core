package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/viant/agently-core/runtime/evidence"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

// WithEvidenceFactory is opt-in host composition. Existing/historical turns are
// unchanged when no factory is registered.
func WithEvidenceFactory(factory evidence.Factory) Option {
	return func(s *Service) { s.evidenceFactory = factory }
}

func (s *Service) captureEvidence(ctx context.Context, input *QueryInput, at time.Time) (evidence.Pending, error) {
	if s.evidenceFactory == nil || input == nil {
		return nil, nil
	}
	raw, err := json.Marshal(input.Context)
	if err != nil {
		return nil, err
	}
	return s.evidenceFactory.Capture(ctx, evidence.Input{Context: raw, ReceivedAt: at, Nested: input.ParentConversationID != "" || requestctx.IsInternalHelperAgentID(input.AgentID)})
}

func evidenceTurn(ctx context.Context, turn requestctx.TurnMeta) evidence.Turn {
	lease := runLeaseFromContext(ctx)
	return evidence.Turn{ConversationID: turn.ConversationID, TurnID: turn.TurnID, StarterMessageID: turn.ParentMessageID, LeaseOwner: func() string {
		if lease == nil || !lease.Active() {
			return ""
		}
		return lease.Owner()
	}}
}

func (s *Service) restoreEvidence(ctx context.Context, turn requestctx.TurnMeta) (context.Context, error) {
	if s.evidenceFactory == nil {
		return ctx, nil
	}
	return s.evidenceFactory.Restore(ctx, evidenceTurn(ctx, turn))
}
