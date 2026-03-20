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
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agturnlist "github.com/viant/agently-core/pkg/agently/turn/queuedList"
	agturnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	turnqueueread "github.com/viant/agently-core/pkg/agently/turnqueue/read"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	"github.com/viant/agently-core/pkg/mcpname"
	"github.com/viant/agently-core/protocol/agent/plan"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/a2a"
	agentsvc "github.com/viant/agently-core/service/agent"
	elicsvc "github.com/viant/agently-core/service/elicitation"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/agently-core/service/scheduler"
	executil "github.com/viant/agently-core/service/shared/executil"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/mcp-protocol/schema"
	hstate "github.com/viant/xdatly/handler/state"
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
	// Initialize feed registry from workspace.
	c.feeds = NewFeedRegistry()
	if rt.DAO != nil && rt.Agent != nil {
		store, err := scheduler.NewDatlyStore(context.Background(), rt.DAO, rt.Data)
		if err != nil {
			return nil, err
		}
		c.SetScheduler(scheduler.New(store, rt.Agent,
			scheduler.WithConversationClient(rt.Conversation),
			scheduler.WithTokenProvider(rt.TokenProvider),
		))
	}
	return c, nil
}

// FeedRegistry returns the feed registry for external use.
func (c *EmbeddedClient) FeedRegistry() *FeedRegistry {
	return c.feeds
}

// ResolveFeedData extracts feed data from conversation transcript by matching
// tool call outputs against the feed spec's data source selectors.
// Returns the matched tool call response payloads keyed by "output".
func (c *EmbeddedClient) ResolveFeedData(ctx context.Context, spec *FeedSpec, conversationID string) (interface{}, error) {
	if spec == nil || conversationID == "" || c.conv == nil {
		return nil, nil
	}
	conv, err := c.conv.GetConversation(ctx, conversationID,
		conversation.WithIncludeTranscript(true),
		conversation.WithIncludeToolCall(true))
	if err != nil || conv == nil {
		return nil, err
	}
	useLast := strings.ToLower(strings.TrimSpace(spec.Activation.Scope)) != "all"
	transcript := conv.GetTranscript()
	// Scan transcript for matching tool call messages.
	for i := len(transcript) - 1; i >= 0; i-- {
		turn := transcript[i]
		if turn == nil {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil || msg.ToolName == nil {
				continue
			}
			toolSvc, toolMtd := parseToolName(*msg.ToolName)
			if !matchesRule(spec.Match, toolSvc, toolMtd) {
				continue
			}
			// Extract content as the tool response data.
			content := ""
			if msg.Content != nil {
				content = strings.TrimSpace(*msg.Content)
			}
			if content == "" {
				continue
			}
			// Try to parse as JSON for structured data.
			var parsed interface{}
			if err := json.Unmarshal([]byte(content), &parsed); err == nil {
				if useLast {
					return map[string]interface{}{"output": parsed}, nil
				}
			}
		}
	}
	return nil, nil
}

// RecordOOBAuthElicitation creates a proper elicitation record for an OAuth
// authentication URL. This creates a real DB entry so resolve/decline works
// normally through the ElicitationOverlay — no fake SSE events needed.
func (c *EmbeddedClient) RecordOOBAuthElicitation(ctx context.Context, authURL string) error {
	if c.elicSvc == nil {
		return fmt.Errorf("elicitation service not configured")
	}
	convID := memory.ConversationIDFromContext(ctx)
	turnID := ""
	if turn, ok := memory.TurnMetaFromContext(ctx); ok {
		turnID = turn.TurnID
		if convID == "" {
			convID = turn.ConversationID
		}
	}
	if convID == "" {
		return fmt.Errorf("no conversation in context for OOB auth")
	}
	turn := memory.TurnMeta{ConversationID: convID, TurnID: turnID}
	elic := &plan.Elicitation{}
	elic.Message = "MCP server requires authentication. Please sign in to continue."
	elic.Mode = "url"
	elic.Url = authURL
	_, err := c.elicSvc.Record(ctx, &turn, "assistant", elic)
	return err
}

