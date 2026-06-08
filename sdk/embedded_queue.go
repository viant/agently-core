package sdk

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/toolvalidate"
	agconvwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	queueCount "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/count"
	queueOutcome "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/outcome"
	queueRead "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queueWrite "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agturnlist "github.com/viant/agently-core/pkg/agently/turn/queuedList"
	agturnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	turnqueueread "github.com/viant/agently-core/pkg/agently/turnqueue/read"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/tool"
	approvalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	api "github.com/viant/agently-core/sdk/api"
	agentsvc "github.com/viant/agently-core/service/agent"
	toolapproval "github.com/viant/agently-core/service/shared/toolapproval"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/mcp-protocol/schema"
	hstate "github.com/viant/xdatly/handler/state"
	_ "modernc.org/sqlite"
)

func moveQueuedTurn(c *backendClient, ctx context.Context, input *MoveQueuedTurnInput) error {
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
	}, principalDataOpts(ctx)...)
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

func editQueuedTurn(c *backendClient, ctx context.Context, input *EditQueuedTurnInput) error {
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
	}, principalDataOpts(ctx)...)
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

func forceSteerQueuedTurn(c *backendClient, ctx context.Context, conversationID, turnID string) (*SteerTurnOutput, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	conversationID = strings.TrimSpace(conversationID)
	turnID = strings.TrimSpace(turnID)
	turn, err := c.data.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{
		ID:             turnID,
		ConversationID: conversationID,
		Has:            &agturnbyid.TurnLookupInputHas{ID: true, ConversationID: true},
	}, principalDataOpts(ctx)...)
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
	logx.Infof("conversation", "steer.force_accepted convo=%q queued_turn_id=%q active_turn_id=%q starter_message_id=%q", conversationID, turnID, strings.TrimSpace(active.Id), starterID)
	return out, nil
}

