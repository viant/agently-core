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
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
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
	skillproto "github.com/viant/agently-core/protocol/skill"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/a2a"
	agentsvc "github.com/viant/agently-core/service/agent"
	elicsvc "github.com/viant/agently-core/service/elicitation"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/agently-core/service/scheduler"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
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

type backendClient struct {
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
	skills         skillBackend
}

type skillBackend interface {
	ListForConversation(ctx context.Context, conversationID string) ([]skillproto.Metadata, []string, error)
	ActivateForConversation(ctx context.Context, conversationID, name, args string) (string, error)
	Diagnostics() []string
}

func newBackend(agent *agentsvc.Service, conv conversation.Client) (*backendClient, error) {
	if agent == nil {
		return nil, errors.New("in-process backend requires non-nil agent service")
	}
	if conv == nil {
		return nil, errors.New("in-process backend requires non-nil conversation client")
	}
	return &backendClient{agent: agent, conv: conv}, nil
}

// Deprecated: use NewBackendFromRuntime for server wiring and
// NewLocalHTTPFromRuntime for SDK callers.
//
// NewEmbedded builds the legacy in-process backend wrapper.
// Prefer NewBackendFromRuntime for server wiring and NewLocalHTTPFromRuntime
// for SDK callers that should exercise the public endpoint contract.
func NewEmbedded(agent *agentsvc.Service, conv conversation.Client) (Backend, error) {
	return newBackend(agent, conv)
}

// Deprecated: use NewBackendFromRuntime for server wiring and
// NewLocalHTTPFromRuntime for SDK callers.
//
// NewEmbeddedFromRuntime builds an in-process backend over a runtime.
// Prefer NewLocalHTTPFromRuntime for SDK callers that should exercise the
// public endpoint contract instead of calling services directly.
func NewEmbeddedFromRuntime(rt *executor.Runtime) (Backend, error) {
	return newBackendFromRuntime(rt)
}