// resolveActiveFeeds scans transcript tool calls against the feed registry
// and returns active feeds with their data for the transcript response.
// resolveActiveFeedsFromState scans canonical state tool steps for matching
// feeds and fetches their response payloads.
func (c *EmbeddedClient) resolveActiveFeedsFromState(ctx context.Context, state *ConversationState) []*ActiveFeedState {
	if c.feeds == nil || state == nil || len(state.Turns) == 0 {
		return nil
	}
	// Collect matching tool call payloads per feed, then merge.
	// scope:last = last TURN's matching calls (not last individual call).
	type feedCollector struct {
		spec     *FeedSpec
		payloads []string
		turnIdx  int // track which turn provided payloads
	}
	collectors := map[string]*feedCollector{}
	for turnIdx, turn := range state.Turns {
		if turn == nil || turn.Execution == nil {
			continue
		}
		for _, page := range turn.Execution.Pages {
			if page == nil {
				continue
			}
			for _, step := range page.ToolSteps {
				if step == nil {
					continue
				}
				matched := c.feeds.Match(step.ToolName)
				for _, spec := range matched {
					col, exists := collectors[spec.ID]
					if !exists {
						col = &feedCollector{spec: spec, turnIdx: turnIdx}
						collectors[spec.ID] = col
					}
					// scope:last = only keep payloads from the latest turn.
					isAll := strings.EqualFold(strings.TrimSpace(spec.Activation.Scope), "all")
					if !isAll && col.turnIdx != turnIdx {
						col.payloads = nil // new turn, reset
						col.turnIdx = turnIdx
					}
					// Fetch response payload.
					content := ""
					if step.ResponsePayloadID != "" && c.conv != nil {
						if p, err := c.conv.GetPayload(ctx, step.ResponsePayloadID); err == nil && p != nil && p.InlineBody != nil {
							content = strings.TrimSpace(string(*p.InlineBody))
						}
					}
					if content == "" && step.RequestPayloadID != "" && c.conv != nil {
						if p, err := c.conv.GetPayload(ctx, step.RequestPayloadID); err == nil && p != nil && p.InlineBody != nil {
							content = strings.TrimSpace(string(*p.InlineBody))
						}
					}
					if content != "" {
						col.payloads = append(col.payloads, content)
					}
				}
			}
		}
	}
	// Build results from collected payloads.
	var result []*ActiveFeedState
	for _, col := range collectors {
		if len(col.payloads) == 0 {
			continue
		}
		rootData := mergeFeedPayloads(col.payloads)
		// Apply feed-specific enrichments.
		if strings.EqualFold(col.spec.ID, "plan") {
			enrichPlanData(rootData)
		}
		if strings.EqualFold(col.spec.ID, "explorer") {
			enrichExplorerData(rootData)
		}
		itemCount := 0
		if output, ok := rootData["output"].(map[string]interface{}); ok {
			raw, _ := json.Marshal(output)
			itemCount = estimateItemCount(string(raw))
		}
		if entries, ok := rootData["entries"].([]interface{}); ok && len(entries) > itemCount {
			itemCount = len(entries)
		}
		if itemCount == 0 {
			continue
		}
		result = append(result, &ActiveFeedState{
			FeedID:    col.spec.ID,
			Title:     col.spec.Title,
			ItemCount: itemCount,
			Data:      rootData,
		})
	}
	return result
}