func resolveElicitation(c *backendClient, ctx context.Context, input *ResolveElicitationInput) error {
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

func listPendingElicitations(c *backendClient, ctx context.Context, input *ListPendingElicitationsInput) ([]*PendingElicitation, error) {
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

func listPendingToolApprovals(c *backendClient, ctx context.Context, input *ListPendingToolApprovalsInput) (*PendingToolApprovalPage, error) {
	lister, ok := c.conv.(toolApprovalQueueLister)
	if !ok || lister == nil {
		return nil, errors.New("tool approval queue not configured")
	}
	patcher, _ := c.conv.(toolApprovalQueuePatcher)
	in := &queueRead.QueueRowsInput{}
	var selectors []*hstate.NamedQuerySelector
	effectiveUserID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if input != nil {
		if strings.TrimSpace(input.UserID) != "" {
			if effectiveUserID != "" && !strings.EqualFold(strings.TrimSpace(input.UserID), effectiveUserID) {
				return nil, errors.New("permission denied")
			}
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
		if input.Limit > 0 || input.Offset > 0 {
			selector := &hstate.NamedQuerySelector{
				Name: "queue_rows",
				QuerySelector: hstate.QuerySelector{
					Offset: input.Offset,
				},
			}
			if input.Limit > 0 {
				selector.Limit = input.Limit + 1
			}
			selectors = append(selectors, selector)
		}
	}
	if effectiveUserID != "" {
		in.UserId = effectiveUserID
		if in.Has == nil {
			in.Has = &queueRead.QueueRowsInputHas{}
		}
		in.Has.UserId = true
	}
	// outcomeBoundary is the wall-clock instant captured *before* the
	// in-call sweep and historical outcome lookup. Any terminal
	// transition produced by this request (e.g. a timeout sweep) is
	// guaranteed to record updated_at/timed_out_at at or after this
	// boundary because the sweep captures a fresh time.Now() value.
	// The cursor we hand back to the client is outcomeBoundary - 1ns
	// so a follow-up poll that carries that cursor can still observe
	// the just-produced outcome under the strict transitionAt > since
	// rule, even on platforms where two adjacent time.Now() calls can
	// land in the same nanosecond.
	outcomeBoundary := time.Now().UTC()
	rows, err := listToolApprovalRows(ctx, lister, selectors, in)
	if err != nil {
		return nil, err
	}
	outcomes, rows, err := expireTimedOutToolApprovals(ctx, c, patcher, lister, selectors, in, rows)
	if err != nil {
		return nil, err
	}
	if outcomeLister, ok := c.conv.(toolApprovalOutcomeLister); ok {
		historicalOutcomes, err := collectApprovalOutcomesSince(ctx, outcomeLister, &approvalOutcomeQuery{
			UserID:         in.UserId,
			ConversationID: in.ConversationId,
			Since:          strings.TrimSpace(valueOrEmptyOutcomeSince(input)),
			Until:          time.Now().UTC(),
		})
		if err != nil {
			return nil, err
		}
		if len(historicalOutcomes) > 0 {
			outcomes = mergeApprovalOutcomes(outcomes, historicalOutcomes)
		}
	}
	out := make([]*PendingToolApproval, 0, len(rows))
	for _, row := range rows {
		if item := pendingToolApprovalFromRow(row); item != nil {
			out = append(out, item)
		}
	}
	total := len(out)
	limit := total
	offset := 0
	rowsPage := out
	hasMore := false
	if input != nil {
		offset = input.Offset
		if offset < 0 {
			offset = 0
		}
		if input.Limit > 0 {
			limit = input.Limit
			if len(rowsPage) > limit {
				hasMore = true
				rowsPage = rowsPage[:limit]
			}
			if counter, ok := c.conv.(toolApprovalQueueCounter); ok {
				countInput := &queueCount.QueueTotalInput{
					Id:             in.Id,
					UserId:         in.UserId,
					ConversationId: in.ConversationId,
					TurnId:         in.TurnId,
					MessageId:      in.MessageId,
					ToolName:       in.ToolName,
					QueueStatus:    in.QueueStatus,
					Has: &queueCount.QueueTotalInputHas{
						Id:             in.Has != nil && in.Has.Id,
						UserId:         in.Has != nil && in.Has.UserId,
						ConversationId: in.Has != nil && in.Has.ConversationId,
						TurnId:         in.Has != nil && in.Has.TurnId,
						MessageId:      in.Has != nil && in.Has.MessageId,
						ToolName:       in.Has != nil && in.Has.ToolName,
						QueueStatus:    in.Has != nil && in.Has.QueueStatus,
					},
				}
				if exact, err := counter.CountToolApprovalQueues(ctx, countInput); err == nil {
					total = exact
				} else {
					total = offset + len(rowsPage)
					if hasMore {
						total++
					}
				}
			} else {
				total = offset + len(rowsPage)
				if hasMore {
					total++
				}
			}
		}
	}
	return &PendingToolApprovalPage{
		Rows:          rowsPage,
		Total:         total,
		Offset:        offset,
		Limit:         limit,
		HasMore:       hasMore,
		Outcomes:      outcomes,
		OutcomeCursor: outcomeBoundary.Add(-time.Nanosecond).Format(time.RFC3339Nano),
	}, nil
}

func listToolApprovalRows(ctx context.Context, lister toolApprovalQueueLister, selectors []*hstate.NamedQuerySelector, in *queueRead.QueueRowsInput) ([]*queueRead.QueueRowView, error) {
	if selectorLister, ok := lister.(toolApprovalQueueSelectorLister); ok && len(selectors) > 0 {
		return selectorLister.ListToolApprovalQueuesWithSelectors(ctx, in, selectors...)
	}
	return lister.ListToolApprovalQueues(ctx, in)
}

func valueOrEmptyOutcomeSince(input *ListPendingToolApprovalsInput) string {
	if input == nil {
		return ""
	}
	return input.OutcomeSince
}

func pendingToolApprovalFromRow(row *queueRead.QueueRowView) *PendingToolApproval {
	if row == nil {
		return nil
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
	item.ExpiresAt = row.ExpiresAt
	if row.ErrorMessage != nil {
		item.ErrorMessage = *row.ErrorMessage
	}
	if len(row.Arguments) > 0 {
		_ = json.Unmarshal(row.Arguments, &item.Arguments)
	}
	if row.Metadata != nil && len(*row.Metadata) > 0 {
		_ = json.Unmarshal(*row.Metadata, &item.Metadata)
	}
	return item
}

func expireTimedOutToolApprovals(ctx context.Context, c *backendClient, patcher toolApprovalQueuePatcher, lister toolApprovalQueueLister, selectors []*hstate.NamedQuerySelector, in *queueRead.QueueRowsInput, rows []*queueRead.QueueRowView) ([]*api.DecideToolApprovalOutcome, []*queueRead.QueueRowView, error) {
	if patcher == nil || lister == nil || len(rows) == 0 {
		return nil, rows, nil
	}
	now := time.Now().UTC()
	outcomes := make([]*api.DecideToolApprovalOutcome, 0)
	changed := false
	for _, row := range rows {
		if !approvalqueue.IsTimedOut(row, now) {
			continue
		}
		changed = true
		outcome, err := timeoutToolApproval(ctx, c, patcher, lister, row, now)
		if err != nil {
			return nil, nil, err
		}
		if outcome != nil {
			outcomes = append(outcomes, outcome)
		}
	}
	if !changed {
		return nil, rows, nil
	}
	refreshed, err := listToolApprovalRows(ctx, lister, selectors, in)
	if err != nil {
		return nil, nil, err
	}
	return outcomes, refreshed, nil
}

type approvalOutcomeQuery struct {
	UserID         string
	ConversationID string
	Since          string
	Until          time.Time
}

func collectApprovalOutcomesSince(ctx context.Context, lister toolApprovalOutcomeLister, query *approvalOutcomeQuery) ([]*api.DecideToolApprovalOutcome, error) {
	if lister == nil || query == nil || strings.TrimSpace(query.Since) == "" {
		return nil, nil
	}
	since, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(query.Since))
	if err != nil {
		return nil, fmt.Errorf("invalid outcomeSince cursor: %w", err)
	}
	in := &queueOutcome.OutcomeRowsInput{Has: &queueOutcome.OutcomeRowsInputHas{}}
	if strings.TrimSpace(query.UserID) != "" {
		in.UserId = strings.TrimSpace(query.UserID)
		in.Has.UserId = true
	}
	if strings.TrimSpace(query.ConversationID) != "" {
		in.ConversationId = strings.TrimSpace(query.ConversationID)
		in.Has.ConversationId = true
	}
	in.Since = since
	in.Has.Since = true
	in.Until = query.Until
	in.Has.Until = true
	rows, err := lister.ListToolApprovalOutcomes(ctx, in)
	if err != nil {
		return nil, err
	}
	outcomes := make([]*api.DecideToolApprovalOutcome, 0)
	for _, row := range rows {
		outcome := approvalOutcomeFromOutcomeRow(row)
		if outcome == nil {
			continue
		}
		outcomes = append(outcomes, outcome)
	}
	sort.SliceStable(outcomes, func(i, j int) bool {
		left := approvalOutcomeSortKey(outcomes[i])
		right := approvalOutcomeSortKey(outcomes[j])
		if left.Equal(right) {
			return outcomes[i].ApprovalID < outcomes[j].ApprovalID
		}
		return left.Before(right)
	})
	return outcomes, nil
}

func mergeApprovalOutcomes(primary, extra []*api.DecideToolApprovalOutcome) []*api.DecideToolApprovalOutcome {
	if len(extra) == 0 {
		return primary
	}
	seen := make(map[string]struct{}, len(primary)+len(extra))
	out := make([]*api.DecideToolApprovalOutcome, 0, len(primary)+len(extra))
	for _, item := range primary {
		if item == nil {
			continue
		}
		key := strings.TrimSpace(item.ApprovalID)
		if key != "" {
			seen[key] = struct{}{}
		}
		out = append(out, item)
	}
	for _, item := range extra {
		if item == nil {
			continue
		}
		key := strings.TrimSpace(item.ApprovalID)
		if key != "" {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, item)
	}
	return out
}

func isTerminalApprovalStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approved", "rejected", "canceled", "executed", "failed", "timed_out":
		return true
	default:
		return false
	}
}

func approvalOutcomeTransitionTime(row *queueRead.QueueRowView) (time.Time, bool) {
	switch strings.ToLower(strings.TrimSpace(row.Status)) {
	case "timed_out":
		if row.TimedOutAt != nil && !row.TimedOutAt.IsZero() {
			return *row.TimedOutAt, true
		}
	case "executed":
		if row.ExecutedAt != nil && !row.ExecutedAt.IsZero() {
			return *row.ExecutedAt, true
		}
	case "approved", "rejected", "canceled":
		if row.ApprovedAt != nil && !row.ApprovedAt.IsZero() {
			return *row.ApprovedAt, true
		}
	}
	if row.UpdatedAt != nil && !row.UpdatedAt.IsZero() {
		return *row.UpdatedAt, true
	}
	return row.CreatedAt, !row.CreatedAt.IsZero()
}

func approvalOutcomeFromRow(row *queueRead.QueueRowView) *api.DecideToolApprovalOutcome {
	if row == nil {
		return nil
	}
	meta := parseToolApprovalMetadata(row.Metadata)
	if meta.Outcome != nil {
		cp := *meta.Outcome
		return &cp
	}
	outcome := &api.DecideToolApprovalOutcome{
		ApprovalID: row.Id,
		ToolName:   row.ToolName,
	}
	if row.ConversationId != nil {
		outcome.ConversationID = strings.TrimSpace(*row.ConversationId)
	}
	if row.TurnId != nil {
		outcome.TurnID = strings.TrimSpace(*row.TurnId)
	}
	if row.MessageId != nil {
		outcome.MessageID = strings.TrimSpace(*row.MessageId)
	}
	switch strings.ToLower(strings.TrimSpace(row.Status)) {
	case "timed_out":
		outcome.Action = api.ApprovalTimeoutOutcomeAction
		outcome.Status = api.ApprovalTimeoutOutcomeStatus
		outcome.Decision = api.ApprovalTimeoutOutcomeDecision
		outcome.Result = api.ApprovalTimeoutErrorMessage
		outcome.ErrorMessage = api.ApprovalTimeoutErrorMessage
		if row.ExpiresAt != nil {
			t := *row.ExpiresAt
			outcome.ExpiresAt = &t
		}
		if row.TimedOutAt != nil {
			t := *row.TimedOutAt
			outcome.TimedOutAt = &t
		}
	case "rejected":
		outcome.Action = "reject"
		outcome.Status = "rejected"
		outcome.Decision = "reject"
		outcome.Result = "tool execution was not approved by user"
	case "canceled":
		outcome.Action = "cancel"
		outcome.Status = "canceled"
		outcome.Decision = "cancel"
		outcome.Result = "tool execution was not approved by user"
	case "executed":
		outcome.Action = "approve"
		outcome.Status = "executed"
		outcome.Decision = "approve"
	case "failed":
		outcome.Action = "approve"
		outcome.Status = "failed"
		outcome.Decision = "approve"
	default:
		outcome.Action = strings.ToLower(strings.TrimSpace(row.Status))
		outcome.Status = strings.ToLower(strings.TrimSpace(row.Status))
		outcome.Decision = strings.ToLower(strings.TrimSpace(valueOrEmpty(row.Decision)))
	}
	if row.ErrorMessage != nil {
		outcome.ErrorMessage = strings.TrimSpace(*row.ErrorMessage)
	}
	return outcome
}

func approvalOutcomeFromOutcomeRow(row *queueOutcome.OutcomeRowView) *api.DecideToolApprovalOutcome {
	if row == nil {
		return nil
	}
	meta := parseToolApprovalMetadata(row.Metadata)
	if meta.Outcome != nil {
		cp := *meta.Outcome
		return &cp
	}
	outcome := &api.DecideToolApprovalOutcome{
		ApprovalID: row.Id,
		ToolName:   row.ToolName,
	}
	if row.ConversationId != nil {
		outcome.ConversationID = strings.TrimSpace(*row.ConversationId)
	}
	if row.TurnId != nil {
		outcome.TurnID = strings.TrimSpace(*row.TurnId)
	}
	if row.MessageId != nil {
		outcome.MessageID = strings.TrimSpace(*row.MessageId)
	}
	switch strings.ToLower(strings.TrimSpace(row.Status)) {
	case "timed_out":
		outcome.Action = api.ApprovalTimeoutOutcomeAction
		outcome.Status = api.ApprovalTimeoutOutcomeStatus
		outcome.Decision = api.ApprovalTimeoutOutcomeDecision
		outcome.ErrorMessage = api.ApprovalTimeoutErrorMessage
		if row.TimedOutAt != nil {
			outcome.TimedOutAt = row.TimedOutAt
		}
	case "executed":
		outcome.Action = "approve"
		outcome.Status = "executed"
		outcome.Decision = valueOrEmpty(row.Decision)
	case "approved":
		outcome.Action = "approve"
		outcome.Status = "approved"
		outcome.Decision = valueOrEmpty(row.Decision)
	case "rejected":
		outcome.Action = "reject"
		outcome.Status = "rejected"
		outcome.Decision = valueOrEmpty(row.Decision)
	case "canceled":
		outcome.Action = "cancel"
		outcome.Status = "canceled"
		outcome.Decision = valueOrEmpty(row.Decision)
	case "failed":
		outcome.Action = "approve"
		outcome.Status = "failed"
		outcome.Decision = valueOrEmpty(row.Decision)
	default:
		outcome.Action = strings.ToLower(strings.TrimSpace(valueOrEmpty(row.Decision)))
		outcome.Status = strings.TrimSpace(row.Status)
		outcome.Decision = valueOrEmpty(row.Decision)
	}
	if row.ErrorMessage != nil {
		outcome.ErrorMessage = strings.TrimSpace(*row.ErrorMessage)
	}
	if row.ExpiresAt != nil {
		outcome.ExpiresAt = row.ExpiresAt
	}
	if row.TimedOutAt != nil {
		outcome.TimedOutAt = row.TimedOutAt
	}
	return outcome
}

func approvalOutcomeSortKey(outcome *api.DecideToolApprovalOutcome) time.Time {
	if outcome == nil {
		return time.Time{}
	}
	if outcome.TimedOutAt != nil && !outcome.TimedOutAt.IsZero() {
		return *outcome.TimedOutAt
	}
	if outcome.ExpiresAt != nil && !outcome.ExpiresAt.IsZero() {
		return *outcome.ExpiresAt
	}
	return time.Time{}
}

func timeoutToolApproval(ctx context.Context, c *backendClient, patcher toolApprovalQueuePatcher, lister toolApprovalQueueLister, row *queueRead.QueueRowView, now time.Time) (*api.DecideToolApprovalOutcome, error) {
	upd := approvalqueue.NewTimedOutPatch(row, now)
	if err := patcher.PatchToolApprovalQueue(ctx, upd); err != nil && !isToolApprovalQueueDuplicateErr(err) {
		return nil, err
	}
	if err := ensureToolApprovalStatus(ctx, lister, row.Id, api.ApprovalTimeoutOutcomeStatus, func() error {
		return fallbackToolApprovalUpdate(row.Id, map[string]interface{}{
			"status":        api.ApprovalTimeoutOutcomeStatus,
			"decision":      api.ApprovalTimeoutOutcomeDecision,
			"timed_out_at":  now,
			"updated_at":    now,
			"error_message": api.ApprovalTimeoutErrorMessage,
		})
	}); err != nil {
		return nil, err
	}
	_ = synthesizeQueueDecisionResult(ctx, c, row, api.ApprovalTimeoutErrorMessage)
	if isSystemOSEnvTool(row.ToolName) {
		_ = persistSystemOSEnvTimedOutAssistantResult(ctx, c, row)
	} else {
		_ = continueQueueConversation(ctx, c, row, buildQueueTimeoutInstruction(row.ToolName))
	}
	outcome := approvalqueue.NewTimedOutOutcome(row, now)
	if err := persistToolApprovalOutcomeMetadata(ctx, patcher, row, outcome, now); err != nil {
		return nil, err
	}
	return outcome, nil
}

func decideToolApproval(c *backendClient, ctx context.Context, input *DecideToolApprovalInput) (*DecideToolApprovalOutput, error) {
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
	effectiveUserID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if effectiveUserID != "" && strings.TrimSpace(input.UserID) != "" && !strings.EqualFold(strings.TrimSpace(input.UserID), effectiveUserID) {
		return nil, errors.New("permission denied")
	}
	in := &queueRead.QueueRowsInput{Id: strings.TrimSpace(input.ID), Has: &queueRead.QueueRowsInputHas{Id: true}}
	if effectiveUserID != "" {
		in.UserId = effectiveUserID
		in.Has.UserId = true
	} else if strings.TrimSpace(input.UserID) != "" {
		in.UserId = strings.TrimSpace(input.UserID)
		in.Has.UserId = true
	}
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
	// Race-condition guard: if the row is still pending but its deadline
	// has already passed, the canonical timeout producer wins over the
	// late user action. The decide caller learns the truthful outcome
	// instead of overwriting a deadline-driven transition with an
	// approve/reject/cancel.
	if strings.EqualFold(strings.TrimSpace(row.Status), "pending") && row.ExpiresAt != nil && !row.ExpiresAt.IsZero() && !row.ExpiresAt.After(now) {
		timeoutOutcome, terr := timeoutToolApproval(ctx, c, patcher, lister, row, now)
		if terr != nil {
			return nil, terr
		}
		return &DecideToolApprovalOutput{Status: "ok", Outcome: timeoutOutcome}, nil
	}
	upd := &queueWrite.ToolApprovalQueue{Has: &queueWrite.ToolApprovalQueueHas{}}
	upd.SetId(row.Id)
	upd.SetUserId(row.UserId)
	upd.SetToolName(row.ToolName)
	upd.SetArguments(row.Arguments)
	upd.SetUpdatedAt(now)
	outcome := &api.DecideToolApprovalOutcome{
		ApprovalID: row.Id,
		Action:     canonicalDecideAction(action),
		ToolName:   row.ToolName,
	}
	if row.ConversationId != nil {
		outcome.ConversationID = strings.TrimSpace(*row.ConversationId)
	}
	if row.TurnId != nil {
		outcome.TurnID = strings.TrimSpace(*row.TurnId)
	}
	if row.MessageId != nil {
		outcome.MessageID = strings.TrimSpace(*row.MessageId)
	}
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
		if err := ensureToolApprovalStatus(ctx, lister, row.Id, "approved", func() error {
			return fallbackToolApprovalUpdate(row.Id, map[string]interface{}{
				"status":              "approved",
				"decision":            "approve",
				"approved_by_user_id": strings.TrimSpace(input.UserID),
				"approved_at":         now,
				"updated_at":          now,
			})
		}); err != nil {
			return nil, err
		}
		var args map[string]interface{}
		_ = json.Unmarshal(row.Arguments, &args)
		meta := parseToolApprovalMetadata(row.Metadata)
		if err := toolapproval.ApplyEdits(args, approvalEditorsFromMeta(meta), input.EditedFields); err != nil {
			return nil, err
		}
		reviewPayload := input.Payload
		if len(reviewPayload) == 0 && len(input.EditedFields) > 0 {
			reviewPayload = input.EditedFields
		}
		if meta.Review != nil && len(reviewPayload) > 0 {
			if err := toolapproval.ApplyReview(args, meta.Review, reviewPayload); err != nil {
				return nil, err
			}
		}
		if patch := decisionPatchFromMeta(meta, "approve"); len(patch) > 0 {
			if err := toolapproval.ApplyDecisionPatch(args, patch); err != nil {
				return nil, err
			}
		}
		execCtx := ctx
		if strings.TrimSpace(input.UserID) != "" {
			execCtx = authctx.WithUserInfo(execCtx, &authctx.UserInfo{Subject: strings.TrimSpace(input.UserID)})
		}
		turn := runtimerequestctx.TurnMeta{}
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
			execCtx = runtimerequestctx.WithConversationID(execCtx, turn.ConversationID)
		}
		if turn.ConversationID != "" && turn.TurnID != "" {
			execCtx = runtimerequestctx.WithTurnMeta(execCtx, turn)
		}
		toolResult, execErr := c.ExecuteTool(execCtx, row.ToolName, args)
		if turn.ConversationID != "" && turn.TurnID != "" {
			_ = toolexec.SynthesizeToolStep(execCtx, c.conv, toolexec.StepInfo{
				ID:         syntheticToolStepID(meta.OpID),
				Name:       row.ToolName,
				Args:       args,
				ResponseID: meta.ResponseID,
			}, resolvedQueueToolResult(toolResult, execErr))
		}
		done := &queueWrite.ToolApprovalQueue{Has: &queueWrite.ToolApprovalQueueHas{}}
		done.SetId(row.Id)
		done.SetUserId(row.UserId)
		done.SetToolName(row.ToolName)
		done.SetArguments(row.Arguments)
		done.SetUpdatedAt(time.Now().UTC())
		if execErr != nil {
			done.SetStatus("pending")
			done.Decision = nil
			done.Has.Decision = true
			done.ApprovedByUserId = nil
			done.Has.ApprovedByUserId = true
			done.ApprovedAt = nil
			done.Has.ApprovedAt = true
			done.ExecutedAt = nil
			done.Has.ExecutedAt = true
			done.SetErrorMessage(execErr.Error())
		} else {
			done.SetStatus("executed")
			done.SetExecutedAt(time.Now().UTC())
			done.ErrorMessage = nil
			done.Has.ErrorMessage = true
		}
		if err := patcher.PatchToolApprovalQueue(ctx, done); err != nil && !isToolApprovalQueueDuplicateErr(err) {
			return nil, err
		}
		finalStatus := "executed"
		fallbackFields := map[string]interface{}{
			"status":        "executed",
			"executed_at":   time.Now().UTC(),
			"error_message": nil,
			"updated_at":    time.Now().UTC(),
		}
		if execErr != nil {
			finalStatus = "pending"
			fallbackFields["status"] = "pending"
			fallbackFields["decision"] = nil
			fallbackFields["approved_by_user_id"] = nil
			fallbackFields["approved_at"] = nil
			fallbackFields["error_message"] = execErr.Error()
			delete(fallbackFields, "executed_at")
		}
		if err := ensureToolApprovalStatus(ctx, lister, row.Id, finalStatus, func() error {
			return fallbackToolApprovalUpdate(row.Id, fallbackFields)
		}); err != nil {
			return nil, err
		}
		outcome.Decision = "approve"
		if execErr != nil {
			outcome.Status = "failed"
			outcome.ErrorMessage = execErr.Error()
		} else {
			outcome.Status = "executed"
			outcome.Result = toolResult
		}
		if err := persistToolApprovalOutcomeMetadata(ctx, patcher, row, outcome, now); err != nil {
			return nil, err
		}
		if execErr == nil {
			if isSystemOSEnvTool(row.ToolName) {
				_ = persistSystemOSEnvAssistantResult(execCtx, c, row, toolResult)
			} else {
				_ = continueQueueConversation(execCtx, c, row, buildQueueContinuationInstruction(row.ToolName, toolResult, true))
			}
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
		if err := ensureToolApprovalStatus(ctx, lister, row.Id, "rejected", func() error {
			return fallbackToolApprovalUpdate(row.Id, map[string]interface{}{
				"status":              "rejected",
				"decision":            "reject",
				"approved_by_user_id": strings.TrimSpace(input.UserID),
				"approved_at":         now,
				"updated_at":          now,
				"error_message":       strings.TrimSpace(input.Reason),
			})
		}); err != nil {
			return nil, err
		}
		outcome.Status = "rejected"
		outcome.Decision = "reject"
		outcome.Result = "tool execution was not approved by user"
		if strings.TrimSpace(input.Reason) != "" {
			outcome.ErrorMessage = strings.TrimSpace(input.Reason)
		}
		if err := persistToolApprovalOutcomeMetadata(ctx, patcher, row, outcome, now); err != nil {
			return nil, err
		}
		_ = synthesizeQueueDecisionResult(ctx, c, row, "tool execution was not approved by user")
		if isSystemOSEnvTool(row.ToolName) {
			_ = persistSystemOSEnvDeniedAssistantResult(ctx, c, row)
		} else {
			_ = continueQueueConversation(ctx, c, row, buildQueueContinuationInstruction(row.ToolName, "tool execution was not approved by user", false))
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
		if err := ensureToolApprovalStatus(ctx, lister, row.Id, "canceled", func() error {
			return fallbackToolApprovalUpdate(row.Id, map[string]interface{}{
				"status":              "canceled",
				"decision":            "cancel",
				"approved_by_user_id": strings.TrimSpace(input.UserID),
				"approved_at":         now,
				"updated_at":          now,
				"error_message":       strings.TrimSpace(input.Reason),
			})
		}); err != nil {
			return nil, err
		}
		outcome.Status = "canceled"
		outcome.Decision = "cancel"
		outcome.Result = "tool execution was not approved by user"
		if strings.TrimSpace(input.Reason) != "" {
			outcome.ErrorMessage = strings.TrimSpace(input.Reason)
		}
		if err := persistToolApprovalOutcomeMetadata(ctx, patcher, row, outcome, now); err != nil {
			return nil, err
		}
		_ = synthesizeQueueDecisionResult(ctx, c, row, "tool execution was not approved by user")
		if isSystemOSEnvTool(row.ToolName) {
			_ = persistSystemOSEnvDeniedAssistantResult(ctx, c, row)
		} else {
			_ = continueQueueConversation(ctx, c, row, buildQueueContinuationInstruction(row.ToolName, "tool execution was not approved by user", false))
		}
	default:
		return nil, errors.New("action must be approve, reject, or cancel")
	}
	return &DecideToolApprovalOutput{Status: "ok", Outcome: outcome}, nil
}

func canonicalDecideAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "approve", "accepted":
		return "approve"
	case "reject", "rejected":
		return "reject"
	case "cancel", "canceled", "cancelled":
		return "cancel"
	default:
		return strings.ToLower(strings.TrimSpace(action))
	}
}

