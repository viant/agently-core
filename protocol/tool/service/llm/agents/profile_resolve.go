package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	promptdef "github.com/viant/agently-core/protocol/prompt"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	agentsvc "github.com/viant/agently-core/service/agent"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
)

// Ensure the toolexec import is used (SystemDocumentMode/Tag are used in injectProfileMessages).
var _ = toolexec.SystemDocumentMode

// resolveProfile expands RunInput.PromptProfileId into child-turn instructions,
// effective tool bundles, and an effective template id — all applied before the
// child agent's BuildBinding runs.
//
// Preconditions (enforced by the caller):
//   - ri.PromptProfileId is non-empty
//   - qi.MessageID has been pre-assigned so profile messages and the agent turn
//     share the same turn ID
//   - childConvID is the already-created child conversation ID
func (s *Service) resolveProfile(ctx context.Context, ri *RunInput, qi *agentsvc.QueryInput, childConvID string) error {
	if s.promptRepo == nil || strings.TrimSpace(ri.PromptProfileId) == "" {
		return nil
	}
	profile, err := s.promptRepo.Load(ctx, strings.TrimSpace(ri.PromptProfileId))
	if err != nil {
		return fmt.Errorf("promptProfileId %q: %w", ri.PromptProfileId, err)
	}
	if profile == nil {
		return fmt.Errorf("promptProfileId %q: not found", ri.PromptProfileId)
	}

	// 1. Render profile messages (local text/URI or MCP source).
	convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	msgs, err := profile.Render(ctx, s.mcpMgr, &promptdef.RenderOptions{ConversationID: convID})
	if err != nil {
		return fmt.Errorf("render profile %q: %w", ri.PromptProfileId, err)
	}

	// 2. Optionally run expansion sidecar to synthesize task-specific instructions.
	if profile.Expansion != nil && strings.EqualFold(strings.TrimSpace(profile.Expansion.Mode), "llm") {
		msgs = s.expandMessages(ctx, msgs, strings.TrimSpace(ri.Objective), profile.Expansion)
	}

	// 3. Inject rendered (and possibly expanded) messages into the child conversation
	//    so BuildBinding picks them up as system documents / history entries.
	if err := s.injectProfileMessages(ctx, ri, qi, childConvID, msgs); err != nil {
		return err
	}

	// 2. Merge tool bundles: profile floor + RunInput additions.
	//    Profile is the structural floor; RunInput may extend but never removes.
	merged := make([]string, 0, len(profile.ToolBundles)+len(ri.ToolBundles))
	merged = append(merged, profile.ToolBundles...)
	merged = append(merged, ri.ToolBundles...)
	qi.ToolBundles = merged

	// 3. Resolve effective template (RunInput > profile default).
	//    Store on QueryInput.TemplateId so the agent pipeline can inject the
	//    template document when a template repo is wired in.
	if qi.TemplateId == "" {
		if strings.TrimSpace(ri.TemplateId) != "" {
			qi.TemplateId = strings.TrimSpace(ri.TemplateId)
		} else if strings.TrimSpace(profile.Template) != "" {
			qi.TemplateId = strings.TrimSpace(profile.Template)
		}
	}
	return nil
}

// injectProfileMessages writes each profile message into the child conversation
// under the pre-assigned turn ID.  System-role messages are stored as system
// documents (SystemDocumentMode + SystemDocumentTag) so BuildBinding loads them.
// User and assistant messages are stored with their natural roles.
func (s *Service) injectProfileMessages(ctx context.Context, ri *RunInput, qi *agentsvc.QueryInput, childConvID string, msgs []promptdef.Message) error {
	if s.conv == nil || len(msgs) == 0 || strings.TrimSpace(childConvID) == "" || strings.TrimSpace(qi.MessageID) == "" {
		return nil
	}
	turn := runtimerequestctx.TurnMeta{
		ConversationID: strings.TrimSpace(childConvID),
		TurnID:         strings.TrimSpace(qi.MessageID),
	}
	// Scope context to the child conversation so any context-aware helpers
	// resolve the right conversation.
	childCtx := runtimerequestctx.WithConversationID(ctx, strings.TrimSpace(childConvID))
	childCtx = runtimerequestctx.WithTurnMeta(childCtx, turn)
	if err := s.ensureProfileTurn(childCtx, turn); err != nil {
		return err
	}

	for i, msg := range msgs {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		opts := []apiconv.MessageOption{
			apiconv.WithRole(role),
			apiconv.WithType("text"),
			apiconv.WithCreatedByUserID("prompt"),
			apiconv.WithContent(text),
			apiconv.WithContextSummary(fmt.Sprintf("prompt://%s/message/%d", strings.TrimSpace(ri.PromptProfileId), i)),
			apiconv.WithCreatedAt(time.Now()),
		}
		if role == "system" {
			opts = append(opts,
				apiconv.WithMode(toolexec.SystemDocumentMode),
				apiconv.WithTags(toolexec.SystemDocumentTag),
			)
		}
		if _, err := apiconv.AddMessage(childCtx, s.conv, &turn, opts...); err != nil {
			return fmt.Errorf("inject profile message %d (role=%s): %w", i, role, err)
		}
	}
	return nil
}

// ensureProfileTurn seeds the child turn before profile messages are inserted.
// Profile injection runs before agent.Query.startTurn, so we create a quiet
// placeholder turn row here to satisfy message.turn_id foreign keys without
// emitting a premature turn_started event.
func (s *Service) ensureProfileTurn(ctx context.Context, turn runtimerequestctx.TurnMeta) error {
	if s == nil || s.conv == nil || strings.TrimSpace(turn.ConversationID) == "" || strings.TrimSpace(turn.TurnID) == "" {
		return nil
	}
	existing, err := s.conv.GetConversation(ctx, strings.TrimSpace(turn.ConversationID), apiconv.WithIncludeTranscript(true))
	if err != nil {
		return fmt.Errorf("load child conversation %q: %w", strings.TrimSpace(turn.ConversationID), err)
	}
	if existing != nil {
		for _, transcriptTurn := range existing.GetTranscript() {
			if transcriptTurn != nil && strings.TrimSpace(transcriptTurn.Id) == strings.TrimSpace(turn.TurnID) {
				return nil
			}
		}
	}
	upd := apiconv.NewTurn()
	upd.SetId(strings.TrimSpace(turn.TurnID))
	upd.SetConversationID(strings.TrimSpace(turn.ConversationID))
	upd.SetStatus("pending")
	if err := s.conv.PatchTurn(ctx, upd); err != nil {
		return fmt.Errorf("seed child turn %q: %w", strings.TrimSpace(turn.TurnID), err)
	}
	return nil
}
