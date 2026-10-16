package agent

import (
	"context"
	"encoding/json"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/toolselection"
)

type toolSelectionHistoryKey struct{}
type completedToolSelection struct {
	operation, tool string
	listedNames     []string
}
type toolSelectionHistory struct {
	history   *binding.History
	completed map[string]completedToolSelection // durable message ID -> exact successful operation
}

// The prompt history alone lacks completion status. Retain provenance from the
// same transcript already loaded by BuildBinding; never infer success from args.
func withToolSelectionHistory(ctx context.Context, history *binding.History, transcript apiconv.Transcript) context.Context {
	turn, ok := requestctx.TurnMetaFromContext(ctx)
	if !ok || turn.TurnID == "" || history == nil {
		return ctx
	}
	snapshot := &toolSelectionHistory{history: history, completed: map[string]completedToolSelection{}}
	add := func(messageID string, call *apiconv.ToolCallView, content string) {
		if messageID == "" || call == nil || call.MessageId != messageID || call.OpId == "" || call.Status != "completed" {
			return
		}
		tool := mcpname.Canonical(call.ToolName)
		if tool != mcpname.Canonical("llm/tools:activate") && tool != mcpname.Canonical("llm/tools:list") {
			return
		}
		if call.ErrorMessage != nil && strings.TrimSpace(*call.ErrorMessage) != "" {
			return
		}
		if call.ErrorCode != nil && strings.TrimSpace(*call.ErrorCode) != "" {
			return
		}
		completed := completedToolSelection{operation: call.OpId, tool: tool}
		if tool == mcpname.Canonical("llm/tools:list") {
			completed.listedNames = toolSelectionResultNames(tool, content)
		}
		snapshot.completed[messageID] = completed
	}
	for _, entry := range transcript {
		if entry == nil || entry.Id != turn.TurnID {
			continue
		}
		for _, raw := range entry.Message {
			msg := (*apiconv.Message)(raw)
			if msg == nil || msg.Id == "" {
				continue
			}
			if msg.ConversationId != turn.ConversationID || msg.TurnId == nil || *msg.TurnId != turn.TurnID {
				continue
			}
			if isConcreteToolResultMessage(msg) {
				add(msg.Id, messageToolCall(msg), msg.GetContentPreferContent())
			}
			for _, child := range msg.ToolMessage {
				if child != nil {
					body := (&apiconv.Message{Content: child.Content}).GetContentPreferContent()
					add(child.Id, child.ToolCall, body)
				}
			}
		}
	}
	return context.WithValue(ctx, toolSelectionHistoryKey{}, snapshot)
}

// restoreToolSelection runs after the normal model-visible candidates are
// seeded. Only successful same-turn durable outputs can restore exposure, and
// Activate rechecks each exact name against the current candidate set.
func restoreToolSelection(ctx context.Context, input *QueryInput, state *toolselection.State) {
	if state == nil || input == nil {
		return
	}
	turn, ok := requestctx.TurnMetaFromContext(ctx)
	if !ok || turn.TurnID == "" {
		return
	}
	snapshot, _ := ctx.Value(toolSelectionHistoryKey{}).(*toolSelectionHistory)
	if snapshot == nil || snapshot.history == nil {
		return
	}
	history := snapshot.history
	if history.CurrentTurnID != turn.TurnID || history.Current == nil || history.Current.ID != turn.TurnID {
		return
	}
	for _, msg := range history.Current.Messages {
		if msg == nil || msg.ToolError != "" {
			continue
		}
		completed, found := snapshot.completed[msg.ID]
		if !found {
			continue
		}
		// Some transcript adapters join call metadata only under the parent
		// assistant. The exact durable message identity above remains required
		// even when the history's flat message omits optional tool metadata.
		if msg.ToolOpID != "" && completed.operation != msg.ToolOpID {
			continue
		}
		if msg.ToolName != "" && mcpname.Canonical(msg.ToolName) != completed.tool {
			continue
		}
		// A list's metadata page can be preview-truncated in prompt history.
		// Preserve only its returned names from the same loaded durable message;
		// the current visible history ID and exact successful op are still required.
		names := completed.listedNames
		if completed.tool != mcpname.Canonical("llm/tools:list") {
			names = toolSelectionResultNames(completed.tool, msg.Content)
		}
		for _, name := range names {
			_ = state.Activate([]string{name})
		}
	}
}

func toolSelectionResultNames(tool, content string) []string {
	maxBytes := 65536
	if tool == mcpname.Canonical("llm/tools:list") {
		maxBytes = 256 << 10
	} // bounded page includes descriptions
	if len(content) > maxBytes {
		return nil
	}
	var result struct {
		Names []string `json:"names"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if json.Unmarshal([]byte(content), &result) != nil {
		return nil
	}
	names := result.Names
	if tool == mcpname.Canonical("llm/tools:list") {
		names = nil
		for _, item := range result.Tools {
			names = append(names, item.Name)
		}
	}
	if len(names) == 0 || len(names) > 50 {
		return nil
	}
	return names
}