type toolApprovalMetadata struct {
	OpID       string                         `json:"opId"`
	ResponseID string                         `json:"responseId"`
	TurnID     string                         `json:"turnId"`
	Approval   *toolapproval.View             `json:"approval,omitempty"`
	Review     *llm.ApprovalReviewConfig      `json:"review,omitempty"`
	Outcome    *api.DecideToolApprovalOutcome `json:"outcome,omitempty"`
}

func parseToolApprovalMetadata(raw *[]byte) toolApprovalMetadata {
	if raw == nil || len(*raw) == 0 {
		return toolApprovalMetadata{}
	}
	result := toolApprovalMetadata{}
	_ = json.Unmarshal(*raw, &result)
	return result
}

func persistToolApprovalOutcomeMetadata(ctx context.Context, patcher toolApprovalQueuePatcher, row *queueRead.QueueRowView, outcome *api.DecideToolApprovalOutcome, now time.Time) error {
	if patcher == nil || row == nil || outcome == nil {
		return nil
	}
	meta := parseToolApprovalMetadata(row.Metadata)
	meta.Outcome = outcome
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	upd := &queueWrite.ToolApprovalQueue{Has: &queueWrite.ToolApprovalQueueHas{}}
	upd.SetId(row.Id)
	upd.SetUserId(row.UserId)
	upd.SetToolName(row.ToolName)
	upd.SetArguments(row.Arguments)
	upd.SetMetadata(encoded)
	upd.SetUpdatedAt(now)
	if err := patcher.PatchToolApprovalQueue(ctx, upd); err != nil && !isToolApprovalQueueDuplicateErr(err) {
		return err
	}
	return nil
}

