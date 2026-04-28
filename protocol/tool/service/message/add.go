package message

import (
	"context"
	"fmt"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type AddInput struct {
	Role    string `json:"role" description:"Message role. Currently only assistant is supported."`
	Content string `json:"content" description:"Message content to persist in the current turn."`
	Interim *bool  `json:"interim,omitempty" description:"Whether the message is interim. Defaults to false."`
	Mode    string `json:"mode,omitempty" description:"Optional message mode, e.g. task or exec."`
	Status  string `json:"status,omitempty" description:"Optional message status."`
}

type AddOutput struct {
	MessageID       string `json:"messageId,omitempty"`
	ConversationID  string `json:"conversationId,omitempty"`
	TurnID          string `json:"turnId,omitempty"`
	ParentMessageID string `json:"parentMessageId,omitempty"`
	Sequence        int    `json:"sequence,omitempty"`
}

func (s *Service) add(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*AddInput)
	if !ok {
		return fmt.Errorf("invalid input")
	}
	output, ok := out.(*AddOutput)
	if !ok {
		return fmt.Errorf("invalid output")
	}
	if s == nil || s.conv == nil {
		return fmt.Errorf("conversation client not initialised")
	}
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok || strings.TrimSpace(turn.ConversationID) == "" || strings.TrimSpace(turn.TurnID) == "" {
		return fmt.Errorf("missing turn context")
	}

	role := strings.ToLower(strings.TrimSpace(input.Role))
	if role == "" {
		role = "assistant"
	}
	if role != "assistant" {
		return fmt.Errorf("unsupported role %q", role)
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return fmt.Errorf("content is required")
	}

	interim := 0
	if input.Interim != nil && *input.Interim {
		interim = 1
	}
	parentMessageID := strings.TrimSpace(turn.ParentMessageID)
	if interim != 0 && parentMessageID == "" {
		parentMessageID = strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	}
	addCtx := ctx
	if interim == 0 {
		addCtx = runtimerequestctx.WithMessageAddEvent(ctx)
	}

	opts := []apiconv.MessageOption{
		apiconv.WithRole(role),
		apiconv.WithContent(content),
		apiconv.WithInterim(interim),
		apiconv.WithCreatedByUserID("assistant"),
	}
	if parentMessageID != "" {
		opts = append(opts, apiconv.WithParentMessageID(parentMessageID))
	}
	if mode := strings.TrimSpace(input.Mode); mode != "" {
		opts = append(opts, apiconv.WithMode(mode))
	}
	if status := strings.TrimSpace(input.Status); status != "" {
		opts = append(opts, apiconv.WithStatus(status))
	}

	msg, err := apiconv.AddMessage(addCtx, s.conv, &turn, opts...)
	if err != nil {
		return err
	}

	output.MessageID = strings.TrimSpace(msg.Id)
	output.ConversationID = strings.TrimSpace(turn.ConversationID)
	output.TurnID = strings.TrimSpace(turn.TurnID)
	output.ParentMessageID = parentMessageID
	output.Sequence = valueOrZero(msg.Sequence)
	return nil
}

func valueOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
