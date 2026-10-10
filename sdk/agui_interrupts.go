package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	exportrequestmodel "github.com/viant/agently-core/model/exportrequest"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	toolcallmodel "github.com/viant/agently-core/model/toolcall"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/tool/resolver"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
	"github.com/viant/agently-core/service/browsermcp"
	"github.com/viant/agently-core/service/elicitation"
	toolapproval "github.com/viant/agently-core/service/shared/toolapproval"
)

// preflightAGUIResume performs no mutations. The broker invokes it before
// admission can consume the prior pending run.
func preflightAGUIResume(ctx context.Context, client Client, input *agui.RunAgentInput, pending aguiPending) error {
	if input == nil {
		return fmt.Errorf("resume input is required")
	}
	if len(input.Resume) > 0 {
		// Validate exact1.0 resume shapes before pointer decoding can collapse
		// explicit payload:null into an omitted optional field.
		envelope := map[string]any{"threadId": input.ThreadID, "runId": "preflight", "messages": []any{}, "resume": json.RawMessage(input.Resume)}
		if err := agui.ValidateInput(rawAGUI(envelope)); err != nil {
			return err
		}
	}
	expectedCalls := map[string]bool{}
	for _, call := range pending.ClientTools {
		expectedCalls[aguiPendingCallID(call)] = true
	}
	seenTools := map[string]bool{}
	var canonicalTools []agui.Message
	if runtime, ok := client.(aguiRuntime); ok {
		principal := authctx.EffectiveUserID(ctx)
		if principal != "" {
			thread, err := runtime.aguiStore().GetThread(ctx, principal, input.ThreadID)
			if err != nil {
				return err
			}
			if len(thread.Messages) > 0 {
				if err = json.Unmarshal(thread.Messages, &canonicalTools); err != nil {
					return err
				}
			}
		}
	}
	for _, message := range input.Messages {
		if message.Role != "tool" {
			continue
		}
		if seenTools[message.ToolCallID] {
			return fmt.Errorf("duplicate tool result for %q", message.ToolCallID)
		}
		seenTools[message.ToolCallID] = true
		if expectedCalls[message.ToolCallID] {
			for _, call := range pending.ClientTools {
				if aguiPendingCallID(call) == message.ToolCallID {
					if err := browsermcp.VerifyResultMetadata(call.Name, call.Metadata, message.Metadata); err != nil {
						return err
					}
				}
			}
			continue
		}
		historical := false
		for _, saved := range canonicalTools {
			if saved.Role == "tool" && saved.ID == message.ID && saved.ToolCallID == message.ToolCallID {
				before, _ := canonicalJSONValue(saved.Content)
				after, _ := canonicalJSONValue(message.Content)
				if bytes.Equal(before, after) && valueOrEmpty(saved.Error) == valueOrEmpty(message.Error) {
					historical = true
					break
				}
			}
		}
		if !historical {
			return fmt.Errorf("unknown tool result %q", message.ToolCallID)
		}
	}
	var answers []agui.WireResumeEntry
	if len(input.Resume) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(input.Resume))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&answers); err != nil {
			return fmt.Errorf("invalid resume entries: %w", err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return fmt.Errorf("resume must contain one JSON value")
		}
	}
	byID := make(map[string]agui.WireInterrupt, len(pending.Interrupts))
	for _, interrupt := range pending.Interrupts {
		if interrupt.ID == "" {
			return fmt.Errorf("pending interrupt has no ID")
		}
		if _, exists := byID[interrupt.ID]; exists {
			return fmt.Errorf("duplicate pending interrupt %q", interrupt.ID)
		}
		byID[interrupt.ID] = interrupt
	}
	seen := make(map[string]bool, len(answers))
	for _, answer := range answers {
		if seen[answer.InterruptId] {
			return fmt.Errorf("duplicate answer for interrupt %q", answer.InterruptId)
		}
		seen[answer.InterruptId] = true
		interrupt, exists := byID[answer.InterruptId]
		if !exists {
			return fmt.Errorf("unknown interrupt %q", answer.InterruptId)
		}
		if answer.Status != "resolved" && answer.Status != "cancelled" {
			return fmt.Errorf("invalid resume status for %q", answer.InterruptId)
		}
		if interrupt.ExpiresAt != nil {
			expiry, err := time.Parse(time.RFC3339Nano, *interrupt.ExpiresAt)
			if err != nil {
				return fmt.Errorf("interrupt %q has invalid expiry", interrupt.ID)
			}
			if !expiry.After(time.Now().UTC()) && answer.Status != "cancelled" {
				verified := false
				if interrupt.Reason == "approval" {
					if checker, ok := client.(interface {
						aguiVerifiedApprovalReceipt(context.Context, string, agui.WireInterrupt, agui.WireResumeEntry) (bool, error)
					}); ok {
						var err error
						verified, err = checker.aguiVerifiedApprovalReceipt(ctx, input.ThreadID, interrupt, answer)
						if err != nil {
							return err
						}
					}
				}
				if !verified {
					return fmt.Errorf("interrupt %q expired; cancel it to continue", interrupt.ID)
				}
			}
		}
		if answer.Payload != nil && bytes.Equal(bytes.TrimSpace(*answer.Payload), []byte("null")) {
			return fmt.Errorf("resume payload cannot be null")
		}
		if answer.Status == "resolved" && interrupt.ResponseSchema != nil {
			var payload any
			if answer.Payload != nil {
				if err := decodeAGUIValue(*answer.Payload, &payload); err != nil {
					return fmt.Errorf("invalid resume payload: %w", err)
				}
			}
			if err := aguiValidateResponseSchema(*interrupt.ResponseSchema, payload); err != nil {
				return fmt.Errorf("interrupt %q answer fails responseSchema: %w", interrupt.ID, err)
			}
		}
		if interrupt.Reason == "agently.client_tool" {
			found := false
			for _, call := range pending.ClientTools {
				if aguiPendingCallID(call) == answer.InterruptId {
					if _, _, err := aguiClientToolAnswer(call, answer); err != nil {
						return err
					}
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("unknown client tool interrupt %q", answer.InterruptId)
			}
		}
		if checker, ok := client.(aguiInterruptPreflight); ok {
			if err := checker.aguiPreflightInterrupt(ctx, input.ThreadID, interrupt, answer); err != nil {
				return err
			}
		}
	}
	for id := range byID {
		if !seen[id] {
			return fmt.Errorf("missing answer for interrupt %q", id)
		}
	}
	return nil
}

