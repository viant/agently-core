package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
)

const (
	GuestToolStatusOK     = "completed"
	GuestToolStatusQueued = "queued"
	GuestToolStatusFailed = "failed"
	GuestToolSourceUI     = "guest_ui"
)

// GuestToolCallInput carries a host-mediated MCP UI tool call request.
// ToolBundles is the only channel through which bundle-derived approval
// metadata reaches this path — host callers never invent their own
// approval state.
type GuestToolCallInput struct {
	ConversationID string
	ToolName       string
	Arguments      map[string]interface{}
	ToolBundles    []string
	AssistantText  string
}

type GuestToolCallOutput struct {
	ConversationID string
	TurnID         string
	ToolName       string
	Status         string
	Result         string
	Source         string
}

// RunGuestToolCall executes an MCP UI host-mediated tool call against the
// active conversation through the canonical toolexec.ExecuteToolStep path.
// Bundle-derived approval configuration is applied via toolapprovalqueue
// before execution, so a tool configured for queue approval is routed to
// the existing approval queue and the returned Status is GuestToolStatusQueued.
func (s *Service) RunGuestToolCall(ctx context.Context, input *GuestToolCallInput) (*GuestToolCallOutput, error) {
	if input == nil {
		return nil, errors.New("input is required")
	}
	name := strings.TrimSpace(input.ToolName)
	if name == "" {
		return nil, errors.New("tool name is required")
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	if conversationID == "" {
		return nil, errors.New("conversation id is required")
	}
	if s == nil || s.registry == nil || s.conversation == nil {
		return nil, errors.New("guest tool call requires registry and conversation client")
	}

	canonical := mcpname.Canonical(name)
	ctx = toolapprovalqueue.WithState(ctx)
	toolapprovalqueue.MarkSource(ctx, GuestToolSourceUI)
	bundles := normalizeBundleIDs(input.ToolBundles)
	if len(bundles) > 0 {
		entry, err := s.resolveBundleResult(ctx, bundles)
		if err != nil {
			return nil, fmt.Errorf("resolve tool bundles: %w", err)
		}
		s.applyResolvedToolSurfaceMetadata(ctx, entry)
		if err := assertBundleAllowsTool(entry, canonical); err != nil {
			return nil, err
		}
	}

	turnID, err := s.ensureGuestTurn(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	meta := runtimerequestctx.TurnMeta{
		ConversationID:  conversationID,
		TurnID:          turnID,
		ParentMessageID: turnID,
	}
	ctx = runtimerequestctx.WithConversationID(ctx, conversationID)
	ctx = runtimerequestctx.WithTurnMeta(ctx, meta)

	out := &GuestToolCallOutput{
		ConversationID: conversationID,
		TurnID:         turnID,
		ToolName:       canonical,
		Source:         GuestToolSourceUI,
	}

	queueGated := toolapprovalqueue.RequiresQueue(ctx, canonical)
	call, _, execErr := toolexec.ExecuteToolStep(ctx, s.registry, toolexec.StepInfo{
		Name:       canonical,
		Args:       guestToolArgs(input.Arguments),
		ResponseID: "mcp_ui_guest_call",
	}, s.conversation)

	switch {
	case toolexec.IsQueued(execErr) || (queueGated && execErr == nil):
		out.Status = GuestToolStatusQueued
		out.Result = strings.TrimSpace(call.Result)
		if out.Result == "" {
			out.Result = "queued for user approval"
		}
		return out, nil
	case execErr != nil:
		out.Status = GuestToolStatusFailed
		out.Result = strings.TrimSpace(call.Result)
		return out, execErr
	default:
		out.Status = GuestToolStatusOK
		out.Result = call.Result
		return out, nil
	}
}

func (s *Service) ensureGuestTurn(ctx context.Context, conversationID string) (string, error) {
	turnID := "guest-" + uuid.NewString()
	turn := apiconv.NewTurn()
	turn.SetId(turnID)
	turn.SetConversationID(conversationID)
	turn.SetStatus("running")
	turn.SetStartedByMessageID(turnID)
	if err := s.conversation.PatchTurn(ctx, turn); err != nil {
		return "", fmt.Errorf("persist guest turn: %w", err)
	}
	return turnID, nil
}

func guestToolArgs(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func normalizeBundleIDs(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, id)
	}
	return out
}

func assertBundleAllowsTool(entry *resolvedToolSurface, canonicalName string) error {
	if entry == nil || canonicalName == "" {
		return nil
	}
	if len(entry.Definitions) == 0 {
		return fmt.Errorf("guest tool %q not exposed by bundle selection", canonicalName)
	}
	wanted := strings.ToLower(canonicalName)
	for _, def := range entry.Definitions {
		if strings.ToLower(mcpname.Canonical(def.Name)) == wanted {
			return nil
		}
	}
	return fmt.Errorf("guest tool %q not exposed by bundle selection", canonicalName)
}