func newBackendFromRuntime(rt *executor.Runtime) (*backendClient, error) {
	if rt == nil {
		return nil, errors.New("runtime was nil")
	}
	c, err := newBackend(rt.Agent, rt.Conversation)
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
	c.skills = rt.Skills
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

func (c *backendClient) Mode() Mode { return ModeEmbedded }

func (c *backendClient) Query(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
	if c.feeds != nil && c.streaming != nil {
		ctx = toolexec.WithFeedNotifier(ctx, newFeedNotifier(c.feeds, c.streaming))
	}
	out := &agentsvc.QueryOutput{}
	if err := c.agent.Query(ctx, input, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *backendClient) GetConversation(ctx context.Context, id string) (*conversation.Conversation, error) {
	return c.conv.GetConversation(ctx, id)
}

func (c *backendClient) UpdateConversation(ctx context.Context, input *UpdateConversationInput) (*conversation.Conversation, error) {
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

func (c *backendClient) GetMessages(ctx context.Context, input *GetMessagesInput) (*MessagePage, error) {
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

func (c *backendClient) StreamEvents(ctx context.Context, input *StreamEventsInput) (streaming.Subscription, error) {
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

func (c *backendClient) CreateConversation(ctx context.Context, input *CreateConversationInput) (*conversation.Conversation, error) {
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
	if strings.TrimSpace(input.ParentConversationID) != "" {
		row.SetConversationParentId(strings.TrimSpace(input.ParentConversationID))
	}
	if strings.TrimSpace(input.ParentTurnID) != "" {
		row.SetConversationParentTurnId(strings.TrimSpace(input.ParentTurnID))
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
	out := &conversation.Conversation{
		Id:                       id,
		CreatedAt:                time.Now(),
		AgentId:                  row.AgentId,
		Title:                    row.Title,
		ConversationParentId:     row.ConversationParentId,
		ConversationParentTurnId: row.ConversationParentTurnId,
		CreatedByUserId:          row.CreatedByUserID,
		Metadata:                 row.Metadata,
		Visibility:               "private",
		Shareable:                0,
	}
	return out, nil
}

func (c *backendClient) ListConversations(ctx context.Context, input *ListConversationsInput) (*ConversationPage, error) {
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
	queryInput := &conversation.Input{Has: &agconv.ConversationInputHas{}}
	if agentID != "" {
		queryInput.AgentId = agentID
		queryInput.Has.AgentId = true
	}
	if strings.TrimSpace(in.ParentId) != "" {
		queryInput.ParentId = strings.TrimSpace(in.ParentId)
		queryInput.Has.ParentId = true
	}
	if strings.TrimSpace(in.ParentTurnId) != "" {
		queryInput.ParentTurnId = strings.TrimSpace(in.ParentTurnId)
		queryInput.Has.ParentTurnId = true
	}
	if in.ExcludeScheduled {
		queryInput.ExcludeScheduled = true
		queryInput.Has.ExcludeScheduled = true
	}
	if query != "" {
		queryInput.Query = query
		queryInput.Has.Query = true
	}
	if status != "" {
		queryInput.StatusFilter = status
		queryInput.Has.StatusFilter = true
	}
	if !queryInput.Has.ParentId && !queryInput.Has.ParentTurnId {
		queryInput.ExcludeChildren = true
		queryInput.Has.ExcludeChildren = true
	}
	list, err := c.conv.GetConversations(ctx, queryInput)
	if err != nil {
		return nil, err
	}
	rows := make([]*agconvlist.ConversationRowsView, 0, len(list))
	for _, item := range list {
		if item == nil {
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

func (c *backendClient) ListLinkedConversations(ctx context.Context, input *ListLinkedConversationsInput) (*LinkedConversationPage, error) {
	if input == nil {
		return nil, errors.New("input is required")
	}
	parentID := strings.TrimSpace(input.ParentConversationID)
	parentTurnID := strings.TrimSpace(input.ParentTurnID)
	if parentID == "" && parentTurnID == "" {
		return nil, errors.New("parent conversation ID or parent turn ID is required")
	}
	if c.conv == nil {
		return nil, errors.New("conversation client not configured")
	}
	query := &conversation.Input{Has: &agconv.ConversationInputHas{}}
	if parentID != "" {
		query.ParentId = parentID
		query.Has.ParentId = true
	}
	if parentTurnID != "" {
		query.ParentTurnId = parentTurnID
		query.Has.ParentTurnId = true
	}
	list, err := c.conv.GetConversations(ctx, query)
	if err != nil {
		return nil, err
	}
	rows := make([]*LinkedConversationEntry, 0, len(list))
	for _, item := range list {
		if item == nil {
			continue
		}
		entryParentID := strings.TrimSpace(valueOrEmpty(item.ConversationParentId))
		entryParentTurnID := strings.TrimSpace(valueOrEmpty(item.ConversationParentTurnId))
		entry := &LinkedConversationEntry{
			ConversationID:       item.Id,
			ParentConversationID: entryParentID,
			ParentTurnID:         entryParentTurnID,
			AgentID:              strings.TrimSpace(valueOrEmpty(item.AgentId)),
			Title:                strings.TrimSpace(valueOrEmpty(item.Title)),
			Status:               strings.TrimSpace(valueOrEmpty(item.Status)),
			CreatedAt:            item.CreatedAt,
			UpdatedAt:            item.UpdatedAt,
		}
		entry.Response = strings.TrimSpace(c.latestAssistantResponse(ctx, item.Id))
		if entry.Response == "" && item.Summary != nil {
			entry.Response = strings.TrimSpace(*item.Summary)
		}
		rows = append(rows, entry)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return lessTimeAndID(rows[j].CreatedAt, rows[j].ConversationID, rows[i].CreatedAt, rows[i].ConversationID)
	})
	return paginateLinkedConversationEntries(rows, input.Page), nil
}

func paginateLinkedConversationEntries(rows []*LinkedConversationEntry, page *PageInput) *LinkedConversationPage {
	limit := 50
	direction := DirectionBefore
	cursor := ""
	if page != nil {
		if page.Limit > 0 {
			limit = page.Limit
		}
		if page.Direction != "" {
			direction = page.Direction
		}
		cursor = strings.TrimSpace(page.Cursor)
	}
	start := 0
	if cursor != "" {
		index := -1
		for i, row := range rows {
			if row != nil && strings.TrimSpace(row.ConversationID) == cursor {
				index = i
				break
			}
		}
		if index >= 0 {
			switch direction {
			case DirectionAfter:
				if index-limit+1 > 0 {
					start = index - limit + 1
				}
				rows = rows[start : index+1]
				start = 0
			case DirectionLatest:
				start = 0
			default:
				start = index + 1
			}
		}
	}
	if start > len(rows) {
		start = len(rows)
	}
	sliced := rows[start:]
	pageOut := &LinkedConversationPage{Rows: []*LinkedConversationEntry{}}
	if len(sliced) > limit {
		pageOut.HasMore = true
		sliced = sliced[:limit]
	}
	pageOut.Rows = sliced
	if len(sliced) > 0 {
		pageOut.PrevCursor = sliced[0].ConversationID
		pageOut.NextCursor = sliced[len(sliced)-1].ConversationID
	}
	return pageOut
}

func (c *backendClient) GetRun(ctx context.Context, id string) (*agrun.RunRowsView, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	return c.data.GetRun(ctx, id, nil)
}

// principalDataOpts returns a data.Option slice carrying the authenticated
// caller's subject so data-layer reads/writes run the conversation-ownership
// check. When ctx has no effective user (scheduler, background recovery,
// local/anonymous mode) the slice is empty — matching today's behaviour for
// those paths.
func principalDataOpts(ctx context.Context) []data.Option {
	if userID := strings.TrimSpace(authctx.EffectiveUserID(ctx)); userID != "" {
		return []data.Option{data.WithPrincipal(userID)}
	}
	return nil
}

// authorizeTurnAccess looks the turn up by ID with the caller's principal so
// the data layer's authorizeConversationID check fires. Returns the turn
// view on success, a conflict error when the turn does not exist, or
// ErrPermissionDenied when the caller does not own the conversation.
func (c *backendClient) authorizeTurnAccess(ctx context.Context, turnID string) (*agturnbyid.TurnLookupView, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	in := &agturnbyid.TurnLookupInput{
		ID:  strings.TrimSpace(turnID),
		Has: &agturnbyid.TurnLookupInputHas{ID: true},
	}
	turn, err := c.data.GetTurnByID(ctx, in, principalDataOpts(ctx)...)
	if err != nil {
		if isTurnLookupUnavailable(err) {
			return nil, newConflictError("turn not found")
		}
		return nil, err
	}
	if turn == nil {
		return nil, newConflictError("turn not found")
	}
	return turn, nil
}

func (c *backendClient) CancelTurn(ctx context.Context, turnID string) (bool, error) {
	if c.cancelRegistry == nil {
		return false, nil
	}
	// Verify the caller owns the conversation this turn belongs to. Without
	// this check, any authenticated user could cancel any turn whose UUID
	// they can guess or observe. The check only runs when there is an
	// authenticated subject on ctx — scheduler/background/local paths that
	// legitimately operate without a principal (or embedded tests with no
	// DAO) fall through to the original best-effort cancel.
	if c.data != nil && len(principalDataOpts(ctx)) > 0 {
		if _, err := c.authorizeTurnAccess(ctx, turnID); err != nil {
			return false, err
		}
	}
	return c.cancelRegistry.CancelTurn(turnID), nil
}

func (c *backendClient) SteerTurn(ctx context.Context, input *SteerTurnInput) (*SteerTurnOutput, error) {
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
	}, principalDataOpts(ctx)...)
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

func (c *backendClient) CancelQueuedTurn(ctx context.Context, conversationID, turnID string) error {
	if c.data == nil || c.conv == nil {
		return errors.New("data service not configured")
	}
	turn, err := c.data.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{
		ID:             strings.TrimSpace(turnID),
		ConversationID: strings.TrimSpace(conversationID),
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

func (c *backendClient) MoveQueuedTurn(ctx context.Context, input *MoveQueuedTurnInput) error {
	return moveQueuedTurn(c, ctx, input)
}
func (c *backendClient) EditQueuedTurn(ctx context.Context, input *EditQueuedTurnInput) error {
	return editQueuedTurn(c, ctx, input)
}
func (c *backendClient) ForceSteerQueuedTurn(ctx context.Context, conversationID, turnID string) (*SteerTurnOutput, error) {
	return forceSteerQueuedTurn(c, ctx, conversationID, turnID)
}
func (c *backendClient) ResolveElicitation(ctx context.Context, input *ResolveElicitationInput) error {
	return resolveElicitation(c, ctx, input)
}
func (c *backendClient) ListPendingElicitations(ctx context.Context, input *ListPendingElicitationsInput) ([]*PendingElicitation, error) {
	return listPendingElicitations(c, ctx, input)
}
func (c *backendClient) ListPendingToolApprovals(ctx context.Context, input *ListPendingToolApprovalsInput) (*PendingToolApprovalPage, error) {
	return listPendingToolApprovals(c, ctx, input)
}
func (c *backendClient) DecideToolApproval(ctx context.Context, input *DecideToolApprovalInput) (*DecideToolApprovalOutput, error) {
	return decideToolApproval(c, ctx, input)
}
func (c *backendClient) ListToolDefinitions(_ context.Context) ([]ToolDefinitionInfo, error) {
	return listToolDefinitions(c)
}
func (c *backendClient) ListSkills(ctx context.Context, input *ListSkillsInput) (*ListSkillsOutput, error) {
	if c.skills == nil {
		return &ListSkillsOutput{}, nil
	}
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	items, diags, err := c.skills.ListForConversation(ctx, strings.TrimSpace(input.ConversationID))
	if err != nil {
		return nil, err
	}
	out := &ListSkillsOutput{Diagnostics: diags}
	for _, item := range items {
		out.Items = append(out.Items, SkillItem{Name: item.Name, Description: item.Description})
	}
	return out, nil
}
func (c *backendClient) ActivateSkill(ctx context.Context, input *ActivateSkillInput) (*ActivateSkillOutput, error) {
	if c.skills == nil {
		return nil, errors.New("skills service not configured")
	}
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	if strings.TrimSpace(input.Name) == "" {
		return nil, errors.New("skill name is required")
	}
	body, err := c.skills.ActivateForConversation(ctx, strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.Name), strings.TrimSpace(input.Args))
	if err != nil {
		return nil, err
	}
	return &ActivateSkillOutput{Name: strings.TrimSpace(input.Name), Body: body}, nil
}
func (c *backendClient) GetSkillDiagnostics(ctx context.Context) (*SkillDiagnosticsOutput, error) {
	_ = ctx
	if c.skills == nil {
		return &SkillDiagnosticsOutput{}, nil
	}
	return &SkillDiagnosticsOutput{Items: c.skills.Diagnostics()}, nil
}
func (c *backendClient) ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return executeTool(c, ctx, name, args)
}
func (c *backendClient) ListTemplates(ctx context.Context, input *ListTemplatesInput) (*ListTemplatesOutput, error) {
	return listTemplates(c, ctx, input)
}
func (c *backendClient) GetTemplate(ctx context.Context, input *GetTemplateInput) (*GetTemplateOutput, error) {
	return getTemplate(c, ctx, input)
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