// Deprecated: use resolveActiveFeedsFromState instead.
func (c *EmbeddedClient) resolveActiveFeeds(turns conversation.Transcript) []*ActiveFeedState {
	if c.feeds == nil || len(turns) == 0 {
		return nil
	}
	// Use the Transcript's UniqueToolNames to find matching feeds, then
	// fetch their data from the call_payload table directly via tool_call records.
	toolNames := turns.UniqueToolNames()
	feedResults := map[string]*ActiveFeedState{}
	for _, toolName := range toolNames {
		matched := c.feeds.Match(toolName)
		for _, spec := range matched {
			if _, exists := feedResults[spec.ID]; exists {
				continue
			}
			// Find the last tool call for this tool and fetch its response payload.
			content := c.findLastToolCallPayload(turns, toolName)
			var data interface{}
			if content != "" {
				var parsed interface{}
				if err := json.Unmarshal([]byte(content), &parsed); err == nil {
					data = map[string]interface{}{"output": parsed}
				}
			}
			itemCount := 0
			if content != "" {
				itemCount = estimateItemCount(content)
			}
			feedResults[spec.ID] = &ActiveFeedState{
				FeedID:    spec.ID,
				Title:     spec.Title,
				ItemCount: itemCount,
				Data:      data,
			}
		}
	}
	if len(feedResults) == 0 {
		return nil
	}
	result := make([]*ActiveFeedState, 0, len(feedResults))
	for _, f := range feedResults {
		// Only include feeds that have actual data.
		if f.ItemCount > 0 || f.Data != nil {
			result = append(result, f)
		}
	}
	return result
}

// findLastToolCallPayload scans turns in reverse for a matching tool call
// and fetches its response payload content.
func (c *EmbeddedClient) findLastToolCallPayload(turns conversation.Transcript, targetTool string) string {
	target := strings.ToLower(strings.TrimSpace(targetTool))
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn == nil {
			continue
		}
		for j := len(turn.Message) - 1; j >= 0; j-- {
			msg := turn.Message[j]
			if msg == nil || msg.ToolName == nil {
				continue
			}
			if strings.ToLower(strings.TrimSpace(*msg.ToolName)) != target {
				continue
			}
			// Try message content first.
			if msg.Content != nil && strings.TrimSpace(*msg.Content) != "" {
				return strings.TrimSpace(*msg.Content)
			}
			// Fetch the tool call's response payload by message ID.
			// The tool_call table links message_id → response_payload_id.
			if c.conv != nil {
				payloadContent := c.fetchToolCallResponsePayload(msg.Id)
				if payloadContent != "" {
					return payloadContent
				}
			}
		}
	}
	return ""
}

// fetchToolCallResponsePayload fetches the response payload for a tool call message.
// It looks up the tool_call record by message_id, gets the response_payload_id,
// then fetches the payload body.
func (c *EmbeddedClient) fetchToolCallResponsePayload(messageID string) string {
	if c.conv == nil || messageID == "" {
		return ""
	}
	// Get the tool call's response payload ID via the message's tool call view.
	// Since ToolMessage children aren't reliably loaded, try fetching the payload
	// using the message ID directly — the tool_call.message_id matches this.
	msg, err := c.conv.GetMessage(context.Background(), messageID)
	if err != nil || msg == nil {
		return ""
	}
	// Check ToolMessage children if loaded.
	for _, tm := range msg.ToolMessage {
		if tm == nil || tm.ToolCall == nil {
			continue
		}
		if tm.ToolCall.ResponsePayloadId != nil {
			payloadID := strings.TrimSpace(*tm.ToolCall.ResponsePayloadId)
			if payloadID != "" {
				if p, err := c.conv.GetPayload(context.Background(), payloadID); err == nil && p != nil && p.InlineBody != nil {
					return strings.TrimSpace(string(*p.InlineBody))
				}
			}
		}
	}
	return ""
}

func (c *EmbeddedClient) Mode() Mode { return ModeEmbedded }

// GetPayload returns a payload body/metadata by payload id.
// This is intentionally not part of the public sdk.Client interface; it is
// used by the HTTP handler when running in embedded mode.
func (c *EmbeddedClient) GetPayload(ctx context.Context, id string) (*conversation.Payload, error) {
	if c.conv == nil {
		return nil, errors.New("conversation client not configured")
	}
	payloadID := strings.TrimSpace(id)
	if payloadID == "" {
		return nil, errors.New("payload ID is required")
	}
	return c.conv.GetPayload(ctx, payloadID)
}