type aguiInterruptPreflight interface {
	aguiPreflightInterrupt(context.Context, string, agui.WireInterrupt, agui.WireResumeEntry) error
}

// Response schemas are protocol-carried data. Their local fragments may refer
// to bundled $defs; remote/file resolution is not a capability of resume input.
type aguiResponseSchemaLoader struct{}

func (aguiResponseSchemaLoader) Load(uri string) (any, error) {
	return nil, fmt.Errorf("external responseSchema reference is unsupported: %s", uri)
}
func aguiValidateResponseSchema(schema map[string]json.RawMessage, payload any) error {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(aguiResponseSchemaLoader{})
	var document any
	if err := decodeAGUIValue(rawAGUI(schema), &document); err != nil {
		return err
	}
	const resource = "https://agui.local/interrupt-answer.json"
	if err := compiler.AddResource(resource, document); err != nil {
		return err
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return err
	}
	return compiled.Validate(payload)
}

// Approval metadata retains the existing authorized policy/editor description;
// no tool definition or new execution permission is supplied by this envelope.
func aguiApprovalInterrupt(approval *PendingToolApproval) (agui.WireInterrupt, error) {
	if approval == nil || approval.ID == "" {
		return agui.WireInterrupt{}, fmt.Errorf("approval identity is required")
	}
	meta := approval.Metadata
	var opID string
	if value, ok := meta["opId"].(string); ok {
		opID = value
	}
	if opID == "" {
		return agui.WireInterrupt{}, fmt.Errorf("approval %q has no original tool call identity", approval.ID)
	}
	message := approval.Title
	if message == "" {
		message = "Approve execution of " + approval.ToolName
	}
	response := map[string]json.RawMessage{
		"type":       json.RawMessage(`"object"`),
		"properties": json.RawMessage(`{"action":{"type":"string","enum":["approve","reject","cancel"]},"editedArgs":{"type":"object"},"editedFields":{"type":"object"},"payload":{"type":"object"},"reason":{"type":"string"},"note":{"type":"string"}}`),
		"required":   json.RawMessage(`["action"]`), "additionalProperties": json.RawMessage(`false`),
	}
	metadata := agui.WireMetadata{"agently": rawAGUI(map[string]any{"version": "1", "kind": "tool-approval", "queueId": approval.ID, "threadId": approval.ConversationID, "turnId": approval.TurnID, "messageId": approval.MessageID, "toolName": approval.ToolName, "arguments": approval.Arguments, "policy": meta})}
	interrupt := agui.WireInterrupt{ID: approval.ID, Reason: "approval", Message: &message, ToolCallID: &opID, ResponseSchema: &response, Metadata: &metadata}
	if approval.ExpiresAt != nil {
		expiry := approval.ExpiresAt.UTC().Format(time.RFC3339Nano)
		interrupt.ExpiresAt = &expiry
	}
	return interrupt, nil
}

