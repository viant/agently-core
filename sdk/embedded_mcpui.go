package sdk

import (
	"context"
	"errors"
	"strings"

	queueRead "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	"github.com/viant/agently-core/pkg/mcpname"
	agentsvc "github.com/viant/agently-core/service/agent"
)

// ExecuteMCPUIToolCall runs an MCP UI host-mediated tool call through the
// agent service's canonical guest-tool path. The browser host hits this
// via POST /v1/api/mcp-ui/tools/call; it never goes through
// /v1/tools/execute because that path bypasses approval-aware execution.
func (c *backendClient) ExecuteMCPUIToolCall(ctx context.Context, input *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) {
	if c == nil || c.agent == nil || c.conv == nil {
		return nil, errors.New("mcp ui tool caller not configured")
	}
	if input == nil {
		return nil, errors.New("input is required")
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	if conversationID == "" {
		created, err := c.CreateConversation(ctx, &CreateConversationInput{Title: "MCP UI Session"})
		if err != nil {
			return nil, err
		}
		if created == nil || strings.TrimSpace(created.Id) == "" {
			return nil, errors.New("failed to create conversation for mcp ui tool call")
		}
		conversationID = strings.TrimSpace(created.Id)
	}
	guestOut, err := c.agent.RunGuestToolCall(ctx, &agentsvc.GuestToolCallInput{
		ConversationID: conversationID,
		ToolName:       input.ToolName,
		Arguments:      input.Arguments,
		ToolBundles:    input.ToolBundles,
		AssistantText:  input.AssistantText,
	})
	if err != nil {
		return nil, err
	}
	out := &MCPUIToolCallOutput{
		ConversationID: guestOut.ConversationID,
		TurnID:         guestOut.TurnID,
		Status:         guestOut.Status,
		Result:         guestOut.Result,
		Source:         guestOut.Source,
	}
	if guestOut.Status == agentsvc.GuestToolStatusQueued {
		out.Approval = c.pendingApprovalForGuestToolCall(ctx, guestOut.ConversationID, guestOut.TurnID, guestOut.ToolName)
	}
	return out, nil
}

func (c *backendClient) pendingApprovalForGuestToolCall(ctx context.Context, conversationID, turnID, toolName string) *PendingToolApproval {
	lister, ok := c.conv.(toolApprovalQueueLister)
	if !ok || lister == nil {
		return nil
	}
	rows, err := lister.ListToolApprovalQueues(ctx, &queueRead.QueueRowsInput{
		ConversationId: strings.TrimSpace(conversationID),
		TurnId:         strings.TrimSpace(turnID),
		QueueStatus:    "pending",
		Has: &queueRead.QueueRowsInputHas{
			ConversationId: true,
			TurnId:         true,
			QueueStatus:    true,
		},
	})
	if err != nil {
		return nil
	}
	wanted := strings.ToLower(strings.TrimSpace(mcpname.Canonical(toolName)))
	for _, row := range rows {
		if row == nil {
			continue
		}
		if wanted != "" && strings.ToLower(strings.TrimSpace(mcpname.Canonical(row.ToolName))) != wanted {
			continue
		}
		return pendingToolApprovalFromRow(row)
	}
	return nil
}