func (c *EmbeddedClient) Query(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
	// Inject feed notifier so tool completion emits SSE feed events.
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
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].Id < rows[j].Id
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
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
	// Set the owner from the authenticated user context so the conversation
	// is visible in user-scoped ListConversations queries.
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
	in := &agconvlist.ConversationRowsInput{
		Has: &agconvlist.ConversationRowsInputHas{},
	}
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
			text := strings.ToLower(strings.TrimSpace(
				valueOrEmpty(item.Title) + " " + valueOrEmpty(item.Summary) + " " + item.Id,
			))
			if !strings.Contains(text, strings.ToLower(query)) {
				continue
			}
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

func (c *EmbeddedClient) latestAssistantResponse(ctx context.Context, conversationID string) string {
	state, err := c.GetTranscript(ctx, &GetTranscriptInput{ConversationID: conversationID})
	if err != nil || state == nil {
		return ""
	}
	for i := len(state.Turns) - 1; i >= 0; i-- {
		turn := state.Turns[i]
		if turn == nil || turn.Assistant == nil {
			continue
		}
		if turn.Assistant.Final != nil {
			if text := strings.TrimSpace(turn.Assistant.Final.Content); text != "" {
				return text
			}
		}
		if turn.Assistant.Preamble != nil {
			if text := strings.TrimSpace(turn.Assistant.Preamble.Content); text != "" {
				return text
			}
		}
	}
	return ""
}

func (c *EmbeddedClient) GetRun(ctx context.Context, id string) (*agrun.RunRowsView, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	return c.data.GetRun(ctx, id, nil)
}

