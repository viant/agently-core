package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/store/conversation"
	cancels "github.com/viant/agently-core/app/store/conversation/cancel"
	"github.com/viant/agently-core/app/store/data"
	authctx "github.com/viant/agently-core/internal/auth"
	agconvlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	agconvwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
	agrun "github.com/viant/agently-core/pkg/agently/run"
	queueRead "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queueWrite "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/a2a"
	agentsvc "github.com/viant/agently-core/service/agent"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/mcp-protocol/schema"
)

type toolApprovalQueueLister interface {
	ListToolApprovalQueues(ctx context.Context, in *queueRead.QueueRowsInput) ([]*queueRead.QueueRowView, error)
}

type toolApprovalQueuePatcher interface {
	PatchToolApprovalQueue(ctx context.Context, queue *queueWrite.ToolApprovalQueue) error
}

type EmbeddedClient struct {
	agent          *agentsvc.Service
	conv           conversation.Client
	data           data.Service
	registry       tool.Registry
	toolPolicy     *tool.Policy
	cancelRegistry cancels.Registry
	elicRouter     elicrouter.ElicitationRouter
	streaming      streaming.Bus
	store          workspace.Store
	a2aSvc         *a2a.Service
}

func NewEmbedded(agent *agentsvc.Service, conv conversation.Client) (*EmbeddedClient, error) {
	if agent == nil {
		return nil, errors.New("embedded sdk requires non-nil agent service")
	}
	if conv == nil {
		return nil, errors.New("embedded sdk requires non-nil conversation client")
	}
	return &EmbeddedClient{agent: agent, conv: conv}, nil
}

func NewEmbeddedFromRuntime(rt *executor.Runtime) (*EmbeddedClient, error) {
	if rt == nil {
		return nil, errors.New("runtime was nil")
	}
	c, err := NewEmbedded(rt.Agent, rt.Conversation)
	if err != nil {
		return nil, err
	}
	c.data = rt.Data
	c.registry = rt.Registry
	c.cancelRegistry = rt.CancelRegistry
	c.elicRouter = rt.ElicitationRouter
	c.streaming = rt.Streaming
	c.store = rt.Store
	if rt.Defaults != nil {
		mode := tool.NormalizeMode(rt.Defaults.ToolApproval.Mode)
		if mode != "" && (mode != tool.ModeAuto || len(rt.Defaults.ToolApproval.AllowList) > 0 || len(rt.Defaults.ToolApproval.BlockList) > 0) {
			c.toolPolicy = &tool.Policy{
				Mode:      mode,
				AllowList: append([]string(nil), rt.Defaults.ToolApproval.AllowList...),
				BlockList: append([]string(nil), rt.Defaults.ToolApproval.BlockList...),
			}
		}
	}
	return c, nil
}

func (c *EmbeddedClient) Mode() Mode { return ModeEmbedded }

