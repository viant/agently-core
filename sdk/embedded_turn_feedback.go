package sdk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	authctx "github.com/viant/agently-core/internal/auth"
)

const MaxTurnFeedbackCommentLength = 4000

var (
	errInvalidTurnFeedback         = errors.New("invalid turn feedback")
	errTurnFeedbackNotFound        = errors.New("turn not found")
	errTurnFeedbackUnavailable     = errors.New("turn has no completed final answer")
	errTurnFeedbackUnauthenticated = errors.New("authentication required")
)

func turnFeedbackEligible(turn *TurnState) bool {
	return turn != nil && turn.Status == TurnStatusCompleted && turn.Origin != "host_request" &&
		turn.Assistant != nil && turn.Assistant.Final != nil &&
		(strings.TrimSpace(turn.Assistant.Final.Content) != "" || turn.Assistant.Final.RenderedContent != nil)
}

func validateTurnFeedback(input *SetTurnFeedbackInput) error {
	if input == nil || (input.Rating != -1 && input.Rating != 1) {
		return fmt.Errorf("%w: rating must be -1 or 1", errInvalidTurnFeedback)
	}
	if input.Comment != nil && (!utf8.ValidString(*input.Comment) || utf8.RuneCountInString(*input.Comment) > MaxTurnFeedbackCommentLength) {
		return fmt.Errorf("%w: comment must contain at most %d characters", errInvalidTurnFeedback, MaxTurnFeedbackCommentLength)
	}
	return nil
}

func (c *backendClient) SetTurnFeedback(ctx context.Context, input *SetTurnFeedbackInput) (*TurnFeedback, error) {
	if err := validateTurnFeedback(input); err != nil {
		return nil, err
	}
	feedback := &TurnFeedback{Rating: input.Rating, Comment: input.Comment}
	if feedback.Comment != nil && strings.TrimSpace(*feedback.Comment) == "" {
		feedback.Comment = nil
	}
	now := time.Now().UTC().Truncate(time.Second)
	feedback.UpdatedAt = &now
	return c.writeTurnFeedback(ctx, input.ConversationID, input.TurnID, feedback)
}

func (c *backendClient) ClearTurnFeedback(ctx context.Context, conversationID, turnID string) (*TurnFeedback, error) {
	return c.writeTurnFeedback(ctx, conversationID, turnID, &TurnFeedback{})
}

func (c *backendClient) writeTurnFeedback(ctx context.Context, conversationID, turnID string, feedback *TurnFeedback) (*TurnFeedback, error) {
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if userID == "" {
		return nil, errTurnFeedbackUnauthenticated
	}
	conversationID, turnID = strings.TrimSpace(conversationID), strings.TrimSpace(turnID)
	if conversationID == "" || turnID == "" {
		return nil, fmt.Errorf("%w: conversation ID and turn ID are required", errInvalidTurnFeedback)
	}
	if c.conv == nil {
		return nil, errors.New("conversation service not configured")
	}
	conv, err := c.conv.GetConversation(ctx, conversationID, conversation.WithIncludeTranscript(true), conversation.WithIncludeModelCall(false), conversation.WithIncludeToolCall(false))
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, errTurnFeedbackNotFound
	}
	// Read access to shared/public conversations does not grant feedback writes.
	if conv.CreatedByUserId == nil || strings.TrimSpace(*conv.CreatedByUserId) != userID {
		return nil, data.ErrPermissionDenied
	}
	var selected *conversation.Turn
	for _, turn := range conv.GetTranscript() {
		if turn != nil && turn.Id == turnID {
			selected = turn
			break
		}
	}
	if selected == nil {
		return nil, errTurnFeedbackNotFound
	}
	if !turnFeedbackEligible(buildTurnState(selected, false)) {
		return nil, errTurnFeedbackUnavailable
	}
	patch := conversation.NewTurn()
	patch.SetId(turnID)
	patch.SetFeedbackRating(feedback.Rating)
	patch.SetFeedbackComment(feedback.Comment)
	patch.SetFeedbackUpdatedAt(feedback.UpdatedAt)
	if err := c.conv.PatchTurn(ctx, patch); err != nil {
		return nil, err
	}
	return feedback, nil
}
