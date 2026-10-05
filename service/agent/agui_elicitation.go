package agent

import (
	"context"
	"fmt"

	receipt "github.com/viant/agently-core/app/store/elicitationreceipt"
	"github.com/viant/agently-core/service/elicitation"
)

func (s *Service) ResolveElicitationChecked(ctx context.Context, conversationID, id, action string, payload map[string]interface{}) (*elicitation.CheckedResolution, error) {
	if s == nil || s.elicitation == nil {
		return nil, fmt.Errorf("elicitation service not configured")
	}
	return s.elicitation.ResolveChecked(ctx, conversationID, id, action, payload, "")
}
func (s *Service) InspectElicitationResolution(ctx context.Context, conversationID, id, action string, payload map[string]interface{}) (*receipt.Receipt, error) {
	if s == nil || s.elicitation == nil {
		return nil, fmt.Errorf("elicitation service not configured")
	}
	return s.elicitation.InspectResolution(ctx, conversationID, id, action, payload, "")
}
