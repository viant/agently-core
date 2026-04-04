package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
	queueRead "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queueWrite "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agturnlist "github.com/viant/agently-core/pkg/agently/turn/queuedList"
	agturnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	turnqueueread "github.com/viant/agently-core/pkg/agently/turnqueue/read"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/mcp-protocol/schema"
)

func moveQueuedTurn(c *EmbeddedClient, ctx context.Context, input *MoveQueuedTurnInput) error {
	if input == nil {
		return errors.New("input is required")
	}
	if c.data == nil {
		return errors.New("data service not configured")
	}
	turn, err := c.data.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{
		ID:             strings.TrimSpace(input.TurnID),
		ConversationID: strings.TrimSpace(input.ConversationID),
		Has:            &agturnbyid.TurnLookupInputHas{ID: true, ConversationID: true},
	})
	if err != nil {
		if isTurnLookupUnavailable(err) {
			return newConflictError("queued turn not found")
		}
		return err
	}
	if turn == nil {
		return newConflictError("queued turn not found")
	}
	if !strings.EqualFold(strings.TrimSpace(turn.Status), "queued") {
		return newConflictError(fmt.Sprintf("turn is not queued: %s", turn.Status))
	}
	type queueRow struct {
		ID       string
		QueueSeq int64
	}
	rows := make([]queueRow, 0)
	if lister, ok := c.data.(turnQueueLister); ok {
		qRows, err := lister.ListTurnQueueRows(ctx, &turnqueueread.QueueRowsInput{
			ConversationId: strings.TrimSpace(input.ConversationID),
			QueueStatus:    "queued",
			Has:            &turnqueueread.QueueRowsInputHas{ConversationId: true, QueueStatus: true},
		})
		if err != nil {
			return err
		}
		for _, row := range qRows {
			if row != nil {
				rows = append(rows, queueRow{ID: strings.TrimSpace(row.Id), QueueSeq: row.QueueSeq})
			}
		}
	} else {
		fallbackRows, err := c.data.ListQueuedTurns(ctx, &agturnlist.QueuedTurnsInput{
			ConversationID: strings.TrimSpace(input.ConversationID),
			Has:            &agturnlist.QueuedTurnsInputHas{ConversationID: true},
		})
		if err != nil {
			return err
		}
		for i, row := range fallbackRows {
			if row == nil {
				continue
			}
			// Use the list position as a stable fallback so that rows without a
			// QueueSeq always have unique, ordered sequence numbers. Using
			// time.Now().UnixNano() inside the loop risks identical values when
			// two turns were queued within the same nanosecond, which would make
			// a move operation swap identical seq values and produce no change.
			seq := int64(i)
			if row.QueueSeq != nil {
				seq = int64(*row.QueueSeq)
			}
			rows = append(rows, queueRow{ID: strings.TrimSpace(row.Id), QueueSeq: seq})
		}
	}
	if len(rows) < 2 {
		return nil
	}
	idx := -1
	for i, row := range rows {
		if strings.TrimSpace(row.ID) == strings.TrimSpace(input.TurnID) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return newConflictError("queued turn not found in queue list")
	}
	target := idx
	switch strings.ToLower(strings.TrimSpace(input.Direction)) {
	case "up":
		target = idx - 1
	case "down":
		target = idx + 1
	default:
		return errors.New("direction must be up or down")
	}
	if target < 0 || target >= len(rows) {
		return newConflictError("turn cannot be moved in requested direction")
	}
	a := rows[idx]
	b := rows[target]
	updA := &agturnwrite.MutableTurnView{Has: &agturnwrite.TurnHas{}}
	updA.SetId(strings.TrimSpace(a.ID))
	updA.SetQueueSeq(b.QueueSeq)
	updB := &agturnwrite.MutableTurnView{Has: &agturnwrite.TurnHas{}}
	updB.SetId(strings.TrimSpace(b.ID))
	updB.SetQueueSeq(a.QueueSeq)
	if _, err := c.data.PatchTurns(ctx, []*agturnwrite.MutableTurnView{updA, updB}); err != nil {
		return err
	}
	if patcher, ok := c.data.(turnQueuePatcher); ok {
		qa := &turnqueuewrite.TurnQueue{Has: &turnqueuewrite.TurnQueueHas{}}
		qa.SetId(strings.TrimSpace(a.ID))
		qa.SetQueueSeq(b.QueueSeq)
		qa.SetUpdatedAt(time.Now())
		if err := patcher.PatchTurnQueue(ctx, qa); err != nil {
			return err
		}
		qb := &turnqueuewrite.TurnQueue{Has: &turnqueuewrite.TurnQueueHas{}}
		qb.SetId(strings.TrimSpace(b.ID))
		qb.SetQueueSeq(a.QueueSeq)
		qb.SetUpdatedAt(time.Now())
		return patcher.PatchTurnQueue(ctx, qb)
	}
	return nil
}

