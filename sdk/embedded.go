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
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agturnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	turnqueueread "github.com/viant/agently-core/pkg/agently/turnqueue/read"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	"github.com/viant/agently-core/pkg/mcpname"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/a2a"
	agentsvc "github.com/viant/agently-core/service/agent"
	elicsvc "github.com/viant/agently-core/service/elicitation"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/agently-core/service/scheduler"
	executil "github.com/viant/agently-core/service/shared/executil"
	"github.com/viant/agently-core/workspace"
)

type toolApprovalQueueLister interface {
	ListToolApprovalQueues(ctx context.Context, in *queueRead.QueueRowsInput) ([]*queueRead.QueueRowView, error)
}

type toolApprovalQueuePatcher interface {
	PatchToolApprovalQueue(ctx context.Context, queue *queueWrite.ToolApprovalQueue) error
}

type turnQueueLister interface {
	ListTurnQueueRows(ctx context.Context, in *turnqueueread.QueueRowsInput) ([]*turnqueueread.QueueRowView, error)
}

type turnQueuePatcher interface {
	PatchTurnQueue(ctx context.Context, in *turnqueuewrite.TurnQueue) error
}

type EmbeddedClient struct {
	agent          *agentsvc.Service
	conv           conversation.Client
	data           data.Service
	registry       tool.Registry
	toolPolicy     *tool.Policy
	cancelRegistry cancels.Registry
	elicRouter     elicrouter.ElicitationRouter
	elicSvc        *elicsvc.Service
	streaming      streaming.Bus
	store          workspace.Store
	a2aSvc         *a2a.Service
	schedulerSvc   *scheduler.Service
	feeds          *FeedRegistry
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
	c.elicSvc = rt.Elicitation
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
	c.feeds = NewFeedRegistry()
	if rt.DAO != nil && rt.Agent != nil {
		store, err := scheduler.NewDatlyStore(context.Background(), rt.DAO, rt.Data)
		if err != nil {
			return nil, err
		}
		c.SetScheduler(scheduler.New(store, rt.Agent,
			scheduler.WithConversationClient(rt.Conversation),
			scheduler.WithTokenProvider(rt.TokenProvider),
			scheduler.WithAuthConfig(rt.AuthConfig),
		))
	}
	return c, nil
}

func (c *EmbeddedClient) Mode() Mode { return ModeEmbedded }