type aguiApprovalAnswer struct {
	Action       string                 `json:"action"`
	EditedArgs   map[string]interface{} `json:"editedArgs,omitempty"`
	EditedFields map[string]interface{} `json:"editedFields,omitempty"`
	Payload      map[string]interface{} `json:"payload,omitempty"`
	Reason       string                 `json:"reason,omitempty"`
	Note         string                 `json:"note,omitempty"`
}

func decodeAGUIApprovalAnswer(answer agui.WireResumeEntry) (*aguiApprovalAnswer, error) {
	if answer.Status == "cancelled" && answer.Payload == nil {
		return &aguiApprovalAnswer{Action: "cancel"}, nil
	}
	if answer.Payload == nil {
		return nil, fmt.Errorf("approval answer requires payload")
	}
	var payload aguiApprovalAnswer
	decoder := json.NewDecoder(bytes.NewReader(*answer.Payload))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	payload.Action = strings.ToLower(strings.TrimSpace(payload.Action))
	if answer.Status == "cancelled" && payload.Action != "cancel" {
		return nil, fmt.Errorf("cancelled approval cannot authorize a tool")
	}
	if payload.Action != "approve" && payload.Action != "reject" && payload.Action != "cancel" {
		return nil, fmt.Errorf("approval action must be approve, reject or cancel")
	}
	if payload.Action != "approve" && (len(payload.EditedArgs) > 0 || len(payload.EditedFields) > 0) {
		return nil, fmt.Errorf("only approval may edit tool arguments")
	}
	return &payload, nil
}

// The coordinator must observe a woken live execution, rather than start or
// resume another native execution for the same blocking MCP elicitation.
type aguiInterruptDisposition string

const (
	aguiInterruptDeferred     aguiInterruptDisposition = "deferred"
	aguiInterruptWokeExisting aguiInterruptDisposition = "woke_existing_execution"
)

