package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/viant/agently-core/app/store/conversation"
	agrun "github.com/viant/agently-core/pkg/agently/run"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/a2a"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/scheduler"
)

type HTTPOption func(*HTTPClient)

func WithHTTPClient(client *http.Client) HTTPOption {
	return func(c *HTTPClient) {
		if client != nil {
			c.client = client
		}
	}
}

func WithQueryPath(path string) HTTPOption {
	return func(c *HTTPClient) {
		if strings.TrimSpace(path) != "" {
			c.queryPath = path
		}
	}
}

func WithConversationsPath(path string) HTTPOption {
	return func(c *HTTPClient) {
		if strings.TrimSpace(path) != "" {
			c.conversationsPath = path
		}
	}
}

func WithSchedulerPath(path string) HTTPOption {
	return func(c *HTTPClient) {
		if strings.TrimSpace(path) != "" {
			c.schedulerPath = path
		}
	}
}

// WithAuthToken sets a static Bearer token that is sent with every request.
func WithAuthToken(token string) HTTPOption {
	return func(c *HTTPClient) {
		c.authToken = strings.TrimSpace(token)
	}
}

// TokenProvider is a function that returns a current auth token.
// It is called before each request, allowing dynamic token refresh.
type TokenProvider func(ctx context.Context) (string, error)

// WithTokenProvider sets a dynamic token provider called before each request.
func WithTokenProvider(p TokenProvider) HTTPOption {
	return func(c *HTTPClient) {
		c.tokenProvider = p
	}
}

type HTTPClient struct {
	baseURL           string
	client            *http.Client
	authToken         string
	tokenProvider     TokenProvider
	queryPath         string
	conversationsPath string
	messagesPath      string
	runsPath          string
	turnsPath         string
	elicitationsPath  string
	toolsPath         string
	streamPath        string
	filesPath         string
	resourcesPath     string
	toolApprovalsPath string
	schedulerPath     string
}