func (c *EmbeddedClient) CancelTurn(ctx context.Context, turnID string) (bool, error) {
	if c.cancelRegistry == nil {
		// Keep API behavior graceful for runtimes that don't wire cancellation.
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
			if row == nil {
				continue
			}
			rows = append(rows, queueRow{ID: strings.TrimSpace(row.Id), QueueSeq: row.QueueSeq})
		}
	} else {
		fallbackRows, err := c.data.ListQueuedTurns(ctx, &agturnlist.QueuedTurnsInput{
			ConversationID: strings.TrimSpace(input.ConversationID),
			Has:            &agturnlist.QueuedTurnsInputHas{ConversationID: true},
		})
		if err != nil {
			return err
		}
		for _, row := range fallbackRows {
			if row == nil {
				continue
			}
			seq := int64(time.Now().UnixNano())
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
	aSeq := a.QueueSeq
	bSeq := b.QueueSeq
	updA := &agturnwrite.MutableTurnView{Has: &agturnwrite.TurnHas{}}
	updA.SetId(strings.TrimSpace(a.ID))
	updA.SetQueueSeq(bSeq)
	updB := &agturnwrite.MutableTurnView{Has: &agturnwrite.TurnHas{}}
	updB.SetId(strings.TrimSpace(b.ID))
	updB.SetQueueSeq(aSeq)
	if _, err := c.data.PatchTurns(ctx, []*agturnwrite.MutableTurnView{updA, updB}); err != nil {
		return err
	}
	if patcher, ok := c.data.(turnQueuePatcher); ok {
		qa := &turnqueuewrite.TurnQueue{Has: &turnqueuewrite.TurnQueueHas{}}
		qa.SetId(strings.TrimSpace(a.ID))
		qa.SetQueueSeq(bSeq)
		qa.SetUpdatedAt(time.Now())
		if err := patcher.PatchTurnQueue(ctx, qa); err != nil {
			return err
		}
		qb := &turnqueuewrite.TurnQueue{Has: &turnqueuewrite.TurnQueueHas{}}
		qb.SetId(strings.TrimSpace(b.ID))
		qb.SetQueueSeq(aSeq)
		qb.SetUpdatedAt(time.Now())
		if err := patcher.PatchTurnQueue(ctx, qb); err != nil {
			return err
		}
	}
	return nil
}

func (c *EmbeddedClient) EditQueuedTurn(ctx context.Context, input *EditQueuedTurnInput) error {
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

func (c *EmbeddedClient) ForceSteerQueuedTurn(ctx context.Context, conversationID, turnID string) (*SteerTurnOutput, error) {
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
				item.Elicitation = c.resolveElicitationPayload(ctx, elicID, valueOrEmpty(row.ElicitationPayloadId), valueOrEmpty(row.Content))
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
					item.Elicitation = c.resolveMessageElicitation(ctx, msg)
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

func isToolApprovalQueueDuplicateErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") &&
		strings.Contains(msg, "tool_approval_queue") &&
		strings.Contains(msg, "id")
}

func (c *EmbeddedClient) UploadFile(_ context.Context, _ *UploadFileInput) (*UploadFileOutput, error) {
	return nil, errors.New("file operations not yet implemented")
}

func (c *EmbeddedClient) DownloadFile(ctx context.Context, input *DownloadFileInput) (*DownloadFileOutput, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	if input == nil || strings.TrimSpace(input.ConversationID) == "" || strings.TrimSpace(input.FileID) == "" {
		return nil, errors.New("conversation ID and file ID are required")
	}
	rows, err := c.data.ListGeneratedFiles(ctx, input.ConversationID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.ID) != strings.TrimSpace(input.FileID) || row.PayloadID == nil || strings.TrimSpace(*row.PayloadID) == "" {
			continue
		}
		payload, err := c.GetPayload(ctx, strings.TrimSpace(*row.PayloadID))
		if err != nil {
			return nil, err
		}
		if payload == nil {
			return nil, nil
		}
		out := &DownloadFileOutput{}
		if row.Filename != nil {
			out.Name = *row.Filename
		}
		if row.MimeType != nil {
			out.ContentType = *row.MimeType
		}
		if payload.InlineBody != nil {
			out.Data = make([]byte, len(*payload.InlineBody))
			copy(out.Data, *payload.InlineBody)
		}
		return out, nil
	}
	return nil, nil
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

func (c *EmbeddedClient) GetTranscript(ctx context.Context, input *GetTranscriptInput, options ...TranscriptOption) (*ConversationState, error) {
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	optState := &transcriptOptions{}
	for _, option := range options {
		if option != nil {
			option(optState)
		}
	}
	sinceMessageID := ""
	sinceTurnID := ""
	if since := strings.TrimSpace(input.Since); since != "" {
		sinceTurnID = since
		if msg, err := c.conv.GetMessage(ctx, since); err == nil && msg != nil && msg.TurnId != nil && strings.TrimSpace(*msg.TurnId) != "" {
			sinceMessageID = since
			sinceTurnID = strings.TrimSpace(*msg.TurnId)
		}
	}
	conv, err := c.getTranscriptConversation(ctx, input.ConversationID, sinceTurnID, input, optState)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return &ConversationState{ConversationID: input.ConversationID}, nil
	}
	turns := conv.GetTranscript()
	c.enrichTranscriptElicitations(ctx, turns)
	pruneTranscriptNoise(turns)
	if sinceMessageID != "" {
		turns = filterTranscriptSinceMessage(turns, sinceMessageID)
	}
	state := BuildCanonicalState(input.ConversationID, turns)
	// Resolve active feeds from canonical state tool steps (opt-in, adds payload fetch overhead).
	if c.feeds != nil && state != nil && optState != nil && optState.includeFeeds {
		state.Feeds = c.resolveActiveFeedsFromState(context.Background(), state)
	}
	return state, nil
}

func (c *EmbeddedClient) getTranscriptConversation(ctx context.Context, conversationID, sinceTurnID string, input *GetTranscriptInput, optsState *transcriptOptions) (*conversation.Conversation, error) {
	selectors := map[string]*QuerySelector(nil)
	if optsState != nil {
		selectors = optsState.selectors
	}
	includeModelCalls := true
	includeToolCalls := true
	if c.data != nil && len(selectors) > 0 {
		in := &agconv.ConversationInput{
			Id:                conversationID,
			IncludeTranscript: true,
			IncludeModelCal:   includeModelCalls,
			IncludeToolCall:   includeToolCalls,
			Has: &agconv.ConversationInputHas{
				Id:                true,
				IncludeTranscript: true,
				IncludeModelCal:   true,
				IncludeToolCall:   true,
			},
		}
		if strings.TrimSpace(sinceTurnID) != "" {
			in.Since = sinceTurnID
			in.Has.Since = true
		}
		dataOpts := make([]data.Option, 0, len(selectors))
		if namedSelectors := buildTranscriptQuerySelectors(selectors); len(namedSelectors) > 0 {
			dataOpts = append(dataOpts, data.WithQuerySelector(namedSelectors...))
		}
		got, err := c.data.GetConversation(ctx, conversationID, in, dataOpts...)
		if err != nil {
			return nil, err
		}
		if got == nil {
			return nil, nil
		}
		return (*conversation.Conversation)(got), nil
	}

	var opts []conversation.Option
	opts = append(opts,
		conversation.WithIncludeModelCall(includeModelCalls),
		conversation.WithIncludeToolCall(includeToolCalls),
	)
	if strings.TrimSpace(sinceTurnID) != "" {
		opts = append(opts, conversation.WithSince(sinceTurnID))
	}
	return c.conv.GetConversation(ctx, conversationID, opts...)
}

func buildTranscriptQuerySelectors(selectors map[string]*QuerySelector) []*hstate.NamedQuerySelector {
	if len(selectors) == 0 {
		return nil
	}
	names := []string{TranscriptSelectorTurn, TranscriptSelectorMessage, TranscriptSelectorToolMessage}
	result := make([]*hstate.NamedQuerySelector, 0, len(selectors))
	for _, name := range names {
		selector := selectors[name]
		if selector == nil {
			continue
		}
		result = append(result, &hstate.NamedQuerySelector{
			Name: name,
			QuerySelector: hstate.QuerySelector{
				Limit:   selector.Limit,
				Offset:  selector.Offset,
				OrderBy: selector.OrderBy,
			},
		})
	}
	return result
}

func (c *EmbeddedClient) enrichTranscriptElicitations(ctx context.Context, turns conversation.Transcript) {
	for _, turn := range turns {
		if turn == nil || len(turn.Message) == 0 {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil {
				continue
			}
			if elicitation := c.resolveMessageElicitation(ctx, msg); len(elicitation) > 0 {
				if content, ok := elicitation["message"].(string); ok {
					content = strings.TrimSpace(content)
					if content != "" && shouldNormalizeElicitationContent(valueOrEmpty(msg.Content)) {
						msg.Content = &content
					}
				}
			}
		}
	}
}

func shouldNormalizeElicitationContent(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return true
	}
	return strings.HasPrefix(content, "{") || strings.HasPrefix(content, "map[")
}