func (c *backendClient) aguiApplyInterrupt(ctx context.Context, record *aguistore.Run, interrupt agui.WireInterrupt, answer agui.WireResumeEntry) (aguiInterruptDisposition, error) {
	if record == nil || record.ConversationID == "" || interrupt.ID != answer.InterruptId {
		return "", fmt.Errorf("interrupt resolution identity mismatch")
	}
	if interrupt.Reason == "approval" {
		return c.aguiApplyApproval(ctx, record, interrupt, answer)
	}
	if interrupt.Reason != "elicitation" {
		return "", fmt.Errorf("unsupported human interrupt reason %q", interrupt.Reason)
	}
	action := "accept"
	if answer.Status == "cancelled" {
		action = "cancel"
	}
	var payload map[string]interface{}
	if answer.Payload != nil {
		if err := decodeAGUIValue(*answer.Payload, &payload); err != nil {
			return "", fmt.Errorf("native elicitation answer requires object payload")
		}
	}
	var resolution *elicitation.CheckedResolution
	var err error
	if c.elicSvc != nil {
		resolution, err = c.elicSvc.ResolveChecked(ctx, record.ConversationID, interrupt.ID, action, payload, "")
	} else if c.agent != nil {
		resolution, err = c.agent.ResolveElicitationChecked(ctx, record.ConversationID, interrupt.ID, action, payload)
	} else {
		return "", fmt.Errorf("checked elicitation service unavailable")
	}
	if err != nil {
		return "", err
	}
	if resolution.Disposition == elicitation.ResolutionWokeExisting {
		return aguiInterruptWokeExisting, nil
	}
	return aguiInterruptDeferred, nil
}
func (c *backendClient) aguiPreflightInterrupt(ctx context.Context, threadID string, interrupt agui.WireInterrupt, answer agui.WireResumeEntry) error {
	if interrupt.Reason == "approval" {
		return c.aguiPreflightApproval(ctx, threadID, interrupt, answer)
	}
	if interrupt.Reason == "agently.client_tool" {
		return nil
	} // covered by pending-call preflight below.
	if interrupt.Reason != "elicitation" {
		return fmt.Errorf("unsupported human interrupt reason %q", interrupt.Reason)
	}
	action := "accept"
	if answer.Status == "cancelled" {
		action = "cancel"
	}
	var payload map[string]interface{}
	if answer.Payload != nil {
		if err := decodeAGUIValue(*answer.Payload, &payload); err != nil {
			return fmt.Errorf("native elicitation answer requires object payload")
		}
	}
	if c.elicSvc != nil {
		_, err := c.elicSvc.InspectResolution(ctx, threadID, interrupt.ID, action, payload, "")
		return err
	}
	if c.agent != nil {
		_, err := c.agent.InspectElicitationResolution(ctx, threadID, interrupt.ID, action, payload)
		return err
	}
	return fmt.Errorf("checked elicitation service unavailable")
}