func NewHTTP(baseURL string, opts ...HTTPOption) (*HTTPClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	c := &HTTPClient{
		baseURL:           strings.TrimRight(baseURL, "/"),
		client:            http.DefaultClient,
		queryPath:         "/v1/agent/query",
		conversationsPath: "/v1/conversations",
		messagesPath:      "/v1/messages",
		runsPath:          "/v1/runs",
		turnsPath:         "/v1/turns",
		elicitationsPath:  "/v1/elicitations",
		toolsPath:         "/v1/tools",
		streamPath:        "/v1/stream",
		filesPath:         "/v1/files",
		resourcesPath:     "/v1/workspace/resources",
		toolApprovalsPath: "/v1/tool-approvals",
		schedulerPath:     "/v1/api/agently/scheduler",
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c, nil
}

func (c *HTTPClient) Mode() Mode { return ModeHTTP }

func (c *HTTPClient) Query(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
	var out agentsvc.QueryOutput
	if err := c.doJSON(ctx, http.MethodPost, c.queryPath, input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) GetWorkspaceMetadata(ctx context.Context) (*WorkspaceMetadata, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/v1/workspace/metadata", nil, "")
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("request failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := &WorkspaceMetadata{}
	if data, ok := raw["data"]; ok {
		var wrapped WorkspaceMetadata
		if err := json.Unmarshal(data, &wrapped); err == nil {
			out = &wrapped
		}
	} else {
		blob, _ := json.Marshal(raw)
		if err := json.Unmarshal(blob, out); err != nil {
			return nil, err
		}
	}
	if out.DefaultAgent == "" && out.Defaults != nil {
		out.DefaultAgent = out.Defaults.Agent
	}
	if out.DefaultModel == "" && out.Defaults != nil {
		out.DefaultModel = out.Defaults.Model
	}
	if out.DefaultEmbedder == "" && out.Defaults != nil {
		out.DefaultEmbedder = out.Defaults.Embedder
	}
	return out, nil
}

func (c *HTTPClient) GetConversation(ctx context.Context, id string) (*conversation.Conversation, error) {
	var out conversation.Conversation
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(strings.TrimSpace(id))
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) UpdateConversation(ctx context.Context, input *UpdateConversationInput) (*conversation.Conversation, error) {
	if input == nil {
		return nil, errors.New("input is required")
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	if conversationID == "" {
		return nil, errors.New("conversation ID is required")
	}
	visibility := strings.TrimSpace(input.Visibility)
	if visibility == "" && input.Shareable == nil {
		return nil, errors.New("at least one of visibility or shareable is required")
	}
	body := struct {
		Visibility string `json:"visibility,omitempty"`
		Shareable  *bool  `json:"shareable,omitempty"`
	}{
		Visibility: visibility,
		Shareable:  input.Shareable,
	}
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(conversationID)
	var out conversation.Conversation
	if err := c.doJSON(ctx, http.MethodPatch, path, &body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) GetMessages(ctx context.Context, input *GetMessagesInput) (*MessagePage, error) {
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	q := url.Values{}
	q.Set("conversationId", input.ConversationID)
	if input.TurnID != "" {
		q.Set("turnId", input.TurnID)
	}
	if len(input.Roles) > 0 {
		q.Set("roles", strings.Join(input.Roles, ","))
	}
	if len(input.Types) > 0 {
		q.Set("types", strings.Join(input.Types, ","))
	}
	if input.Page != nil {
		if input.Page.Limit > 0 {
			q.Set("limit", fmt.Sprintf("%d", input.Page.Limit))
		}
		if input.Page.Cursor != "" {
			q.Set("cursor", input.Page.Cursor)
		}
		if input.Page.Direction != "" {
			q.Set("direction", string(input.Page.Direction))
		}
	}
	var out MessagePage
	path := c.messagesPath + "?" + q.Encode()
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) StreamEvents(ctx context.Context, input *StreamEventsInput) (streaming.Subscription, error) {
	q := url.Values{}
	if input != nil && strings.TrimSpace(input.ConversationID) != "" {
		q.Set("conversationId", input.ConversationID)
	}
	sseURL := c.baseURL + c.streamPath
	if len(q) > 0 {
		sseURL += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	if err != nil {
		return nil, err
	}
	if err := c.applyAuth(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("stream request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return newSSESubscription(ctx, resp.Body, input), nil
}

func (c *HTTPClient) CreateConversation(ctx context.Context, input *CreateConversationInput) (*conversation.Conversation, error) {
	if input == nil {
		return nil, errors.New("input is required")
	}
	var out conversation.Conversation
	if err := c.doJSON(ctx, http.MethodPost, c.conversationsPath, input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ListConversations(ctx context.Context, input *ListConversationsInput) (*ConversationPage, error) {
	q := url.Values{}
	if input != nil {
		if strings.TrimSpace(input.AgentID) != "" {
			q.Set("agentId", input.AgentID)
		}
		if strings.TrimSpace(input.ParentID) != "" {
			q.Set("parentId", input.ParentID)
		}
		if strings.TrimSpace(input.ParentTurnID) != "" {
			q.Set("parentTurnId", input.ParentTurnID)
		}
		if input.ExcludeScheduled {
			q.Set("excludeScheduled", "true")
		}
		if strings.TrimSpace(input.Query) != "" {
			q.Set("q", input.Query)
		}
		if strings.TrimSpace(input.Status) != "" {
			q.Set("status", input.Status)
		}
		if input.Page != nil {
			if input.Page.Limit > 0 {
				q.Set("limit", fmt.Sprintf("%d", input.Page.Limit))
			}
			if input.Page.Cursor != "" {
				q.Set("cursor", input.Page.Cursor)
			}
			if input.Page.Direction != "" {
				q.Set("direction", string(input.Page.Direction))
			}
		}
	}
	var out ConversationPage
	path := c.conversationsPath
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ListLinkedConversations(ctx context.Context, input *ListLinkedConversationsInput) (*LinkedConversationPage, error) {
	q := url.Values{}
	if input != nil {
		if strings.TrimSpace(input.ParentConversationID) != "" {
			q.Set("parentId", input.ParentConversationID)
		}
		if strings.TrimSpace(input.ParentTurnID) != "" {
			q.Set("parentTurnId", input.ParentTurnID)
		}
		if input.Page != nil {
			if input.Page.Limit > 0 {
				q.Set("limit", fmt.Sprintf("%d", input.Page.Limit))
			}
			if input.Page.Cursor != "" {
				q.Set("cursor", input.Page.Cursor)
			}
			if input.Page.Direction != "" {
				q.Set("direction", string(input.Page.Direction))
			}
		}
	}
	var out LinkedConversationPage
	path := strings.TrimRight(c.conversationsPath, "/") + "/linked"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) GetRun(ctx context.Context, id string) (*agrun.RunRowsView, error) {
	var out agrun.RunRowsView
	path := strings.TrimRight(c.runsPath, "/") + "/" + url.PathEscape(strings.TrimSpace(id))
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) CancelTurn(ctx context.Context, turnID string) (bool, error) {
	path := strings.TrimRight(c.turnsPath, "/") + "/" + url.PathEscape(strings.TrimSpace(turnID)) + "/cancel"
	var out struct {
		Cancelled bool `json:"cancelled"`
	}
	if err := c.doJSON(ctx, http.MethodPost, path, nil, &out); err != nil {
		return false, err
	}
	return out.Cancelled, nil
}

func (c *HTTPClient) SteerTurn(ctx context.Context, input *SteerTurnInput) (*SteerTurnOutput, error) {
	if input == nil {
		return nil, errors.New("input is required")
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	turnID := strings.TrimSpace(input.TurnID)
	if conversationID == "" || turnID == "" {
		return nil, errors.New("conversation ID and turn ID are required")
	}
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(conversationID) + "/turns/" + url.PathEscape(turnID) + "/steer"
	var out SteerTurnOutput
	if err := c.doJSON(ctx, http.MethodPost, path, input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) CancelQueuedTurn(ctx context.Context, conversationID, turnID string) error {
	conversationID = strings.TrimSpace(conversationID)
	turnID = strings.TrimSpace(turnID)
	if conversationID == "" || turnID == "" {
		return errors.New("conversation ID and turn ID are required")
	}
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(conversationID) + "/turns/" + url.PathEscape(turnID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *HTTPClient) MoveQueuedTurn(ctx context.Context, input *MoveQueuedTurnInput) error {
	if input == nil {
		return errors.New("input is required")
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	turnID := strings.TrimSpace(input.TurnID)
	if conversationID == "" || turnID == "" {
		return errors.New("conversation ID and turn ID are required")
	}
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(conversationID) + "/turns/" + url.PathEscape(turnID) + "/move"
	return c.doJSON(ctx, http.MethodPost, path, input, nil)
}

func (c *HTTPClient) EditQueuedTurn(ctx context.Context, input *EditQueuedTurnInput) error {
	if input == nil {
		return errors.New("input is required")
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	turnID := strings.TrimSpace(input.TurnID)
	if conversationID == "" || turnID == "" {
		return errors.New("conversation ID and turn ID are required")
	}
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(conversationID) + "/turns/" + url.PathEscape(turnID)
	return c.doJSON(ctx, http.MethodPatch, path, input, nil)
}

func (c *HTTPClient) ForceSteerQueuedTurn(ctx context.Context, conversationID, turnID string) (*SteerTurnOutput, error) {
	conversationID = strings.TrimSpace(conversationID)
	turnID = strings.TrimSpace(turnID)
	if conversationID == "" || turnID == "" {
		return nil, errors.New("conversation ID and turn ID are required")
	}
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(conversationID) + "/turns/" + url.PathEscape(turnID) + "/force-steer"
	var out SteerTurnOutput
	if err := c.doJSON(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ResolveElicitation(ctx context.Context, input *ResolveElicitationInput) error {
	if input == nil {
		return errors.New("input is required")
	}
	path := strings.TrimRight(c.elicitationsPath, "/") + "/" +
		url.PathEscape(input.ConversationID) + "/" +
		url.PathEscape(input.ElicitationID) + "/resolve"
	body := map[string]interface{}{
		"action":  input.Action,
		"payload": input.Payload,
	}
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}

func (c *HTTPClient) ListPendingElicitations(ctx context.Context, input *ListPendingElicitationsInput) ([]*PendingElicitation, error) {
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	q := url.Values{}
	q.Set("conversationId", strings.TrimSpace(input.ConversationID))
	path := c.elicitationsPath + "?" + q.Encode()
	var out struct {
		Rows []*PendingElicitation `json:"rows"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out.Rows == nil {
		return []*PendingElicitation{}, nil
	}
	return out.Rows, nil
}

func (c *HTTPClient) ListPendingToolApprovals(ctx context.Context, input *ListPendingToolApprovalsInput) ([]*PendingToolApproval, error) {
	q := url.Values{}
	if input != nil {
		if strings.TrimSpace(input.UserID) != "" {
			q.Set("userId", strings.TrimSpace(input.UserID))
		}
		if strings.TrimSpace(input.ConversationID) != "" {
			q.Set("conversationId", strings.TrimSpace(input.ConversationID))
		}
		if strings.TrimSpace(input.Status) != "" {
			q.Set("status", strings.TrimSpace(input.Status))
		}
	}
	path := strings.TrimRight(c.toolApprovalsPath, "/") + "/pending"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Rows []*PendingToolApproval `json:"rows"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out.Rows == nil {
		return []*PendingToolApproval{}, nil
	}
	return out.Rows, nil
}

func (c *HTTPClient) DecideToolApproval(ctx context.Context, input *DecideToolApprovalInput) (*DecideToolApprovalOutput, error) {
	if input == nil || strings.TrimSpace(input.ID) == "" {
		return nil, errors.New("approval id is required")
	}
	path := strings.TrimRight(c.toolApprovalsPath, "/") + "/" + url.PathEscape(strings.TrimSpace(input.ID)) + "/decision"
	var out DecideToolApprovalOutput
	if err := c.doJSON(ctx, http.MethodPost, path, input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ListToolDefinitions(ctx context.Context) ([]ToolDefinitionInfo, error) {
	var out []ToolDefinitionInfo
	if err := c.doJSON(ctx, http.MethodGet, "/v1/tools", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	path := strings.TrimRight(c.toolsPath, "/") + "/" + url.PathEscape(name) + "/execute"
	var out struct {
		Result string `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, path, args, &out); err != nil {
		return "", err
	}
	return out.Result, nil
}

func (c *HTTPClient) UploadFile(_ context.Context, _ *UploadFileInput) (*UploadFileOutput, error) {
	return nil, errors.New("file operations not yet implemented")
}

func (c *HTTPClient) DownloadFile(ctx context.Context, input *DownloadFileInput) (*DownloadFileOutput, error) {
	if input == nil || strings.TrimSpace(input.ConversationID) == "" || strings.TrimSpace(input.FileID) == "" {
		return nil, errors.New("conversation ID and file ID are required")
	}
	q := url.Values{}
	q.Set("conversationId", strings.TrimSpace(input.ConversationID))
	q.Set("raw", "1")
	path := strings.TrimRight(c.filesPath, "/") + "/" + url.PathEscape(strings.TrimSpace(input.FileID)) + "?" + q.Encode()
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download file: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	out := &DownloadFileOutput{
		ContentType: strings.TrimSpace(resp.Header.Get("Content-Type")),
		Data:        data,
	}
	if disposition := strings.TrimSpace(resp.Header.Get("Content-Disposition")); disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil {
			out.Name = strings.TrimSpace(params["filename"])
		}
	}
	return out, nil
}

func (c *HTTPClient) ListFiles(ctx context.Context, input *ListFilesInput) (*ListFilesOutput, error) {
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	q := url.Values{}
	q.Set("conversationId", strings.TrimSpace(input.ConversationID))
	var out ListFilesOutput
	if err := c.doJSON(ctx, http.MethodGet, c.filesPath+"?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ListResources(ctx context.Context, input *ListResourcesInput) (*ListResourcesOutput, error) {
	if input == nil || strings.TrimSpace(input.Kind) == "" {
		return nil, errors.New("resource kind is required")
	}
	q := url.Values{}
	q.Set("kind", input.Kind)
	var out ListResourcesOutput
	path := c.resourcesPath + "?" + q.Encode()
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) GetResource(ctx context.Context, input *ResourceRef) (*GetResourceOutput, error) {
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return nil, errors.New("resource kind and name are required")
	}
	var out GetResourceOutput
	path := strings.TrimRight(c.resourcesPath, "/") + "/" +
		url.PathEscape(input.Kind) + "/" + url.PathEscape(input.Name)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) SaveResource(ctx context.Context, input *SaveResourceInput) error {
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return errors.New("resource kind and name are required")
	}
	path := strings.TrimRight(c.resourcesPath, "/") + "/" +
		url.PathEscape(input.Kind) + "/" + url.PathEscape(input.Name)
	return c.doJSON(ctx, http.MethodPut, path, input, nil)
}

func (c *HTTPClient) DeleteResource(ctx context.Context, input *ResourceRef) error {
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return errors.New("resource kind and name are required")
	}
	path := strings.TrimRight(c.resourcesPath, "/") + "/" +
		url.PathEscape(input.Kind) + "/" + url.PathEscape(input.Name)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *HTTPClient) ExportResources(ctx context.Context, input *ExportResourcesInput) (*ExportResourcesOutput, error) {
	var out ExportResourcesOutput
	path := strings.TrimRight(c.resourcesPath, "/") + "/export"
	if err := c.doJSON(ctx, http.MethodPost, path, input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ImportResources(ctx context.Context, input *ImportResourcesInput) (*ImportResourcesOutput, error) {
	if input == nil {
		return nil, errors.New("input is required")
	}
	var out ImportResourcesOutput
	path := strings.TrimRight(c.resourcesPath, "/") + "/import"
	if err := c.doJSON(ctx, http.MethodPost, path, input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) GetTranscript(ctx context.Context, input *GetTranscriptInput, options ...TranscriptOption) (*ConversationState, error) {
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	optState := &transcriptOptions{}
	for _, option := range options {
		if option != nil {
			option(optState)
		}
	}
	q := url.Values{}
	if input.Since != "" {
		q.Set("since", input.Since)
	}
	if input.IncludeModelCalls {
		q.Set("includeModelCalls", "true")
	}
	if input.IncludeToolCalls {
		q.Set("includeToolCalls", "true")
	}
	if optState.includeFeeds {
		q.Set("includeFeeds", "true")
	}
	if len(optState.selectors) > 0 {
		data, err := json.Marshal(optState.selectors)
		if err != nil {
			return nil, err
		}
		q.Set("selectors", string(data))
	}
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(input.ConversationID) + "/transcript"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out ConversationState
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) TerminateConversation(ctx context.Context, conversationID string) error {
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(conversationID) + "/terminate"
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

func (c *HTTPClient) CompactConversation(ctx context.Context, conversationID string) error {
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(conversationID) + "/compact"
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

func (c *HTTPClient) PruneConversation(ctx context.Context, conversationID string) error {
	path := strings.TrimRight(c.conversationsPath, "/") + "/" + url.PathEscape(conversationID) + "/prune"
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

func (c *HTTPClient) GetA2AAgentCard(ctx context.Context, agentID string) (*a2a.AgentCard, error) {
	var out a2a.AgentCard
	path := "/v1/api/a2a/agents/" + url.PathEscape(agentID) + "/card"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) SendA2AMessage(ctx context.Context, agentID string, req *a2a.SendMessageRequest) (*a2a.SendMessageResponse, error) {
	var out a2a.SendMessageResponse
	path := "/v1/api/a2a/agents/" + url.PathEscape(agentID) + "/message"
	if err := c.doJSON(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ListA2AAgents(ctx context.Context, agentIDs []string) ([]string, error) {
	path := "/v1/api/a2a/agents?ids=" + url.QueryEscape(strings.Join(agentIDs, ","))
	var out struct {
		Agents []string `json:"agents"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Agents, nil
}

func (c *HTTPClient) GetSchedule(ctx context.Context, id string) (*scheduler.Schedule, error) {
	path := strings.TrimRight(c.schedulerPath, "/") + "/schedule/" + url.PathEscape(strings.TrimSpace(id))
	var out struct {
		Status string              `json:"status"`
		Data   *scheduler.Schedule `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *HTTPClient) ListSchedules(ctx context.Context) ([]*scheduler.Schedule, error) {
	path := strings.TrimRight(c.schedulerPath, "/") + "/"
	var out struct {
		Status string `json:"status"`
		Data   struct {
			Schedules []*scheduler.Schedule `json:"schedules"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Data.Schedules, nil
}

func (c *HTTPClient) UpsertSchedules(ctx context.Context, schedules []*scheduler.Schedule) error {
	path := strings.TrimRight(c.schedulerPath, "/") + "/"
	body := struct {
		Schedules []*scheduler.Schedule `json:"schedules"`
	}{Schedules: schedules}
	return c.doJSON(ctx, http.MethodPatch, path, &body, nil)
}

func (c *HTTPClient) RunScheduleNow(ctx context.Context, id string) error {
	path := strings.TrimRight(c.schedulerPath, "/") + "/run-now/" + url.PathEscape(strings.TrimSpace(id))
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// sseSubscription implements streaming.Subscription over an HTTP SSE connection.
type sseSubscription struct {
	id     string
	ch     chan *streaming.Event
	done   chan struct{}
	once   sync.Once
	cancel context.CancelFunc
}

func newSSESubscription(ctx context.Context, body io.ReadCloser, input *StreamEventsInput) *sseSubscription {
	ctx, cancel := context.WithCancel(ctx)
	sub := &sseSubscription{
		id:     uuid.New().String(),
		ch:     make(chan *streaming.Event, 64),
		done:   make(chan struct{}),
		cancel: cancel,
	}
	go sub.readLoop(ctx, body, input)
	return sub
}

func (s *sseSubscription) ID() string                 { return s.id }
func (s *sseSubscription) C() <-chan *streaming.Event { return s.ch }
func (s *sseSubscription) Close() error {
	s.once.Do(func() {
		s.cancel()
		close(s.done)
	})
	return nil
}

func (s *sseSubscription) readLoop(ctx context.Context, body io.ReadCloser, input *StreamEventsInput) {
	defer body.Close()
	defer close(s.ch)
	scanner := bufio.NewScanner(body)
	buf := make([]byte, 0, 256*1024)
	scanner.Buffer(buf, 16*1024*1024)
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
			continue
		}
		if line == "" && len(dataLines) > 0 {
			raw := strings.Join(dataLines, "\n")
			dataLines = dataLines[:0]
			var ev streaming.Event
			if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &ev); err != nil {
				continue
			}
			if ev.CreatedAt.IsZero() {
				ev.CreatedAt = time.Now()
			}
			if input != nil && input.Filter != nil && !input.Filter(&ev) {
				continue
			}
			select {
			case s.ch <- &ev:
			case <-ctx.Done():
				return
			case <-s.done:
				return
			}
		}
	}
}

func (c *HTTPClient) resolveToken(ctx context.Context) (string, error) {
	if c.tokenProvider != nil {
		return c.tokenProvider(ctx)
	}
	return c.authToken, nil
}

func (c *HTTPClient) applyAuth(ctx context.Context, req *http.Request) error {
	token, err := c.resolveToken(ctx)
	if err != nil {
		return fmt.Errorf("auth token: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

func (c *HTTPClient) newRequest(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if err := c.applyAuth(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(contentType) != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

func (c *HTTPClient) doJSON(ctx context.Context, method, path string, in interface{}, out interface{}) error {
	var body io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	contentType := ""
	if in != nil {
		contentType = "application/json"
	}
	req, err := c.newRequest(ctx, method, path, body, contentType)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("request failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