func editQueuedTurn(c *EmbeddedClient, ctx context.Context, input *EditQueuedTurnInput) error {
	if input == nil {
		return errors.New("input is required")
	}
	if c.data == nil || c.conv == nil {
		return errors.New("data service not configured")
	}
	turn, err := c.data.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{
		ID:             strings.TrimSpace(input.TurnID),
		ConversationID: strings.TrimSpace(input.ConversationID),
		Has:            &agturnbyid.TurnLookupInputHas{ID: true, ConversationID: true},
	})
	if err != nil {
		if isTurnLookupUnavailable(err) {
			return newConflictError("queued turn not found")
		}
		return err
	}
	if turn == nil {
		return newConflictError("queued turn not found")
	}
	if !strings.EqualFold(strings.TrimSpace(turn.Status), "queued") {
		return newConflictError(fmt.Sprintf("turn is not queued: %s", turn.Status))
	}
	starterID := strings.TrimSpace(valueOrEmpty(turn.StartedByMessageId))
	if starterID == "" {
		starterID = strings.TrimSpace(input.TurnID)
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return errors.New("content is required")
	}
	msg := conversation.NewMessage()
	msg.SetId(starterID)
	msg.SetContent(content)
	msg.SetRawContent(content)
	msg.SetUpdatedAt(time.Now())
	return c.conv.PatchMessage(ctx, msg)
}

func forceSteerQueuedTurn(c *EmbeddedClient, ctx context.Context, conversationID, turnID string) (*SteerTurnOutput, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	conversationID = strings.TrimSpace(conversationID)
	turnID = strings.TrimSpace(turnID)
	turn, err := c.data.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{
		ID:             turnID,
		ConversationID: conversationID,
		Has:            &agturnbyid.TurnLookupInputHas{ID: true, ConversationID: true},
	})
	if err != nil {
		if isTurnLookupUnavailable(err) {
			return nil, newConflictError("queued turn not found")
		}
		return nil, err
	}
	if turn == nil {
		return nil, newConflictError("queued turn not found")
	}
	if !strings.EqualFold(strings.TrimSpace(turn.Status), "queued") {
		return nil, newConflictError(fmt.Sprintf("turn is not queued: %s", turn.Status))
	}
	active, err := c.data.GetActiveTurn(ctx, &agturnactive.ActiveTurnsInput{
		ConversationID: conversationID,
		Has:            &agturnactive.ActiveTurnsInputHas{ConversationID: true},
	})
	if err != nil {
		return nil, err
	}
	if active == nil || strings.TrimSpace(active.Id) == "" {
		return nil, newConflictError("no running turn to steer into")
	}
	starterID := strings.TrimSpace(valueOrEmpty(turn.StartedByMessageId))
	if starterID == "" {
		starterID = turnID
	}
	msg, err := c.conv.GetMessage(ctx, starterID)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(valueOrEmpty(msg.Content))
	if content == "" {
		return nil, newConflictError("queued turn starter message is empty")
	}
	if err := c.CancelQueuedTurn(ctx, conversationID, turnID); err != nil {
		return nil, err
	}
	out, err := c.SteerTurn(ctx, &SteerTurnInput{
		ConversationID: conversationID,
		TurnID:         strings.TrimSpace(active.Id),
		Content:        content,
		Role:           "user",
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &SteerTurnOutput{}
	}
	out.CanceledTurnID = turnID
	out.Status = "accepted"
	return out, nil
}