func (c *backendClient) aguiApprovalRow(ctx context.Context, threadID string, interrupt agui.WireInterrupt) (*toolapprovalqueuemodel.QueueRowView, error) {
	principal := authctx.EffectiveUserID(ctx)
	lister, ok := c.conv.(toolApprovalQueueLister)
	if !ok || principal == "" {
		return nil, fmt.Errorf("authenticated approval store unavailable")
	}
	rows, err := lister.ListToolApprovalQueues(ctx, &toolapprovalqueuemodel.QueueRowsInput{Id: interrupt.ID, UserId: principal, ConversationId: threadID, Has: &toolapprovalqueuemodel.QueueRowsInputHas{Id: true, UserId: true, ConversationId: true}})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 || rows[0] == nil || rows[0].UserId != principal || valueOrEmpty(rows[0].ConversationId) != threadID {
		return nil, fmt.Errorf("approval scope mismatch")
	}
	row := rows[0]
	meta := parseToolApprovalMetadata(row.Metadata)
	if interrupt.ToolCallID == nil || (*interrupt.ToolCallID != meta.OpID && *interrupt.ToolCallID != agui.ProtocolToolCallID(valueOrEmpty(row.TurnId), meta.OpID)) {
		return nil, fmt.Errorf("approval tool identity mismatch")
	}
	return row, nil
}
func aguiApprovalMetadata(row *toolapprovalqueuemodel.QueueRowView) map[string]json.RawMessage {
	values := map[string]json.RawMessage{}
	if row.Metadata != nil {
		_ = json.Unmarshal(*row.Metadata, &values)
	}
	return values
}
func aguiApprovalArgs(row *toolapprovalqueuemodel.QueueRowView, answer *aguiApprovalAnswer) (map[string]interface{}, error) {
	var args map[string]interface{}
	if err := decodeAGUIValue(row.Arguments, &args); err != nil {
		return nil, err
	}
	meta := parseToolApprovalMetadata(row.Metadata)
	editors := approvalEditorsFromMeta(meta)
	fields := map[string]interface{}{}
	for k, v := range answer.EditedFields {
		fields[k] = v
	}
	allowed := map[string]bool{}
	for _, editor := range editors {
		if editor == nil {
			continue
		}
		allowed[editor.Name] = true
		if answer.EditedArgs != nil {
			fields[editor.Name] = resolver.Select(editor.Path, answer.EditedArgs, answer.EditedArgs)
		}
	}
	for key := range fields {
		if !allowed[key] {
			return nil, fmt.Errorf("unknown approval editor %q", key)
		}
	}
	if err := toolapproval.ApplyEdits(args, editors, fields); err != nil {
		return nil, err
	}
	if answer.EditedArgs != nil && !bytes.Equal(rawAGUI(args), rawAGUI(answer.EditedArgs)) {
		return nil, fmt.Errorf("editedArgs changes arguments outside authorized editors")
	}
	payload := answer.Payload
	if len(payload) == 0 {
		payload = fields
	}
	if meta.Review != nil && len(payload) > 0 {
		if err := toolapproval.ApplyReview(args, meta.Review, payload); err != nil {
			return nil, err
		}
	}
	if patch := decisionPatchFromMeta(meta, answer.Action); len(patch) > 0 {
		if err := toolapproval.ApplyDecisionPatch(args, patch); err != nil {
			return nil, err
		}
	}
	return args, nil
}
func (c *backendClient) aguiPreflightApproval(ctx context.Context, threadID string, interrupt agui.WireInterrupt, entry agui.WireResumeEntry) error {
	row, err := c.aguiApprovalRow(ctx, threadID, interrupt)
	if err != nil {
		return err
	}
	if c.data == nil {
		return fmt.Errorf("approval native graph unavailable")
	}
	meta := parseToolApprovalMetadata(row.Metadata)
	calls, err := c.data.GetToolCallByOp(ctx, meta.OpID, &toolcallmodel.ToolCallByOpInput{ConversationId: threadID, OpId: meta.OpID, Has: &toolcallmodel.ToolCallByOpInputHas{ConversationId: true, OpId: true}}, principalDataOpts(ctx)...)
	if err != nil {
		return err
	}
	if len(calls) != 1 || valueOrEmpty(calls[0].TurnId) != valueOrEmpty(row.TurnId) {
		return fmt.Errorf("original approval tool call unavailable")
	}
	message, err := c.conv.GetMessage(ctx, calls[0].MessageId, conversation.WithIncludeToolCall(true))
	if err != nil {
		return err
	}
	if message == nil || message.ConversationId != threadID || valueOrEmpty(message.TurnId) != valueOrEmpty(row.TurnId) || valueOrEmpty(message.ParentMessageId) != valueOrEmpty(row.MessageId) || message.MessageToolCall == nil || message.MessageToolCall.OpId != meta.OpID {
		return fmt.Errorf("original approval message identity mismatch")
	}
	if receipt := aguiApprovalMetadata(row)["aguiDecision"]; len(receipt) > 0 {
		canonical, err := canonicalJSONValue(rawAGUI(entry))
		if err != nil {
			return err
		}
		if !bytes.Equal(receipt, canonical) {
			return fmt.Errorf("approval already has a different answer")
		}
		var outcome struct {
			Status string  `json:"status"`
			Result *string `json:"result"`
		}
		if json.Unmarshal(aguiApprovalMetadata(row)["aguiOutcome"], &outcome) != nil || outcome.Result == nil || (outcome.Status != "completed" && outcome.Status != "failed") {
			return fmt.Errorf("approval effect outcome is uncertain; refusing duplicate execution")
		}
		if message.Content == nil || *message.Content != *outcome.Result || message.Status == nil || *message.Status != outcome.Status || message.MessageToolCall.Status != outcome.Status || message.MessageToolCall.CompletedAt == nil {
			return fmt.Errorf("approval receipt does not match the completed original effect")
		}
		if row.Status == "pending" || row.Status == "approved" {
			return fmt.Errorf("approval receipt lacks terminal queue outcome")
		}
		return nil
	}
	answer, err := decodeAGUIApprovalAnswer(entry)
	if err != nil {
		return err
	}
	if row.Status != "pending" {
		return fmt.Errorf("approval is no longer pending")
	}
	if row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) && !aguiTrustedApprovalTimeout(ctx, row, entry) {
		return fmt.Errorf("approval expired")
	}
	if _, ok := c.conv.(interface {
		ClaimToolApprovalDecision(context.Context, *toolapprovalqueuemodel.QueueRowView, string, string) error
	}); !ok {
		return fmt.Errorf("atomic approval claim unavailable")
	}
	if _, err = aguiApprovalArgs(row, answer); err != nil {
		return err
	}
	if c.data == nil {
		return fmt.Errorf("approval native graph unavailable")
	}
	return nil
}
func (c *backendClient) aguiApplyApproval(ctx context.Context, record *aguistore.Run, interrupt agui.WireInterrupt, entry agui.WireResumeEntry) (aguiInterruptDisposition, error) {
	if err := c.aguiPreflightApproval(ctx, record.ConversationID, interrupt, entry); err != nil {
		return "", err
	}
	answer, _ := decodeAGUIApprovalAnswer(entry)
	row, err := c.aguiApprovalRow(ctx, record.ConversationID, interrupt)
	if err != nil {
		return "", err
	}
	if valueOrEmpty(row.TurnId) != record.TurnID {
		return "", fmt.Errorf("approval native turn mismatch")
	}
	metadata := aguiApprovalMetadata(row)
	if len(metadata["aguiDecision"]) > 0 {
		if len(metadata["aguiOutcome"]) > 0 {
			return aguiInterruptDeferred, nil
		}
		return "", fmt.Errorf("approval effect outcome is uncertain; refusing duplicate execution")
	}
	if aguiTrustedApprovalTimeout(ctx, row, entry) {
		answer.Action = "timeout"
	}
	args, err := aguiApprovalArgs(row, answer)
	if err != nil {
		return "", err
	}
	meta := parseToolApprovalMetadata(row.Metadata)
	calls, err := c.data.GetToolCallByOp(ctx, meta.OpID, &toolcallmodel.ToolCallByOpInput{ConversationId: record.ConversationID, OpId: meta.OpID, Has: &toolcallmodel.ToolCallByOpInputHas{ConversationId: true, OpId: true}}, principalDataOpts(ctx)...)
	if err != nil {
		return "", err
	}
	if len(calls) != 1 || valueOrEmpty(calls[0].TurnId) != record.TurnID {
		return "", fmt.Errorf("original approval tool call unavailable")
	}
	message, err := c.conv.GetMessage(ctx, calls[0].MessageId, conversation.WithIncludeToolCall(true))
	if err != nil {
		return "", err
	}
	if message == nil || message.ConversationId != record.ConversationID || valueOrEmpty(message.TurnId) != record.TurnID || valueOrEmpty(message.ParentMessageId) != valueOrEmpty(row.MessageId) || message.MessageToolCall == nil || message.MessageToolCall.OpId != meta.OpID {
		return "", fmt.Errorf("original approval message identity mismatch")
	}
	canonical, err := canonicalJSONValue(rawAGUI(entry))
	if err != nil {
		return "", err
	}
	metadata["aguiDecision"] = canonical
	blob := []byte(rawAGUI(metadata))
	row.Metadata = &blob
	claimer := c.conv.(interface {
		ClaimToolApprovalDecision(context.Context, *toolapprovalqueuemodel.QueueRowView, string, string) error
	})
	if err = claimer.ClaimToolApprovalDecision(ctx, row, authctx.EffectiveUserID(ctx), answer.Action); err != nil {
		return "", err
	}
	execCtx := runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{ConversationID: record.ConversationID, TurnID: record.TurnID, ParentMessageID: valueOrEmpty(row.MessageId)})
	execCtx = exportrequestmodel.WithID(execCtx, meta.OpID)
	result := "tool execution was not approved by user"
	if answer.Action == "timeout" {
		result = api.ApprovalTimeoutErrorMessage
	}
	var effectErr error
	if answer.Action == "approve" {
		result, effectErr = c.ExecuteTool(execCtx, row.ToolName, args)
	}
	// The claimed queue can never be reset to pending after an uncertain effect.
	status := "completed"
	if effectErr != nil {
		status = "failed"
		result = resolvedQueueToolResult(result, effectErr)
	}
	for _, part := range []struct {
		kind string
		body []byte
	}{{"tool_request", rawAGUI(args)}, {"tool_response", []byte(result)}} {
		payload := conversation.NewPayload()
		id := message.Id + "/agui-approval/" + part.kind
		payload.SetId(id)
		payload.SetKind(part.kind)
		payload.SetMimeType("application/json")
		if part.kind == "tool_response" {
			payload.SetMimeType("text/plain")
		}
		payload.SetStorage("inline")
		payload.SetCompression("none")
		payload.SetSizeBytes(len(part.body))
		payload.SetInlineBody(part.body)
		if err = c.conv.PatchPayload(ctx, payload); err != nil {
			return "", err
		}
		link := conversation.NewToolCall()
		link.SetMessageID(message.Id)
		link.SetOpID(meta.OpID)
		if part.kind == "tool_request" {
			link.RequestPayloadID = &id
			link.Has.RequestPayloadID = true
		} else {
			link.ResponsePayloadID = &id
			link.Has.ResponsePayloadID = true
		}
		if err = c.conv.PatchToolCall(ctx, link); err != nil {
			return "", err
		}
	}
	update := conversation.NewMessage()
	update.SetId(message.Id)
	update.SetContent(result)
	update.SetStatus(status)
	if err = c.conv.PatchMessage(ctx, update); err != nil {
		return "", err
	}
	call := conversation.NewToolCall()
	call.SetMessageID(message.Id)
	call.SetOpID(meta.OpID)
	call.SetTurnID(record.TurnID)
	call.SetToolName(row.ToolName)
	call.SetStatus(status)
	now := time.Now().UTC()
	call.CompletedAt = &now
	call.Has.CompletedAt = true
	if effectErr != nil {
		call.SetErrorMessage(effectErr.Error())
	}
	if err = c.conv.PatchToolCall(ctx, call); err != nil {
		return "", err
	}
	metadata["aguiOutcome"] = rawAGUI(map[string]any{"status": status, "result": result})
	blob = rawAGUI(metadata)
	done := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	done.SetId(row.Id)
	done.SetUserId(row.UserId)
	done.SetMetadata(blob)
	done.SetUpdatedAt(now)
	if answer.Action == "timeout" {
		done.SetErrorMessage(api.ApprovalTimeoutErrorMessage)
	}
	if answer.Action == "approve" {
		if effectErr == nil {
			done.SetStatus("executed")
			done.SetExecutedAt(now)
		} else {
			done.SetStatus("failed")
			done.SetErrorMessage(effectErr.Error())
		}
	}
	patcher, ok := c.conv.(toolApprovalQueuePatcher)
	if !ok {
		return "", fmt.Errorf("approval writer unavailable")
	}
	if err = patcher.PatchToolApprovalQueue(ctx, done); err != nil {
		return "", err
	}
	c.aguiNotifyApprovalUpdated(ctx, record.ConversationID)
	return aguiInterruptDeferred, nil
}

