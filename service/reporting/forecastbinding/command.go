package forecastbinding

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"sort"

	documents "github.com/viant/agently-core/app/store/reportingevidence"
	"github.com/viant/agently-core/protocol/mcpname"
	workspaceproto "github.com/viant/agently-core/protocol/ui/workspace"
	"github.com/viant/agently-core/runtime/evidence"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

// CommandReceipt is immutable server authority for one exact UI command. Its
// reference is an address, not a bearer credential: every load checks ownership.
type CommandReceipt struct {
	Version int `json:"version"`
	Scope
	RequestID   string                `json:"requestId"`
	OpID        string                `json:"opId"`
	MessageID   string                `json:"messageId"`
	RequestHash string                `json:"requestHash"`
	WindowID    string                `json:"windowId"`
	BuilderRef  string                `json:"builderRef"`
	Workspace   workspaceproto.Object `json:"workspace"`
	PlanIDs     []string              `json:"planIds"`
}
type commandReference struct {
	ConversationID string `json:"conversationId"`
	Version        int    `json:"version"`
	TurnID         string `json:"turnId"`
	RequestID      string `json:"requestId"`
}
type commandLink struct {
	Version   int    `json:"version"`
	Ref       string `json:"ref"`
	RequestID string `json:"requestId"`
}