func resolveElicitation(c *EmbeddedClient, ctx context.Context, input *ResolveElicitationInput) error {
	if input == nil {
		return errors.New("input is required")
	}
	if c.agent != nil {
		return c.agent.ResolveElicitation(ctx, input.ConversationID, input.ElicitationID, input.Action, input.Payload)
	}
	res := &schema.ElicitResult{Action: schema.ElicitResultAction(input.Action), Content: input.Payload}
	if c.elicRouter != nil && c.elicRouter.AcceptByElicitation(input.ConversationID, input.ElicitationID, res) {
		return nil
	}
	return errors.New("elicitation resolver not configured")
}

func listPendingElicitations(c *EmbeddedClient, ctx context.Context, input *ListPendingElicitationsInput) ([]*PendingElicitation, error) {
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	byElicitation := map[string]*PendingElicitation{}
	if c.data != nil {
		in := &agmessagelist.MessageRowsInput{
			ConversationId: input.ConversationID,
			Roles:          []string{"assistant", "tool"},
			Has: &agmessagelist.MessageRowsInputHas{
				ConversationId: true,
				Roles:          true,
			},
		}
		page, err := c.data.GetMessagesPage(ctx, in, nil)
		if err == nil {
			for _, row := range page.Rows {
				if row == nil || row.ElicitationId == nil {
					continue
				}
				elicID := strings.TrimSpace(*row.ElicitationId)
				if elicID == "" {
					continue
				}
				status := strings.TrimSpace(valueOrEmpty(row.Status))
				if !strings.EqualFold(status, "pending") {
					continue
				}
				item := &PendingElicitation{
					ConversationID: row.ConversationId,
					ElicitationID:  elicID,
					MessageID:      row.Id,
					Status:         status,
					Role:           strings.TrimSpace(row.Role),
					Type:           strings.TrimSpace(row.Type),
					CreatedAt:      row.CreatedAt,
					Content:        strings.TrimSpace(valueOrEmpty(row.Content)),
				}
				item.Elicitation = c.resolveElicitationPayload(ctx, elicID, valueOrEmpty(row.ElicitationPayloadId), valueOrEmpty(row.Content))
				if prev, ok := byElicitation[elicID]; ok {
					if prev.CreatedAt.After(item.CreatedAt) || (prev.CreatedAt.Equal(item.CreatedAt) && strings.Compare(prev.MessageID, item.MessageID) >= 0) {
						continue
					}
				}
				byElicitation[elicID] = item
			}
		}
	}
	if c.conv != nil {
		conv, err := c.conv.GetConversation(ctx, input.ConversationID)
		if err != nil {
			return nil, err
		}
		if conv != nil {
			for _, turn := range conv.Transcript {
				if turn == nil {
					continue
				}
				for _, msg := range turn.Message {
					if msg == nil || msg.ElicitationId == nil {
						continue
					}
					role := strings.TrimSpace(msg.Role)
					if !strings.EqualFold(role, "assistant") && !strings.EqualFold(role, "tool") {
						continue
					}
					status := strings.TrimSpace(valueOrEmpty(msg.Status))
					if !strings.EqualFold(status, "pending") {
						continue
					}
					elicID := strings.TrimSpace(*msg.ElicitationId)
					if elicID == "" {
						continue
					}
					item := &PendingElicitation{
						ConversationID: msg.ConversationId,
						ElicitationID:  elicID,
						MessageID:      msg.Id,
						Status:         status,
						Role:           role,
						Type:           strings.TrimSpace(msg.Type),
						CreatedAt:      msg.CreatedAt,
						Content:        strings.TrimSpace(valueOrEmpty(msg.Content)),
					}
					item.Elicitation = c.resolveMessageElicitation(ctx, msg)
					if prev, ok := byElicitation[elicID]; ok {
						if prev.CreatedAt.After(item.CreatedAt) || (prev.CreatedAt.Equal(item.CreatedAt) && strings.Compare(prev.MessageID, item.MessageID) >= 0) {
							continue
						}
					}
					byElicitation[elicID] = item
				}
			}
		}
	}
	out := make([]*PendingElicitation, 0, len(byElicitation))
	for _, item := range byElicitation {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return lessPendingElicitation(out[i], out[j])
	})
	return out, nil
}