func canonicalJSONValue(raw json.RawMessage) ([]byte, error) {
	var value any
	if err := decodeAGUIValue(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// Read the pending queue without invoking the legacy listing timeout producer,
// which can synthesize user instructions or finish a different native turn.
func (c *backendClient) aguiPendingApprovalInterrupts(ctx context.Context, threadID, turnID string) ([]agui.WireInterrupt, error) {
	lister, ok := c.conv.(toolApprovalQueueLister)
	if !ok {
		return nil, nil
	}
	principal := authctx.EffectiveUserID(ctx)
	if principal == "" {
		return nil, fmt.Errorf("approval discovery requires authenticated scope")
	}
	query := &toolapprovalqueuemodel.QueueRowsInput{UserId: principal, ConversationId: threadID, QueueStatus: "pending", Has: &toolapprovalqueuemodel.QueueRowsInputHas{UserId: true, ConversationId: true, QueueStatus: true}}
	rows, err := lister.ListToolApprovalQueues(ctx, query)
	if err != nil {
		return nil, err
	}
	var output []agui.WireInterrupt
	for _, row := range rows {
		if row == nil || row.UserId != principal || valueOrEmpty(row.ConversationId) != threadID || valueOrEmpty(row.TurnId) != turnID {
			continue
		}
		approval := pendingToolApprovalFromRow(row)
		interrupt, err := aguiApprovalInterrupt(approval)
		if err != nil {
			return nil, err
		}
		output = append(output, interrupt)
	}
	return output, nil
}
