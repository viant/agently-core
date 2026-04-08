package message

import (
	"context"
	"fmt"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	runtimeprojection "github.com/viant/agently-core/runtime/projection"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type ProjectInput struct {
	TurnIDs    []string `json:"turnIds,omitempty" description:"turn IDs to hide from active prompt history"`
	MessageIDs []string `json:"messageIds,omitempty" description:"message IDs to hide from active prompt history"`
	Reason     string   `json:"reason,omitempty" description:"short human-readable reason for the projection update"`
}

type ProjectOutput struct {
	HiddenTurnIDs    []string `json:"hiddenTurnIds,omitempty"`
	HiddenMessageIDs []string `json:"hiddenMessageIds,omitempty"`
	Reason           string   `json:"reason,omitempty"`
}

// project mutates the request-scoped projection state only. It does not modify
// transcript truth. The updated hidden turn/message IDs take effect on the next
// prompt-history build within the same request.
func (s *Service) project(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ProjectInput)
	if !ok {
		return fmt.Errorf("invalid input")
	}
	output, ok := out.(*ProjectOutput)
	if !ok {
		return fmt.Errorf("invalid output")
	}
	state, ok := runtimeprojection.StateFromContext(ctx)
	if !ok {
		return fmt.Errorf("projection state not initialized")
	}
	if len(input.TurnIDs) > 0 {
		state.HideTurns(input.TurnIDs...)
		if s != nil && s.conv != nil {
			if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok && strings.TrimSpace(turn.ConversationID) != "" {
				if expanded := s.expandTurnMessageIDs(ctx, strings.TrimSpace(turn.ConversationID), input.TurnIDs); len(expanded) > 0 {
					state.HideMessages(expanded...)
				}
			}
		}
	}
	if len(input.MessageIDs) > 0 {
		state.HideMessages(input.MessageIDs...)
	}
	if reason := strings.TrimSpace(input.Reason); reason != "" {
		state.AddReason(reason)
	}
	snapshot := state.Snapshot()
	output.HiddenTurnIDs = append([]string(nil), snapshot.HiddenTurnIDs...)
	output.HiddenMessageIDs = append([]string(nil), snapshot.HiddenMessageIDs...)
	output.Reason = snapshot.Reason
	return nil
}

func (s *Service) expandTurnMessageIDs(ctx context.Context, conversationID string, turnIDs []string) []string {
	if s == nil || s.conv == nil || strings.TrimSpace(conversationID) == "" || len(turnIDs) == 0 {
		return nil
	}
	conv, err := s.conv.GetConversation(ctx, strings.TrimSpace(conversationID), apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	if err != nil || conv == nil {
		return nil
	}
	hiddenTurns := map[string]struct{}{}
	for _, turnID := range turnIDs {
		turnID = strings.TrimSpace(turnID)
		if turnID == "" {
			continue
		}
		hiddenTurns[turnID] = struct{}{}
	}
	if len(hiddenTurns) == 0 {
		return nil
	}
	var result []string
	seen := map[string]struct{}{}
	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		if _, ok := hiddenTurns[strings.TrimSpace(turn.Id)]; !ok {
			continue
		}
		for _, msg := range turn.GetMessages() {
			if msg == nil {
				continue
			}
			if id := strings.TrimSpace(msg.Id); id != "" {
				if _, ok := seen[id]; !ok {
					seen[id] = struct{}{}
					result = append(result, id)
				}
			}
			for _, tm := range msg.ToolMessage {
				if tm == nil {
					continue
				}
				if id := strings.TrimSpace(tm.Id); id != "" {
					if _, ok := seen[id]; !ok {
						seen[id] = struct{}{}
						result = append(result, id)
					}
				}
			}
		}
	}
	return result
}