func pruneTranscriptNoise(turns conversation.Transcript) {
	for _, turn := range turns {
		if turn == nil || len(turn.Message) == 0 {
			continue
		}
		filtered := turn.Message[:0]
		for _, msg := range turn.Message {
			if shouldDropTranscriptMessage(msg) {
				continue
			}
			filtered = append(filtered, msg)
		}
		turn.Message = filtered
	}
}

func shouldDropTranscriptMessage(msg *agconv.MessageView) bool {
	if msg == nil {
		return true
	}
	if msg.Interim != 1 {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
		return false
	}
	if strings.TrimSpace(valueOrEmpty(msg.Content)) != "" || strings.TrimSpace(valueOrEmpty(msg.RawContent)) != "" {
		return false
	}
	if strings.TrimSpace(valueOrEmpty(msg.Preamble)) != "" {
		return false
	}
	if msg.ElicitationId != nil && strings.TrimSpace(*msg.ElicitationId) != "" {
		return false
	}
	if msg.ModelCall != nil || len(msg.ToolMessage) > 0 {
		return false
	}
	return true
}

func (c *EmbeddedClient) resolveMessageElicitation(ctx context.Context, msg *agconv.MessageView) map[string]interface{} {
	if msg == nil || msg.ElicitationId == nil || strings.TrimSpace(*msg.ElicitationId) == "" {
		return nil
	}
	if msg.Elicitation != nil {
		return msg.Elicitation
	}
	return c.resolveElicitationPayload(ctx, strings.TrimSpace(*msg.ElicitationId), valueOrEmpty(msg.ElicitationPayloadId), valueOrEmpty(msg.Content))
}