func commandRequestID(scope Scope, op string) string {
	body, _ := json.Marshal(struct {
		Scope
		OpID string
	}{scope, op})
	hash, _ := RequestHash(body)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("agently.forecast.command.v1:"+hash)).String()
}
func commandRef(receipt *CommandReceipt) string {
	raw, _ := json.Marshal(commandReference{ConversationID: receipt.ConversationID, Version: 1, TurnID: receipt.TurnID, RequestID: receipt.RequestID})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func parseCommandRef(ref string) (commandReference, error) {
	var result commandReference
	if len(ref) == 0 || len(ref) > 4096 {
		return result, reject("invalid command reference")
	}
	raw, err := base64.RawURLEncoding.DecodeString(ref)
	if err != nil {
		return result, reject("invalid command reference")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil || result.Version != 1 || result.ConversationID == "" || result.TurnID == "" || result.RequestID == "" {
		return result, reject("invalid command reference")
	}
	canonical, _ := json.Marshal(result)
	if !bytes.Equal(raw, canonical) {
		return result, reject("noncanonical command reference")
	}
	return result, nil
}

func (s *NativeSourceStore) canonicalCommandWorkspace(ctx context.Context, scope Scope, target evidence.ReportCommandTarget) (*workspaceproto.Object, error) {
	var hint workspaceproto.Object
	if err := json.Unmarshal(target.Workspace, &hint); err != nil {
		return nil, reject("workspace origin missing")
	}
	if hint.ConversationID != scope.ConversationID || hint.Content.WindowID != target.WindowID || hint.ObjectID == "" || hint.Revision < 1 || hint.LastActivatedBy.TurnID == "" || hint.LastActivatedBy.ToolCallID == "" {
		return nil, reject("workspace origin scope mismatch")
	}
	activatedScope := scope
	activatedScope.TurnID = hint.LastActivatedBy.TurnID
	activated, err := s.loadNativeCall(ctx, activatedScope, "", hint.LastActivatedBy.ToolCallID, true)
	if err != nil {
		return nil, err
	}
	if mcpname.Display(activated.Tool) != "ui/view/open" {
		return nil, reject("workspace activation is not an owned view open")
	}
	var response struct {
		Workspace *workspaceproto.Object `json:"workspaceObject"`
	}
	if err = json.Unmarshal(activated.Response, &response); err != nil || response.Workspace == nil {
		return nil, reject("canonical workspace descriptor unavailable")
	}
	canonical := response.Workspace
	if canonical.ConversationID != scope.ConversationID || canonical.ObjectID != hint.ObjectID || canonical.Revision != hint.Revision || canonical.Content.WindowID != target.WindowID || canonical.LastActivatedBy.TurnID != activated.TurnID || canonical.LastActivatedBy.ToolCallID != activated.MessageID {
		return nil, reject("workspace activation identity mismatch")
	}
	creatorScope := scope
	creatorScope.TurnID = canonical.Origin.TurnID
	creator, err := s.loadNativeCall(ctx, creatorScope, "", canonical.Origin.ToolCallID, true)
	if err != nil {
		return nil, err
	}
	if mcpname.Display(creator.Tool) != "ui/view/open" {
		return nil, reject("workspace creator is not an owned view open")
	}
	var original struct {
		Workspace *workspaceproto.Object `json:"workspaceObject"`
	}
	if err = json.Unmarshal(creator.Response, &original); err != nil || original.Workspace == nil || original.Workspace.ObjectID != canonical.ObjectID || original.Workspace.Origin.TurnID != creator.TurnID || original.Workspace.Origin.ToolCallID != creator.MessageID {
		return nil, reject("workspace immutable origin mismatch")
	}
	return canonical, nil
}

func (s *NativeSourceStore) saveCommand(ctx context.Context, receipt *CommandReceipt, lease string) error {
	doc, err := s.documentScope(ctx, receipt.Scope)
	if err != nil {
		return err
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	return s.documents.Save(ctx, doc, documents.Command, receipt.RequestID, lease, body)
}
func (s *NativeSourceStore) loadCommand(ctx context.Context, scope Scope, requestID string) (*CommandReceipt, error) {
	doc, err := s.documentScope(ctx, scope)
	if err != nil {
		return nil, err
	}
	body, err := s.documents.Load(ctx, doc, documents.Command, requestID)
	if err != nil {
		return nil, err
	}
	var receipt CommandReceipt
	if err = json.Unmarshal(body, &receipt); err != nil {
		return nil, err
	}
	if receipt.Version != 1 || receipt.Scope != scope || receipt.RequestID != requestID || receipt.RequestID != commandRequestID(scope, receipt.OpID) || len(receipt.PlanIDs) == 0 {
		return nil, reject("command receipt identity mismatch")
	}
	return &receipt, nil
}

func (c *Controller) IssueReportCommand(ctx context.Context, target evidence.ReportCommandTarget) (*evidence.ReportCommand, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	if !c.active.Load() {
		return nil, nil
	}
	store, ok := c.runtime.store.(*NativeSourceStore)
	if !ok {
		return nil, reject("native command receipt store unavailable")
	}
	toolMessageID := requestctx.ToolMessageIDFromContext(ctx)
	call, err := store.loadNativeCall(ctx, c.scope, "", toolMessageID, false)
	if err != nil {
		return nil, err
	}
	if call.Status != "running" || mcpname.Display(call.Tool) != "ui/report/run" {
		return nil, reject("command requires the running native report operation")
	}
	workspace, err := store.canonicalCommandWorkspace(ctx, c.scope, target)
	if err != nil {
		return nil, err
	}
	originalRequest, err := object(call.Request)
	if err != nil {
		return nil, err
	}
	if requested, ok := originalRequest["windowId"].(string); ok && requested != "" && requested != target.WindowID {
		return nil, reject("command target differs from native request")
	}
	if requested, ok := originalRequest["windowKey"].(string); ok && requested != "" && requested != workspace.Content.WindowKey {
		return nil, reject("command window key differs from native request")
	}
	builder, _ := workspace.Content.Parameters["reportBuilderRef"].(string)
	if builder == "" {
		return nil, reject("canonical workspace builder identity missing")
	}
	hash, err := RequestHash(call.Request)
	if err != nil {
		return nil, err
	}
	plans := c.admittedPlanIDs()
	if len(plans) == 0 {
		return nil, reject("command has no admitted forecast plan")
	}
	receipt := &CommandReceipt{Version: 1, Scope: c.scope, RequestID: commandRequestID(c.scope, call.OpID), OpID: call.OpID, MessageID: call.MessageID, RequestHash: hash, WindowID: target.WindowID, BuilderRef: builder, Workspace: *workspace, PlanIDs: plans}
	if err = store.saveCommand(ctx, receipt, c.lease()); err != nil {
		return nil, err
	}
	confirmed, err := store.loadCommand(ctx, c.scope, receipt.RequestID)
	if err != nil {
		return nil, err
	}
	a, _ := json.Marshal(receipt)
	b, _ := json.Marshal(confirmed)
	if !bytes.Equal(a, b) {
		return nil, reject("command receipt save unconfirmed")
	}
	return &evidence.ReportCommand{RequestID: receipt.RequestID, AdmissionRef: commandRef(receipt)}, nil
}

func (c *Controller) admittedPlanIDs() []string {
	c.planMu.Lock()
	defer c.planMu.Unlock()
	ids := make([]string, 0, len(c.planIDs))
	for id := range c.planIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func (c *Controller) rememberPlan(body json.RawMessage) error {
	var response struct {
		Plan *PlanReceipt `json:"_agentlyForecastPlan"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Plan == nil || response.Plan.PlanID == "" {
		return reject("durable plan receipt missing")
	}
	c.planMu.Lock()
	defer c.planMu.Unlock()
	if c.planIDs == nil {
		c.planIDs = map[string]bool{}
	}
	c.planIDs[response.Plan.PlanID] = true
	c.active.Store(true)
	return nil
}