func approvalEditorsFromMeta(meta toolApprovalMetadata) []*toolapproval.EditorView {
	if meta.Approval == nil {
		return nil
	}
	return meta.Approval.Editors
}

func decisionPatchFromMeta(meta toolApprovalMetadata, action string) map[string]interface{} {
	if meta.Approval == nil || len(meta.Approval.Decisions) == 0 {
		return nil
	}
	decision := meta.Approval.Decisions[canonicalDecideAction(action)]
	if decision == nil || len(decision.Patch) == 0 {
		return nil
	}
	return decision.Patch
}

func syntheticToolStepID(opID string) string {
	opID = strings.TrimSpace(opID)
	if opID == "" {
		return "tool-approval-" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "-")
	}
	return opID + ":approved"
}

func resolvedQueueToolResult(result string, err error) string {
	if strings.TrimSpace(result) != "" {
		return result
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func synthesizeQueueDecisionResult(ctx context.Context, c *backendClient, row *queueRead.QueueRowView, result string) error {
	if c == nil || c.conv == nil || row == nil || row.ConversationId == nil || row.TurnId == nil {
		return nil
	}
	var args map[string]interface{}
	_ = json.Unmarshal(row.Arguments, &args)
	meta := parseToolApprovalMetadata(row.Metadata)
	turnCtx := runtimerequestctx.WithConversationID(ctx, strings.TrimSpace(*row.ConversationId))
	turnCtx = runtimerequestctx.WithTurnMeta(turnCtx, runtimerequestctx.TurnMeta{
		ConversationID:  strings.TrimSpace(*row.ConversationId),
		TurnID:          strings.TrimSpace(*row.TurnId),
		ParentMessageID: valueOrEmpty(row.MessageId),
	})
	return toolexec.SynthesizeToolStep(turnCtx, c.conv, toolexec.StepInfo{
		ID:         syntheticToolStepID(meta.OpID),
		Name:       row.ToolName,
		Args:       args,
		ResponseID: meta.ResponseID,
	}, result)
}

func continueQueueConversation(ctx context.Context, c *backendClient, row *queueRead.QueueRowView, instruction string) error {
	if c == nil || c.agent == nil || row == nil || row.ConversationId == nil || row.TurnId == nil {
		return nil
	}
	turnID := strings.TrimSpace(*row.TurnId)
	conversationID := strings.TrimSpace(*row.ConversationId)
	if turnID == "" || conversationID == "" {
		return nil
	}
	agentID, err := lookupQueueTurnAgentID(turnID)
	if err != nil {
		return err
	}
	if agentID == "" {
		return nil
	}
	userID := strings.TrimSpace(row.UserId)
	if strings.TrimSpace(userID) == "" {
		userID = strings.TrimSpace(authctx.EffectiveUserID(ctx))
	}
	followCtx := context.Background()
	if userID != "" {
		followCtx = authctx.WithUserInfo(followCtx, &authctx.UserInfo{Subject: userID})
	}
	followUp := &agentsvc.QueryInput{
		ConversationID:         conversationID,
		UserId:                 userID,
		Query:                  strings.TrimSpace(instruction),
		DisplayQuery:           strings.TrimSpace(instruction),
		MessageID:              turnID,
		SkipInitialUserMessage: true,
		DisableChains:          true,
		ToolsAllowed:           []string{"__queue_continuation_no_tools__"},
	}
	autoSelectTools := false
	followUp.AutoSelectTools = &autoSelectTools
	if finder := c.agent.Finder(); finder != nil {
		if ag, err := finder.Find(followCtx, agentID); err == nil && ag != nil {
			cloned := *ag
			cloned.Tool = agentmdl.Tool{}
			cloned.ToolCallExposure = ""
			followUp.Agent = &cloned
		} else {
			followUp.AgentID = agentID
		}
	} else {
		followUp.AgentID = agentID
	}
	var output agentsvc.QueryOutput
	return c.agent.Query(followCtx, followUp, &output)
}

func buildQueueContinuationInstruction(toolName, result string, approved bool) string {
	toolName = strings.TrimSpace(toolName)
	result = strings.TrimSpace(result)
	if approved {
		return fmt.Sprintf("Continue the previous request using this approved %s result. Do not call any tools. Return the answer directly from this result:\n\n%s", toolName, result)
	}
	return fmt.Sprintf("Continue the previous request. The %s execution was not approved by the user. Do not ask for approval again in this turn and do not call any tools. Explain briefly that the value could not be retrieved because approval was not granted. Latest tool result:\n\n%s", toolName, result)
}

func buildQueueTimeoutInstruction(toolName string) string {
	toolName = strings.TrimSpace(toolName)
	return fmt.Sprintf("Continue the previous request. The %s execution could not proceed because user approval timed out. Do not ask for approval again in this turn and do not call any tools. Explain briefly that the value could not be retrieved because approval timed out.", toolName)
}

func isSystemOSEnvTool(name string) bool {
	return mcpname.Canonical(strings.TrimSpace(name)) == mcpname.Canonical("system/os/getEnv")
}

func persistSystemOSEnvAssistantResult(ctx context.Context, c *backendClient, row *queueRead.QueueRowView, toolResult string) error {
	if c == nil || c.conv == nil || row == nil || row.ConversationId == nil || row.TurnId == nil {
		return nil
	}
	content := formatSystemOSEnvResult(toolResult)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return persistQueueAssistantResult(ctx, c, row, content)
}

func persistSystemOSEnvDeniedAssistantResult(ctx context.Context, c *backendClient, row *queueRead.QueueRowView) error {
	return persistQueueAssistantResult(ctx, c, row, formatSystemOSEnvDeniedResult(row))
}

func persistSystemOSEnvTimedOutAssistantResult(ctx context.Context, c *backendClient, row *queueRead.QueueRowView) error {
	return persistQueueAssistantResult(ctx, c, row, formatSystemOSEnvTimedOutResult(row))
}

func persistQueueAssistantResult(ctx context.Context, c *backendClient, row *queueRead.QueueRowView, content string) error {
	if c == nil || c.conv == nil || row == nil || row.ConversationId == nil || row.TurnId == nil {
		return nil
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	conversationID := strings.TrimSpace(*row.ConversationId)
	turnID := strings.TrimSpace(*row.TurnId)
	if convView, err := c.conv.GetConversation(ctx, conversationID, conversation.WithIncludeTranscript(true)); err == nil && convView != nil {
		for _, tr := range convView.Transcript {
			if tr == nil || strings.TrimSpace(tr.Id) != turnID {
				continue
			}
			maxIteration := 0
			for _, existing := range tr.Message {
				if existing == nil || existing.Iteration == nil {
					continue
				}
				if *existing.Iteration > maxIteration {
					maxIteration = *existing.Iteration
				}
			}
			for i := len(tr.Message) - 1; i >= 0; i-- {
				msg := tr.Message[i]
				if msg == nil || !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
					continue
				}
				if msg.Interim == 1 {
					continue
				}
				upd := conversation.NewMessage()
				upd.SetId(msg.Id)
				upd.SetConversationID(conversationID)
				upd.SetTurnID(turnID)
				upd.SetRole("assistant")
				upd.SetType("text")
				upd.SetContent(content)
				upd.SetInterim(0)
				if err := c.conv.PatchMessage(ctx, upd); err != nil {
					return err
				}
				return completeResolvedQueueTurn(ctx, c, conversationID, turnID)
			}
			msg := conversation.NewMessage()
			msg.SetId(uuid.NewString())
			msg.SetConversationID(conversationID)
			msg.SetTurnID(turnID)
			msg.SetRole("assistant")
			msg.SetType("text")
			msg.SetContent(content)
			msg.SetCreatedAt(time.Now())
			msg.SetInterim(0)
			msg.SetParentMessageID(valueOrEmpty(row.MessageId))
			if maxIteration > 0 {
				msg.SetIteration(maxIteration + 1)
			}
			if err := c.conv.PatchMessage(ctx, msg); err != nil {
				return err
			}
			return completeResolvedQueueTurn(ctx, c, conversationID, turnID)
		}
	}
	msg := conversation.NewMessage()
	msg.SetId(uuid.NewString())
	msg.SetConversationID(conversationID)
	msg.SetTurnID(turnID)
	msg.SetRole("assistant")
	msg.SetType("text")
	msg.SetContent(content)
	msg.SetCreatedAt(time.Now())
	msg.SetInterim(0)
	msg.SetParentMessageID(valueOrEmpty(row.MessageId))
	if err := c.conv.PatchMessage(ctx, msg); err != nil {
		return err
	}
	return completeResolvedQueueTurn(ctx, c, conversationID, turnID)
}

func formatSystemOSEnvResult(result string) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	var payload struct {
		Values map[string]string `json:"values"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return result
	}
	if len(payload.Values) == 0 {
		return "The requested environment variable is not set."
	}
	return "```json\n" + result + "\n```"
}

func formatSystemOSEnvDeniedResult(row *queueRead.QueueRowView) string {
	if row == nil {
		return "I couldn't retrieve the requested environment variable because approval was not granted."
	}
	var args struct {
		Names []string `json:"names"`
	}
	if len(row.Arguments) > 0 {
		_ = json.Unmarshal(row.Arguments, &args)
	}
	if len(args.Names) == 1 && strings.TrimSpace(args.Names[0]) != "" {
		return fmt.Sprintf("I couldn't retrieve your %s environment variable because approval was not granted.", strings.TrimSpace(args.Names[0]))
	}
	if len(args.Names) > 1 {
		return "I couldn't retrieve the requested environment variables because approval was not granted."
	}
	return "I couldn't retrieve the requested environment variable because approval was not granted."
}

func formatSystemOSEnvTimedOutResult(row *queueRead.QueueRowView) string {
	if row == nil {
		return "I couldn't retrieve the requested environment variable because approval timed out."
	}
	var args struct {
		Names []string `json:"names"`
	}
	if len(row.Arguments) > 0 {
		_ = json.Unmarshal(row.Arguments, &args)
	}
	if len(args.Names) == 1 && strings.TrimSpace(args.Names[0]) != "" {
		return fmt.Sprintf("I couldn't retrieve your %s environment variable because approval timed out.", strings.TrimSpace(args.Names[0]))
	}
	if len(args.Names) > 1 {
		return "I couldn't retrieve the requested environment variables because approval timed out."
	}
	return "I couldn't retrieve the requested environment variable because approval timed out."
}

func completeResolvedQueueTurn(ctx context.Context, c *backendClient, conversationID, turnID string) error {
	conversationID = strings.TrimSpace(conversationID)
	turnID = strings.TrimSpace(turnID)
	if c == nil || c.conv == nil || conversationID == "" || turnID == "" {
		return nil
	}
	now := time.Now()
	if c.data != nil {
		run := agrunwrite.NewMutableRunView(agrunwrite.WithRunID(turnID))
		run.SetStatus("succeeded")
		run.SetCompletedAt(now)
		if _, err := c.data.PatchRuns(ctx, []*agrunwrite.MutableRunView{run}); err != nil {
			return err
		}
	}
	if err := c.conv.PatchConversations(ctx, agconvwrite.NewConversationStatus(conversationID, "succeeded")); err != nil {
		return err
	}
	upd := conversation.NewTurn()
	upd.SetId(turnID)
	upd.SetConversationID(conversationID)
	upd.SetStatus("succeeded")
	return c.conv.PatchTurn(ctx, upd)
}

func lookupQueueTurnAgentID(turnID string) (string, error) {
	dsn := strings.TrimSpace(os.Getenv("AGENTLY_DB_DSN"))
	if dsn == "" {
		dsn = filepath.Join(workspace.RuntimeRoot(), "db", "agently-core.db")
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var agentID sql.NullString
	if err := db.QueryRow("SELECT agent_id_used FROM turn WHERE id = ?", strings.TrimSpace(turnID)).Scan(&agentID); err != nil {
		return "", err
	}
	return strings.TrimSpace(agentID.String), nil
}

func ensureToolApprovalStatus(ctx context.Context, lister toolApprovalQueueLister, id, want string, fallback func() error) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(want) == "" || lister == nil {
		return nil
	}
	if toolApprovalHasStatus(ctx, lister, id, want) {
		return nil
	}
	if fallback == nil {
		return nil
	}
	if err := fallback(); err != nil {
		return err
	}
	if !toolApprovalHasStatus(ctx, lister, id, want) {
		return fmt.Errorf("tool approval %s did not transition to %s", id, want)
	}
	return nil
}

func toolApprovalHasStatus(ctx context.Context, lister toolApprovalQueueLister, id, want string) bool {
	rows, err := lister.ListToolApprovalQueues(ctx, &queueRead.QueueRowsInput{
		Id:  strings.TrimSpace(id),
		Has: &queueRead.QueueRowsInputHas{Id: true},
	})
	if err != nil || len(rows) == 0 || rows[0] == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(rows[0].Status), strings.TrimSpace(want))
}

func fallbackToolApprovalUpdate(id string, fields map[string]interface{}) error {
	if strings.TrimSpace(id) == "" || len(fields) == 0 {
		return nil
	}
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("AGENTLY_DB_DRIVER")))
	if driver != "" && driver != "sqlite" {
		return nil
	}
	dsn := strings.TrimSpace(os.Getenv("AGENTLY_DB_DSN"))
	if dsn == "" {
		dsn = filepath.Join(workspace.RuntimeRoot(), "db", "agently-core.db")
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	sets := make([]string, 0, len(fields))
	args := make([]interface{}, 0, len(fields)+1)
	add := func(name string, value interface{}) {
		sets = append(sets, name+" = ?")
		args = append(args, value)
	}
	if value, ok := fields["status"]; ok {
		add("status", value)
	}
	if value, ok := fields["decision"]; ok {
		add("decision", nullableString(value))
	}
	if value, ok := fields["approved_by_user_id"]; ok {
		add("approved_by_user_id", nullableString(value))
	}
	if value, ok := fields["approved_at"]; ok {
		add("approved_at", value)
	}
	if value, ok := fields["executed_at"]; ok {
		add("executed_at", value)
	}
	if value, ok := fields["expires_at"]; ok {
		add("expires_at", value)
	}
	if value, ok := fields["timed_out_at"]; ok {
		add("timed_out_at", value)
	}
	if value, ok := fields["error_message"]; ok {
		add("error_message", nullableString(value))
	}
	if value, ok := fields["updated_at"]; ok {
		add("updated_at", value)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, strings.TrimSpace(id))
	_, err = db.Exec("UPDATE tool_approval_queue SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	return err
}

func nullableString(value interface{}) interface{} {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return nil
	}
	return text
}

func listToolDefinitions(c *backendClient) ([]ToolDefinitionInfo, error) {
	if c.registry == nil {
		return nil, nil
	}
	defs := c.registry.Definitions()
	out := make([]ToolDefinitionInfo, len(defs))
	for i, d := range defs {
		out[i] = ToolDefinitionInfo{Name: d.Name, Description: d.Description, Parameters: d.Parameters, Required: d.Required, OutputSchema: d.OutputSchema, Cacheable: d.Cacheable}
	}
	return out, nil
}

func executeTool(c *backendClient, ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if c.registry == nil {
		return "", errors.New("tool registry not configured")
	}
	if c.toolPolicy != nil && tool.FromContext(ctx) == nil {
		ctx = tool.WithPolicy(ctx, c.toolPolicy)
	}
	if err := toolvalidate.ValidateExecution(ctx, tool.FromContext(ctx), name, args); err != nil {
		return "", err
	}
	return c.registry.Execute(ctx, name, args)
}