func (c *EmbeddedClient) resolveElicitationPayload(ctx context.Context, elicitationID, payloadID, content string) map[string]interface{} {
	elicitationID = strings.TrimSpace(elicitationID)
	if elicitationID == "" {
		return nil
	}
	payloadID = strings.TrimSpace(payloadID)
	content = strings.TrimSpace(content)
	if payloadID != "" {
		if payload, err := c.GetPayload(ctx, payloadID); err == nil && payload != nil && payload.InlineBody != nil && len(*payload.InlineBody) > 0 {
			var elicitation map[string]interface{}
			if err = json.Unmarshal(*payload.InlineBody, &elicitation); err == nil {
				elicitation["elicitationId"] = elicitationID
				if content != "" {
					if _, ok := elicitation["message"]; !ok {
						elicitation["message"] = content
					}
				}
				return elicitation
			}
		}
	}
	if content != "" {
		var elicitation plan.Elicitation
		if err := json.Unmarshal([]byte(content), &elicitation); err == nil && !elicitation.IsEmpty() {
			raw, err := json.Marshal(elicitation)
			if err == nil {
				out := map[string]interface{}{}
				if err = json.Unmarshal(raw, &out); err == nil {
					out["elicitationId"] = elicitationID
					return out
				}
			}
		}
	}
	return nil
}

func filterTranscriptSinceMessage(turns conversation.Transcript, messageID string) conversation.Transcript {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return turns
	}
	result := make(conversation.Transcript, 0, len(turns))
	found := false
	for _, turn := range turns {
		if turn == nil {
			continue
		}
		if !found {
			index := -1
			for i, msg := range turn.Message {
				if msg != nil && strings.TrimSpace(msg.Id) == messageID {
					index = i
					break
				}
			}
			if index == -1 {
				continue
			}
			found = true
			cloned := *turn
			cloned.Message = append([]*agconv.MessageView(nil), turn.Message[index:]...)
			result = append(result, &cloned)
			continue
		}
		result = append(result, turn)
	}
	if found {
		return result
	}
	return turns
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

// SetScheduler sets the scheduler service on the embedded client.
func (c *EmbeddedClient) SetScheduler(svc *scheduler.Service) {
	c.schedulerSvc = svc
}

func (c *EmbeddedClient) GetSchedule(ctx context.Context, id string) (*scheduler.Schedule, error) {
	if c.schedulerSvc == nil {
		return nil, errors.New("scheduler service not configured")
	}
	return c.schedulerSvc.Get(ctx, id)
}

func (c *EmbeddedClient) ListSchedules(ctx context.Context) ([]*scheduler.Schedule, error) {
	if c.schedulerSvc == nil {
		return nil, errors.New("scheduler service not configured")
	}
	return c.schedulerSvc.List(ctx)
}

func (c *EmbeddedClient) UpsertSchedules(ctx context.Context, schedules []*scheduler.Schedule) error {
	if c.schedulerSvc == nil {
		return errors.New("scheduler service not configured")
	}
	for _, s := range schedules {
		if err := c.schedulerSvc.Upsert(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func (c *EmbeddedClient) RunScheduleNow(ctx context.Context, id string) error {
	if c.schedulerSvc == nil {
		return errors.New("scheduler service not configured")
	}
	return c.schedulerSvc.RunNow(ctx, id)
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