func (c *EmbeddedClient) Query(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
	out := &agentsvc.QueryOutput{}
	if err := c.agent.Query(ctx, input, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *EmbeddedClient) GetConversation(ctx context.Context, id string) (*conversation.Conversation, error) {
	return c.conv.GetConversation(ctx, id)
}

func (c *EmbeddedClient) UpdateConversationVisibility(ctx context.Context, input *UpdateConversationVisibilityInput) (*conversation.Conversation, error) {
	if c.conv == nil {
		return nil, errors.New("conversation client not configured")
	}
	if input == nil {
		return nil, errors.New("input is required")
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	if conversationID == "" {
		return nil, errors.New("conversation ID is required")
	}
	visibility := strings.ToLower(strings.TrimSpace(input.Visibility))
	hasVisibility := visibility != ""
	hasShareable := input.Shareable != nil
	if !hasVisibility && !hasShareable {
		return nil, errors.New("at least one of visibility or shareable is required")
	}
	if hasVisibility && visibility != agconvwrite.VisibilityPrivate && visibility != agconvwrite.VisibilityPublic {
		return nil, fmt.Errorf("unsupported visibility: %q", input.Visibility)
	}
	row := agconvwrite.NewMutableConversationView()
	row.SetId(conversationID)
	if hasVisibility {
		row.SetVisibility(visibility)
	}
	if hasShareable {
		if *input.Shareable {
			row.SetShareable(1)
		} else {
			row.SetShareable(0)
		}
	}
	if err := c.conv.PatchConversations(ctx, (*conversation.MutableConversation)(row)); err != nil {
		return nil, err
	}
	return c.conv.GetConversation(ctx, conversationID)
}

func (c *EmbeddedClient) GetMessages(ctx context.Context, input *GetMessagesInput) (*MessagePage, error) {
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}

	if c.data != nil {
		in := &agmessagelist.MessageRowsInput{
			ConversationId: input.ConversationID,
			Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true},
		}
		if input.TurnID != "" {
			in.TurnId = input.TurnID
			in.Has.TurnId = true
		}
		if len(input.Roles) > 0 {
			in.Roles = input.Roles
			in.Has.Roles = true
		}
		if len(input.Types) > 0 {
			in.Types = input.Types
			in.Has.Types = true
		}
		page, err := c.data.GetMessagesPage(ctx, in, input.Page)
		if err == nil {
			return page, nil
		}
	}

	if c.conv == nil {
		return nil, errors.New("data service not configured")
	}
	conv, err := c.conv.GetConversation(ctx, input.ConversationID)
	if err != nil {
		return nil, err
	}
	if conv == nil || len(conv.Transcript) == 0 {
		return &MessagePage{Rows: []*agmessagelist.MessageRowsView{}}, nil
	}
	roleFilter := map[string]bool{}
	for _, role := range input.Roles {
		if role = strings.ToLower(strings.TrimSpace(role)); role != "" {
			roleFilter[role] = true
		}
	}
	typeFilter := map[string]bool{}
	for _, typ := range input.Types {
		if typ = strings.ToLower(strings.TrimSpace(typ)); typ != "" {
			typeFilter[typ] = true
		}
	}
	rows := make([]*agmessagelist.MessageRowsView, 0)
	for _, turn := range conv.Transcript {
		if turn == nil {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil {
				continue
			}
			if strings.TrimSpace(input.TurnID) != "" {
				turnID := strings.TrimSpace(valueOrEmpty(msg.TurnId))
				if turnID != strings.TrimSpace(input.TurnID) {
					continue
				}
			}
			role := strings.ToLower(strings.TrimSpace(msg.Role))
			if len(roleFilter) > 0 && !roleFilter[role] {
				continue
			}
			typ := strings.ToLower(strings.TrimSpace(msg.Type))
			if len(typeFilter) > 0 && !typeFilter[typ] {
				continue
			}
			rows = append(rows, &agmessagelist.MessageRowsView{
				Id:                   msg.Id,
				ConversationId:       msg.ConversationId,
				TurnId:               msg.TurnId,
				Role:                 msg.Role,
				Type:                 msg.Type,
				Status:               msg.Status,
				Content:              msg.Content,
				ElicitationId:        msg.ElicitationId,
				ElicitationPayloadId: msg.ElicitationPayloadId,
				CreatedAt:            msg.CreatedAt,
				ToolName:             msg.ToolName,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].Id < rows[j].Id
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	return &MessagePage{Rows: rows}, nil
}

func (c *EmbeddedClient) StreamEvents(ctx context.Context, input *StreamEventsInput) (streaming.Subscription, error) {
	if c.streaming == nil {
		return nil, errors.New("streaming bus not configured")
	}
	var filter streaming.Filter
	if input != nil {
		filter = input.Filter
	}
	if filter == nil && input != nil && strings.TrimSpace(input.ConversationID) != "" {
		convID := strings.TrimSpace(input.ConversationID)
		filter = func(e *streaming.Event) bool {
			return e != nil && e.StreamID == convID
		}
	}
	return c.streaming.Subscribe(ctx, filter)
}

func (c *EmbeddedClient) CreateConversation(ctx context.Context, input *CreateConversationInput) (*conversation.Conversation, error) {
	if input == nil {
		return nil, errors.New("input is required")
	}
	row := agconvwrite.NewMutableConversationView()
	id := generateID()
	row.SetId(id)
	if strings.TrimSpace(input.AgentID) != "" {
		row.SetAgentId(strings.TrimSpace(input.AgentID))
	}
	if strings.TrimSpace(input.Title) != "" {
		row.SetTitle(strings.TrimSpace(input.Title))
	}
	if len(input.Metadata) > 0 {
		raw, err := json.Marshal(input.Metadata)
		if err != nil {
			return nil, fmt.Errorf("marshal metadata: %w", err)
		}
		s := string(raw)
		row.Metadata = &s
		row.Has.Metadata = true
	}
	if err := c.conv.PatchConversations(ctx, (*conversation.MutableConversation)(row)); err != nil {
		return nil, err
	}
	return c.conv.GetConversation(ctx, id)
}

func (c *EmbeddedClient) ListConversations(ctx context.Context, input *ListConversationsInput) (*ConversationPage, error) {
	in := &agconvlist.ConversationRowsInput{
		Has: &agconvlist.ConversationRowsInputHas{},
	}
	var page *PageInput
	agentID := ""
	if input != nil {
		if strings.TrimSpace(input.AgentID) != "" {
			agentID = strings.TrimSpace(input.AgentID)
			in.AgentId = agentID
			in.Has.AgentId = true
		}
		page = input.Page
	}
	if c.data != nil {
		options := make([]data.Option, 0, 1)
		if userID := strings.TrimSpace(authctx.EffectiveUserID(ctx)); userID != "" {
			options = append(options, data.WithPrincipal(userID))
		}
		out, err := c.data.ListConversations(ctx, in, page, options...)
		if err == nil {
			return out, nil
		}
	}
	if c.conv == nil {
		return nil, errors.New("data service not configured")
	}
	list, err := c.conv.GetConversations(ctx, &conversation.Input{})
	if err != nil {
		return nil, err
	}
	rows := make([]*agconvlist.ConversationRowsView, 0, len(list))
	for _, item := range list {
		if item == nil {
			continue
		}
		if agentID != "" && strings.TrimSpace(valueOrEmpty(item.AgentId)) != agentID {
			continue
		}
		rows = append(rows, &agconvlist.ConversationRowsView{
			Id:                   item.Id,
			AgentId:              item.AgentId,
			Title:                item.Title,
			Visibility:           item.Visibility,
			CreatedAt:            item.CreatedAt,
			LastActivity:         item.LastActivity,
			ConversationParentId: item.ConversationParentId,
			CreatedByUserId:      item.CreatedByUserId,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].Id < rows[j].Id
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	return &ConversationPage{Rows: rows, NextCursor: "", PrevCursor: "", HasMore: false}, nil
}

func (c *EmbeddedClient) GetRun(ctx context.Context, id string) (*agrun.RunRowsView, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	return c.data.GetRun(ctx, id, nil)
}

func (c *EmbeddedClient) CancelTurn(ctx context.Context, turnID string) (bool, error) {
	if c.cancelRegistry == nil {
		return false, errors.New("cancel registry not configured")
	}
	return c.cancelRegistry.CancelTurn(turnID), nil
}

func (c *EmbeddedClient) ResolveElicitation(ctx context.Context, input *ResolveElicitationInput) error {
	if input == nil {
		return errors.New("input is required")
	}
	if c.agent != nil {
		if err := c.agent.ResolveElicitation(ctx, input.ConversationID, input.ElicitationID, input.Action, input.Payload); err != nil {
			return fmt.Errorf("resolve elicitation %q for conversation %q: %w", input.ElicitationID, input.ConversationID, err)
		}
		return nil
	}
	res := &schema.ElicitResult{Action: schema.ElicitResultAction(input.Action), Content: input.Payload}
	if c.elicRouter != nil && c.elicRouter.AcceptByElicitation(input.ConversationID, input.ElicitationID, res) {
		return nil
	}
	return errors.New("elicitation resolver not configured")
}

func (c *EmbeddedClient) ListPendingElicitations(ctx context.Context, input *ListPendingElicitationsInput) ([]*PendingElicitation, error) {
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
				if prev, ok := byElicitation[elicID]; ok {
					if prev.CreatedAt.After(item.CreatedAt) {
						continue
					}
					if prev.CreatedAt.Equal(item.CreatedAt) && strings.Compare(prev.MessageID, item.MessageID) >= 0 {
						continue
					}
				}
				byElicitation[elicID] = item
			}
		}
	}

	// Fallback for in-memory runtimes where conversation state may be present
	// without a Datly-backed data service view.
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
					if prev, ok := byElicitation[elicID]; ok {
						if prev.CreatedAt.After(item.CreatedAt) {
							continue
						}
						if prev.CreatedAt.Equal(item.CreatedAt) && strings.Compare(prev.MessageID, item.MessageID) >= 0 {
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
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ElicitationID < out[j].ElicitationID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (c *EmbeddedClient) ListPendingToolApprovals(ctx context.Context, input *ListPendingToolApprovalsInput) ([]*PendingToolApproval, error) {
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
		item := &PendingToolApproval{
			ID:        row.Id,
			UserID:    row.UserId,
			ToolName:  row.ToolName,
			Status:    row.Status,
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
		}
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

func (c *EmbeddedClient) DecideToolApproval(ctx context.Context, input *DecideToolApprovalInput) (*DecideToolApprovalOutput, error) {
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
	upd.SetUpdatedAt(now)
	switch action {
	case "approve", "accepted":
		upd.SetStatus("approved")
		upd.SetDecision("approve")
		if strings.TrimSpace(input.UserID) != "" {
			upd.SetApprovedByUserId(strings.TrimSpace(input.UserID))
		}
		upd.SetApprovedAt(now)
		if err := patcher.PatchToolApprovalQueue(ctx, upd); err != nil {
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
		done.SetUpdatedAt(time.Now().UTC())
		if execErr != nil {
			done.SetStatus("failed")
			done.SetErrorMessage(execErr.Error())
		} else {
			done.SetStatus("executed")
			done.SetExecutedAt(time.Now().UTC())
		}
		if err := patcher.PatchToolApprovalQueue(ctx, done); err != nil {
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
		if err := patcher.PatchToolApprovalQueue(ctx, upd); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("action must be approve or reject")
	}
	return &DecideToolApprovalOutput{Status: "ok"}, nil
}

func (c *EmbeddedClient) ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
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

func (c *EmbeddedClient) UploadFile(_ context.Context, _ *UploadFileInput) (*UploadFileOutput, error) {
	return nil, errors.New("file operations not yet implemented")
}

func (c *EmbeddedClient) DownloadFile(_ context.Context, _ *DownloadFileInput) (*DownloadFileOutput, error) {
	return nil, errors.New("file operations not yet implemented")
}

func (c *EmbeddedClient) ListFiles(ctx context.Context, input *ListFilesInput) (*ListFilesOutput, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	rows, err := c.data.ListGeneratedFiles(ctx, input.ConversationID)
	if err != nil {
		return nil, err
	}
	out := &ListFilesOutput{}
	for _, r := range rows {
		entry := &FileEntry{ID: r.ID}
		if r.Filename != nil {
			entry.Name = *r.Filename
		}
		if r.MimeType != nil {
			entry.ContentType = *r.MimeType
		}
		if r.SizeBytes != nil {
			entry.Size = int64(*r.SizeBytes)
		}
		out.Files = append(out.Files, entry)
	}
	return out, nil
}

func (c *EmbeddedClient) ListResources(ctx context.Context, input *ListResourcesInput) (*ListResourcesOutput, error) {
	if c.store == nil {
		return nil, errors.New("workspace store not configured")
	}
	if input == nil || strings.TrimSpace(input.Kind) == "" {
		return nil, errors.New("resource kind is required")
	}
	names, err := c.store.List(ctx, input.Kind)
	if err != nil {
		return nil, err
	}
	return &ListResourcesOutput{Names: names}, nil
}

func (c *EmbeddedClient) GetResource(ctx context.Context, input *ResourceRef) (*GetResourceOutput, error) {
	if c.store == nil {
		return nil, errors.New("workspace store not configured")
	}
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return nil, errors.New("resource kind and name are required")
	}
	data, err := c.store.Load(ctx, input.Kind, input.Name)
	if err != nil {
		return nil, err
	}
	return &GetResourceOutput{Kind: input.Kind, Name: input.Name, Data: data}, nil
}

func (c *EmbeddedClient) SaveResource(ctx context.Context, input *SaveResourceInput) error {
	if c.store == nil {
		return errors.New("workspace store not configured")
	}
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return errors.New("resource kind and name are required")
	}
	return c.store.Save(ctx, input.Kind, input.Name, input.Data)
}

func (c *EmbeddedClient) DeleteResource(ctx context.Context, input *ResourceRef) error {
	if c.store == nil {
		return errors.New("workspace store not configured")
	}
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return errors.New("resource kind and name are required")
	}
	return c.store.Delete(ctx, input.Kind, input.Name)
}

func (c *EmbeddedClient) ExportResources(ctx context.Context, input *ExportResourcesInput) (*ExportResourcesOutput, error) {
	if c.store == nil {
		return nil, errors.New("workspace store not configured")
	}
	kinds := input.Kinds
	if len(kinds) == 0 {
		kinds = workspace.AllKinds()
	}
	out := &ExportResourcesOutput{}
	for _, kind := range kinds {
		names, err := c.store.List(ctx, kind)
		if err != nil {
			continue
		}
		for _, name := range names {
			data, err := c.store.Load(ctx, kind, name)
			if err != nil {
				continue
			}
			out.Resources = append(out.Resources, Resource{Kind: kind, Name: name, Data: data})
		}
	}
	return out, nil
}

func (c *EmbeddedClient) ImportResources(ctx context.Context, input *ImportResourcesInput) (*ImportResourcesOutput, error) {
	if c.store == nil {
		return nil, errors.New("workspace store not configured")
	}
	if input == nil {
		return nil, errors.New("input is required")
	}
	out := &ImportResourcesOutput{}
	for _, r := range input.Resources {
		if strings.TrimSpace(r.Kind) == "" || strings.TrimSpace(r.Name) == "" {
			continue
		}
		if !input.Replace {
			exists, err := c.store.Exists(ctx, r.Kind, r.Name)
			if err == nil && exists {
				out.Skipped++
				continue
			}
		}
		if err := c.store.Save(ctx, r.Kind, r.Name, r.Data); err != nil {
			continue
		}
		out.Imported++
	}
	return out, nil
}

func (c *EmbeddedClient) GetTranscript(ctx context.Context, input *GetTranscriptInput) (*TranscriptOutput, error) {
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	var opts []conversation.Option
	if input.Since != "" {
		opts = append(opts, conversation.WithSince(input.Since))
	}
	if input.IncludeModelCalls {
		opts = append(opts, conversation.WithIncludeModelCall(true))
	}
	if input.IncludeToolCalls {
		opts = append(opts, conversation.WithIncludeToolCall(true))
	}
	conv, err := c.conv.GetConversation(ctx, input.ConversationID, opts...)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return &TranscriptOutput{Turns: nil}, nil
	}
	turns := conv.GetTranscript()
	return &TranscriptOutput{Turns: turns}, nil
}

func (c *EmbeddedClient) TerminateConversation(ctx context.Context, conversationID string) error {
	if c.agent == nil {
		return errors.New("agent service not configured")
	}
	return c.agent.Terminate(ctx, conversationID)
}

func (c *EmbeddedClient) CompactConversation(ctx context.Context, conversationID string) error {
	if c.agent == nil {
		return errors.New("agent service not configured")
	}
	return c.agent.Compact(ctx, conversationID)
}

func (c *EmbeddedClient) PruneConversation(ctx context.Context, conversationID string) error {
	if c.agent == nil {
		return errors.New("agent service not configured")
	}
	return c.agent.Prune(ctx, conversationID)
}

func (c *EmbeddedClient) GetA2AAgentCard(ctx context.Context, agentID string) (*a2a.AgentCard, error) {
	if c.a2aSvc == nil {
		return nil, errors.New("A2A service not configured")
	}
	return c.a2aSvc.GetAgentCard(ctx, agentID)
}

func (c *EmbeddedClient) SendA2AMessage(ctx context.Context, agentID string, req *a2a.SendMessageRequest) (*a2a.SendMessageResponse, error) {
	if c.a2aSvc == nil {
		return nil, errors.New("A2A service not configured")
	}
	return c.a2aSvc.SendMessage(ctx, agentID, req)
}

func (c *EmbeddedClient) ListA2AAgents(ctx context.Context, agentIDs []string) ([]string, error) {
	if c.a2aSvc == nil {
		return nil, errors.New("A2A service not configured")
	}
	return c.a2aSvc.ListA2AAgents(ctx, agentIDs)
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func generateID() string { return uuid.New().String() }