func (c *EmbeddedClient) Query(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
	if c.feeds != nil && c.streaming != nil {
		ctx = executil.WithFeedNotifier(ctx, newFeedNotifier(c.feeds, c.streaming))
	}
	out := &agentsvc.QueryOutput{}
	if err := c.agent.Query(ctx, input, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *EmbeddedClient) GetConversation(ctx context.Context, id string) (*conversation.Conversation, error) {
	return c.conv.GetConversation(ctx, id)
}

func (c *EmbeddedClient) UpdateConversation(ctx context.Context, input *UpdateConversationInput) (*conversation.Conversation, error) {
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
	title := strings.TrimSpace(input.Title)
	hasTitle := title != ""
	visibility := strings.ToLower(strings.TrimSpace(input.Visibility))
	hasVisibility := visibility != ""
	hasShareable := input.Shareable != nil
	if !hasTitle && !hasVisibility && !hasShareable {
		return nil, errors.New("at least one of title, visibility, or shareable is required")
	}
	if hasVisibility && visibility != agconvwrite.VisibilityPrivate && visibility != agconvwrite.VisibilityPublic {
		return nil, fmt.Errorf("unsupported visibility: %q", input.Visibility)
	}
	row := agconvwrite.NewMutableConversationView()
	row.SetId(conversationID)
	if hasTitle {
		row.SetTitle(title)
	}
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
		if input.ID != "" {
			in.Id = input.ID
			in.Has.Id = true
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
			normalizeMessagePage(page)
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
			if strings.TrimSpace(input.ID) != "" && strings.TrimSpace(msg.Id) != strings.TrimSpace(input.ID) {
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
			toolName := msg.ToolName
			if toolName != nil {
				name := mcpname.Display(strings.TrimSpace(*toolName))
				toolName = &name
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
				ToolName:             toolName,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return lessTimeAndID(rows[i].CreatedAt, rows[i].Id, rows[j].CreatedAt, rows[j].Id)
	})
	return &MessagePage{Rows: rows}, nil
}

func normalizeMessagePage(page *MessagePage) {
	if page == nil {
		return
	}
	for _, row := range page.Rows {
		if row == nil || row.ToolName == nil {
			continue
		}
		name := mcpname.Display(strings.TrimSpace(*row.ToolName))
		row.ToolName = &name
	}
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
	if userID := strings.TrimSpace(authctx.EffectiveUserID(ctx)); userID != "" {
		row.SetCreatedByUserID(userID)
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
	in := &agconvlist.ConversationRowsInput{Has: &agconvlist.ConversationRowsInputHas{}}
	var page *PageInput
	agentID := ""
	query := ""
	status := ""
	if input != nil {
		if strings.TrimSpace(input.AgentID) != "" {
			agentID = strings.TrimSpace(input.AgentID)
			in.AgentId = agentID
			in.Has.AgentId = true
		}
		if strings.TrimSpace(input.ParentID) != "" {
			in.ParentId = strings.TrimSpace(input.ParentID)
			in.Has.ParentId = true
		}
		if strings.TrimSpace(input.ParentTurnID) != "" {
			in.ParentTurnId = strings.TrimSpace(input.ParentTurnID)
			in.Has.ParentTurnId = true
		}
		if input.ExcludeScheduled {
			in.ExcludeScheduled = true
			in.Has.ExcludeScheduled = true
		}
		if strings.TrimSpace(input.Query) != "" {
			query = strings.TrimSpace(input.Query)
			in.Query = query
			in.Has.Query = true
		}
		if strings.TrimSpace(input.Status) != "" {
			status = strings.TrimSpace(input.Status)
			in.StatusFilter = status
			in.Has.StatusFilter = true
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
		if status != "" && !strings.EqualFold(strings.TrimSpace(valueOrEmpty(item.Status)), status) {
			continue
		}
		if query != "" {
			text := strings.ToLower(strings.TrimSpace(valueOrEmpty(item.Title) + " " + valueOrEmpty(item.Summary) + " " + item.Id))
			if !strings.Contains(text, strings.ToLower(query)) {
				continue
			}
		}
		if input != nil && input.ExcludeScheduled && item.ScheduleId != nil && strings.TrimSpace(*item.ScheduleId) != "" {
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
		return lessTimeAndID(rows[i].CreatedAt, rows[i].Id, rows[j].CreatedAt, rows[j].Id)
	})
	return &ConversationPage{Rows: rows, NextCursor: "", PrevCursor: "", HasMore: false}, nil
}

func (c *EmbeddedClient) ListLinkedConversations(ctx context.Context, input *ListLinkedConversationsInput) (*LinkedConversationPage, error) {
	if input == nil {
		return nil, errors.New("input is required")
	}
	parentID := strings.TrimSpace(input.ParentConversationID)
	parentTurnID := strings.TrimSpace(input.ParentTurnID)
	if parentID == "" && parentTurnID == "" {
		return nil, errors.New("parent conversation ID or parent turn ID is required")
	}
	page, err := c.ListConversations(ctx, &ListConversationsInput{
		ParentID:     parentID,
		ParentTurnID: parentTurnID,
		Page:         input.Page,
	})
	if err != nil {
		return nil, err
	}
	result := &LinkedConversationPage{
		Rows:       make([]*LinkedConversationEntry, 0, len(page.Rows)),
		NextCursor: page.NextCursor,
		PrevCursor: page.PrevCursor,
		HasMore:    page.HasMore,
	}
	for _, row := range page.Rows {
		if row == nil {
			continue
		}
		entry := &LinkedConversationEntry{
			ConversationID: row.Id,
			AgentID:        strings.TrimSpace(valueOrEmpty(row.AgentId)),
			Title:          strings.TrimSpace(valueOrEmpty(row.Title)),
			CreatedAt:      row.CreatedAt,
			UpdatedAt:      row.UpdatedAt,
		}
		if row.ConversationParentId != nil {
			entry.ParentConversationID = strings.TrimSpace(*row.ConversationParentId)
		}
		if row.ConversationParentTurnId != nil {
			entry.ParentTurnID = strings.TrimSpace(*row.ConversationParentTurnId)
		}
		if row.Status != nil {
			entry.Status = strings.TrimSpace(*row.Status)
		}
		entry.Response = strings.TrimSpace(c.latestAssistantResponse(ctx, row.Id))
		if entry.Response == "" && row.Summary != nil {
			entry.Response = strings.TrimSpace(*row.Summary)
		}
		result.Rows = append(result.Rows, entry)
	}
	return result, nil
}

func (c *EmbeddedClient) GetRun(ctx context.Context, id string) (*agrun.RunRowsView, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	return c.data.GetRun(ctx, id, nil)
}

func (c *EmbeddedClient) CancelTurn(ctx context.Context, turnID string) (bool, error) {
	if c.cancelRegistry == nil {
		return false, nil
	}
	return c.cancelRegistry.CancelTurn(turnID), nil
}

func (c *EmbeddedClient) SteerTurn(ctx context.Context, input *SteerTurnInput) (*SteerTurnOutput, error) {
	if input == nil {
		return nil, errors.New("input is required")
	}
	if c.data == nil || c.conv == nil {
		return nil, errors.New("data service not configured")
	}
	turn, err := c.data.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{
		ID:             strings.TrimSpace(input.TurnID),
		ConversationID: strings.TrimSpace(input.ConversationID),
		Has:            &agturnbyid.TurnLookupInputHas{ID: true, ConversationID: true},
	})
	if err != nil {
		if isTurnLookupUnavailable(err) {
			return nil, newConflictError("turn not found")
		}
		return nil, err
	}
	if turn == nil {
		return nil, newConflictError("turn not found")
	}
	status := strings.ToLower(strings.TrimSpace(turn.Status))
	if status != "running" && status != "waiting_for_user" {
		return nil, newConflictError(fmt.Sprintf("turn is not currently running: %s", turn.Status))
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return nil, errors.New("content is required")
	}
	role := strings.TrimSpace(input.Role)
	if role == "" {
		role = "user"
	}
	msg := conversation.NewMessage()
	msg.SetId(uuid.NewString())
	msg.SetConversationID(strings.TrimSpace(input.ConversationID))
	msg.SetTurnID(strings.TrimSpace(input.TurnID))
	msg.SetRole(role)
	msg.SetType("task")
	msg.SetContent(content)
	msg.SetRawContent(content)
	msg.SetCreatedAt(time.Now())
	if userID := strings.TrimSpace(authctx.EffectiveUserID(ctx)); userID != "" {
		msg.SetCreatedByUserID(userID)
	}
	if err := c.conv.PatchMessage(ctx, msg); err != nil {
		return nil, err
	}
	return &SteerTurnOutput{MessageID: msg.Id, TurnID: input.TurnID, Status: "accepted"}, nil
}

func (c *EmbeddedClient) CancelQueuedTurn(ctx context.Context, conversationID, turnID string) error {
	if c.data == nil || c.conv == nil {
		return errors.New("data service not configured")
	}
	turn, err := c.data.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{
		ID:             strings.TrimSpace(turnID),
		ConversationID: strings.TrimSpace(conversationID),
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
	upd := &agturnwrite.MutableTurnView{Has: &agturnwrite.TurnHas{}}
	upd.SetId(strings.TrimSpace(turnID))
	upd.SetStatus("canceled")
	if _, err := c.data.PatchTurns(ctx, []*agturnwrite.MutableTurnView{upd}); err != nil {
		return err
	}
	if patcher, ok := c.data.(turnQueuePatcher); ok {
		q := &turnqueuewrite.TurnQueue{Has: &turnqueuewrite.TurnQueueHas{}}
		q.SetId(strings.TrimSpace(turnID))
		q.SetStatus("canceled")
		q.SetUpdatedAt(time.Now())
		if err := patcher.PatchTurnQueue(ctx, q); err != nil {
			return err
		}
	}
	starterID := strings.TrimSpace(valueOrEmpty(turn.StartedByMessageId))
	if starterID == "" {
		starterID = strings.TrimSpace(turnID)
	}
	msg := conversation.NewMessage()
	msg.SetId(starterID)
	msg.SetStatus("cancel")
	_ = c.conv.PatchMessage(ctx, msg)
	return nil
}

func (c *EmbeddedClient) MoveQueuedTurn(ctx context.Context, input *MoveQueuedTurnInput) error {
	return moveQueuedTurn(c, ctx, input)
}
func (c *EmbeddedClient) EditQueuedTurn(ctx context.Context, input *EditQueuedTurnInput) error {
	return editQueuedTurn(c, ctx, input)
}
func (c *EmbeddedClient) ForceSteerQueuedTurn(ctx context.Context, conversationID, turnID string) (*SteerTurnOutput, error) {
	return forceSteerQueuedTurn(c, ctx, conversationID, turnID)
}
func (c *EmbeddedClient) ResolveElicitation(ctx context.Context, input *ResolveElicitationInput) error {
	return resolveElicitation(c, ctx, input)
}
func (c *EmbeddedClient) ListPendingElicitations(ctx context.Context, input *ListPendingElicitationsInput) ([]*PendingElicitation, error) {
	return listPendingElicitations(c, ctx, input)
}
func (c *EmbeddedClient) ListPendingToolApprovals(ctx context.Context, input *ListPendingToolApprovalsInput) ([]*PendingToolApproval, error) {
	return listPendingToolApprovals(c, ctx, input)
}
func (c *EmbeddedClient) DecideToolApproval(ctx context.Context, input *DecideToolApprovalInput) (*DecideToolApprovalOutput, error) {
	return decideToolApproval(c, ctx, input)
}
func (c *EmbeddedClient) ListToolDefinitions(_ context.Context) ([]ToolDefinitionInfo, error) {
	return listToolDefinitions(c)
}
func (c *EmbeddedClient) ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return executeTool(c, ctx, name, args)
}

func isToolApprovalQueueDuplicateErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") && strings.Contains(msg, "tool_approval_queue") && strings.Contains(msg, "id")
}

func isTurnLookupUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "couldn't match uri") && strings.Contains(msg, "/v1/api/agently/turn/byid/byid")
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func generateID() string { return uuid.New().String() }