func listPendingToolApprovals(c *EmbeddedClient, ctx context.Context, input *ListPendingToolApprovalsInput) ([]*PendingToolApproval, error) {
	lister, ok := c.conv.(toolApprovalQueueLister)
	if !ok || lister == nil {
		return nil, errors.New("tool approval queue not configured")
	}
	in := &queueRead.QueueRowsInput{}
	if input != nil {
		if strings.TrimSpace(input.UserID) != "" {
			in.UserId = strings.TrimSpace(input.UserID)
			in.Has = &queueRead.QueueRowsInputHas{UserId: true}
		}
		if strings.TrimSpace(input.ConversationID) != "" {
			if in.Has == nil {
				in.Has = &queueRead.QueueRowsInputHas{}
			}
			in.ConversationId = strings.TrimSpace(input.ConversationID)
			in.Has.ConversationId = true
		}
		if strings.TrimSpace(input.Status) != "" {
			if in.Has == nil {
				in.Has = &queueRead.QueueRowsInputHas{}
			}
			in.QueueStatus = strings.TrimSpace(input.Status)
			in.Has.QueueStatus = true
		}
	}
	rows, err := lister.ListToolApprovalQueues(ctx, in)
	if err != nil {
		return nil, err
	}
	out := make([]*PendingToolApproval, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		item := &PendingToolApproval{ID: row.Id, UserID: row.UserId, ToolName: row.ToolName, Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		if row.Title != nil {
			item.Title = *row.Title
		}
		if row.ConversationId != nil {
			item.ConversationID = *row.ConversationId
		}
		if row.TurnId != nil {
			item.TurnID = *row.TurnId
		}
		if row.MessageId != nil {
			item.MessageID = *row.MessageId
		}
		if row.Decision != nil {
			item.Decision = *row.Decision
		}
		if row.ErrorMessage != nil {
			item.ErrorMessage = *row.ErrorMessage
		}
		if len(row.Arguments) > 0 {
			_ = json.Unmarshal(row.Arguments, &item.Arguments)
		}
		if row.Metadata != nil && len(*row.Metadata) > 0 {
			_ = json.Unmarshal(*row.Metadata, &item.Metadata)
		}
		out = append(out, item)
	}
	return out, nil
}

func decideToolApproval(c *EmbeddedClient, ctx context.Context, input *DecideToolApprovalInput) (*DecideToolApprovalOutput, error) {
	if input == nil || strings.TrimSpace(input.ID) == "" {
		return nil, errors.New("approval id is required")
	}
	lister, ok := c.conv.(toolApprovalQueueLister)
	if !ok || lister == nil {
		return nil, errors.New("tool approval queue not configured")
	}
	patcher, ok := c.conv.(toolApprovalQueuePatcher)
	if !ok || patcher == nil {
		return nil, errors.New("tool approval queue not configured")
	}
	in := &queueRead.QueueRowsInput{Id: strings.TrimSpace(input.ID), Has: &queueRead.QueueRowsInputHas{Id: true}}
	rows, err := lister.ListToolApprovalQueues(ctx, in)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || rows[0] == nil {
		return nil, errors.New("approval request not found")
	}
	row := rows[0]
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action == "" {
		return nil, errors.New("action is required")
	}
	now := time.Now().UTC()
	upd := &queueWrite.ToolApprovalQueue{Has: &queueWrite.ToolApprovalQueueHas{}}
	upd.SetId(row.Id)
	upd.SetUserId(row.UserId)
	upd.SetToolName(row.ToolName)
	upd.SetArguments(row.Arguments)
	upd.SetUpdatedAt(now)
	switch action {
	case "approve", "accepted":
		upd.SetStatus("approved")
		upd.SetDecision("approve")
		if strings.TrimSpace(input.UserID) != "" {
			upd.SetApprovedByUserId(strings.TrimSpace(input.UserID))
		}
		upd.SetApprovedAt(now)
		if err := patcher.PatchToolApprovalQueue(ctx, upd); err != nil && !isToolApprovalQueueDuplicateErr(err) {
			return nil, err
		}
		var args map[string]interface{}
		_ = json.Unmarshal(row.Arguments, &args)
		execCtx := ctx
		if strings.TrimSpace(input.UserID) != "" {
			execCtx = authctx.WithUserInfo(execCtx, &authctx.UserInfo{Subject: strings.TrimSpace(input.UserID)})
		}
		turn := memory.TurnMeta{}
		if row.ConversationId != nil {
			turn.ConversationID = strings.TrimSpace(*row.ConversationId)
		}
		if row.TurnId != nil {
			turn.TurnID = strings.TrimSpace(*row.TurnId)
		}
		if row.MessageId != nil {
			turn.ParentMessageID = strings.TrimSpace(*row.MessageId)
		}
		if turn.ConversationID != "" {
			execCtx = memory.WithConversationID(execCtx, turn.ConversationID)
		}
		if turn.ConversationID != "" && turn.TurnID != "" {
			execCtx = memory.WithTurnMeta(execCtx, turn)
		}
		_, execErr := c.ExecuteTool(execCtx, row.ToolName, args)
		done := &queueWrite.ToolApprovalQueue{Has: &queueWrite.ToolApprovalQueueHas{}}
		done.SetId(row.Id)
		done.SetUserId(row.UserId)
		done.SetToolName(row.ToolName)
		done.SetArguments(row.Arguments)
		done.SetUpdatedAt(time.Now().UTC())
		if execErr != nil {
			done.SetStatus("failed")
			done.SetErrorMessage(execErr.Error())
		} else {
			done.SetStatus("executed")
			done.SetExecutedAt(time.Now().UTC())
		}
		if err := patcher.PatchToolApprovalQueue(ctx, done); err != nil && !isToolApprovalQueueDuplicateErr(err) {
			return nil, err
		}
	case "reject", "rejected":
		upd.SetStatus("rejected")
		upd.SetDecision("reject")
		if strings.TrimSpace(input.Reason) != "" {
			upd.SetErrorMessage(strings.TrimSpace(input.Reason))
		}
		if strings.TrimSpace(input.UserID) != "" {
			upd.SetApprovedByUserId(strings.TrimSpace(input.UserID))
		}
		upd.SetApprovedAt(now)
		if err := patcher.PatchToolApprovalQueue(ctx, upd); err != nil && !isToolApprovalQueueDuplicateErr(err) {
			return nil, err
		}
	case "cancel", "canceled", "cancelled":
		upd.SetStatus("canceled")
		upd.SetDecision("cancel")
		if strings.TrimSpace(input.Reason) != "" {
			upd.SetErrorMessage(strings.TrimSpace(input.Reason))
		}
		if strings.TrimSpace(input.UserID) != "" {
			upd.SetApprovedByUserId(strings.TrimSpace(input.UserID))
		}
		upd.SetApprovedAt(now)
		if err := patcher.PatchToolApprovalQueue(ctx, upd); err != nil && !isToolApprovalQueueDuplicateErr(err) {
			return nil, err
		}
	default:
		return nil, errors.New("action must be approve, reject, or cancel")
	}
	return &DecideToolApprovalOutput{Status: "ok"}, nil
}

func listToolDefinitions(c *EmbeddedClient) ([]ToolDefinitionInfo, error) {
	if c.registry == nil {
		return nil, nil
	}
	defs := c.registry.Definitions()
	out := make([]ToolDefinitionInfo, len(defs))
	for i, d := range defs {
		out[i] = ToolDefinitionInfo{Name: d.Name, Description: d.Description, Parameters: d.Parameters, Required: d.Required, OutputSchema: d.OutputSchema}
	}
	return out, nil
}

func executeTool(c *EmbeddedClient, ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if c.registry == nil {
		return "", errors.New("tool registry not configured")
	}
	if c.toolPolicy != nil && tool.FromContext(ctx) == nil {
		ctx = tool.WithPolicy(ctx, c.toolPolicy)
	}
	if err := tool.ValidateExecution(ctx, tool.FromContext(ctx), name, args); err != nil {
		return "", err
	}
	return c.registry.Execute(ctx, name, args)
}
