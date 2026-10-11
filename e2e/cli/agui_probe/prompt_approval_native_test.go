package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/protocol/tool"
	approvalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/elicitation"
	"github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/agently-core/service/shared/toolapproval"
	"github.com/viant/agently-core/service/shared/toolexec"
	"github.com/viant/mcp-protocol/schema"
)

const nativePromptToken = "owned-native-prompt-fixture-token"
const nativePromptOwner = "owned-native-prompt-fixture"
const nativePromptProbeID = "native-prompt-approval-fixture"
const nativePromptOperation = "native-prompt-operation"
const nativePromptConversation = "native-prompt-conversation"
const nativePromptTurn = "native-prompt-turn"
const nativePromptModelMessage = "native-prompt-model-message"

func nativePromptGET(t *testing.T, endpoint, bearer string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+bearer)
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, body
}

type nativePromptElicitor struct {
	elicitation          *elicitation.Service
	captureURL           string
	probeID              string
	captureCode          int
	captureBody          map[string]any
	elicitationMessageID string
}

func (e *nativePromptElicitor) ElicitToolApproval(ctx context.Context, turn *requestctx.TurnMeta, name string, cfg *llm.ApprovalConfig, args map[string]interface{}) (string, map[string]interface{}, error) {
	view := toolapproval.BuildView(name, args, cfg)
	requested := schema.ElicitRequestParamsRequestedSchema{Type: "object", Properties: map[string]interface{}{
		"names": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
	}}
	req := &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: view.Message, RequestedSchema: requested}}
	message, err := e.elicitation.Record(ctx, turn, "assistant", req)
	if err != nil {
		return "", nil, err
	}
	e.elicitationMessageID = message.Id

	captureURL := e.captureURL + "/prompt-approval-capture/" + url.PathEscape(e.probeID) + "?id=" + url.QueryEscape(req.ElicitationId)
	captureRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, captureURL, nil)
	if err != nil {
		return "", nil, err
	}
	captureRequest.Header.Set("Authorization", "Bearer "+nativePromptToken)
	captureResponse, err := http.DefaultClient.Do(captureRequest)
	if err != nil {
		return "", nil, err
	}
	var capture map[string]any
	decodeErr := json.NewDecoder(captureResponse.Body).Decode(&capture)
	_ = captureResponse.Body.Close()
	e.captureCode = captureResponse.StatusCode
	e.captureBody = capture
	if decodeErr != nil {
		e.captureBody = map[string]any{"decodeError": decodeErr.Error()}
	}

	payload := map[string]interface{}{"editedFields": map[string]interface{}{"names": []interface{}{"HOME", "PATH"}}}
	_, err = e.elicitation.ResolveChecked(ctx, turn.ConversationID, req.ElicitationId, "accept", payload, "")
	if err != nil {
		return "", nil, err
	}
	return "accept", payload, nil
}

type nativePromptRegistry struct {
	tool.Registry
	calls int
	names []interface{}
}

func (r *nativePromptRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) {
	return &llm.ToolDefinition{Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"names": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}}}}, true
}
func (r *nativePromptRegistry) Execute(_ context.Context, _ string, args map[string]interface{}) (string, error) {
	r.calls++
	r.names = append([]interface{}(nil), args["names"].([]interface{})...)
	return `{"values":{"HOME":"safe-fixture-home","PATH":"safe-fixture-path"}}`, nil
}

func TestPromptApprovalNativeReceiptUsesAcceptedUserMessagePayload(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: nativePromptOwner})
	root := t.TempDir()
	nativeServer, err := native.New(ctx, native.Options{WorkspaceRoot: filepath.Join(root, "native")})
	require.NoError(t, err)
	defer nativeServer.Shutdown(ctx)
	conv, err := convservice.New(ctx, nativeServer)
	require.NoError(t, err)

	conversation := apiconv.NewConversation()
	conversation.SetId(nativePromptConversation)
	conversation.SetCreatedByUserID(nativePromptOwner)
	require.NoError(t, conv.PatchConversations(ctx, conversation))
	turn := apiconv.NewTurn()
	turn.SetId(nativePromptTurn)
	turn.SetConversationID(nativePromptConversation)
	turn.SetStatus("running")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	model := apiconv.NewMessage()
	model.SetId(nativePromptModelMessage)
	model.SetConversationID(nativePromptConversation)
	model.SetTurnID(nativePromptTurn)
	model.SetRole("assistant")
	model.SetType("text")
	model.SetContent("Read the requested environment once")
	require.NoError(t, conv.PatchMessage(ctx, model))

	elicRouter := router.New()
	elicSvc := elicitation.New(conv, nil, elicRouter, nil)
	registry := &nativePromptRegistry{}
	agent := agentsvc.New(nil, nil, nil, registry, nil, conv)
	dataSvc := data.NewService(nativeServer)
	backend, err := sdk.NewEmbeddedFromRuntime(&executor.Runtime{Agent: agent, Native: nativeServer, Conversation: conv, Data: dataSvc, Elicitation: elicSvc, ElicitationRouter: elicRouter})
	require.NoError(t, err)
	sdkHandler, err := sdk.NewHandlerWithContext(ctx, backend)
	require.NoError(t, err)
	sdkServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+nativePromptToken {
			http.Error(w, "unauthorized fixture", http.StatusUnauthorized)
			return
		}
		authed := authctx.WithUserInfo(r.Context(), &authctx.UserInfo{Subject: nativePromptOwner})
		sdkHandler.ServeHTTP(w, r.WithContext(authed))
	}))
	defer sdkServer.Close()

	probe := &handler{runs: map[string]*observedRun{}}
	helperServer := httptest.NewServer(probe)
	defer helperServer.Close()
	record := &observedRun{ownerHash: digest(nativePromptToken), target: sdkServer.URL, identity: identity{ThreadID: "native-prompt-thread", RunID: "native-prompt-run", ConversationID: nativePromptConversation, TurnID: nativePromptTurn}}
	probe.runs[nativePromptProbeID] = record

	elicitor := &nativePromptElicitor{elicitation: elicSvc, captureURL: helperServer.URL, probeID: nativePromptProbeID}
	toolName := "system/os:getEnv"
	cfg := &llm.ApprovalConfig{Mode: llm.ApprovalModePrompt, UI: &llm.ApprovalUIBinding{Editable: []*llm.ApprovalEditableField{{Name: "names", Selector: "input.names", Kind: "checkbox_list"}}}}
	callCtx := requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: nativePromptConversation, TurnID: nativePromptTurn, ParentMessageID: nativePromptModelMessage})
	callCtx = requestctx.WithModelMessageID(callCtx, nativePromptModelMessage)
	callCtx = approvalqueue.WithState(callCtx)
	approvalqueue.MarkTool(callCtx, toolName, cfg)
	callCtx = toolapproval.WithElicitor(callCtx, elicitor)
	originalNames := []interface{}{"HOME", "SHELL", "PATH"}
	call, _, err := toolexec.ExecuteToolStep(callCtx, registry, toolexec.StepInfo{ID: nativePromptOperation, Name: toolName, Args: map[string]interface{}{"names": originalNames}}, conv)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, elicitor.captureCode, "capture endpoint must prove the real pending prompt/effect before acceptance; body=%v", elicitor.captureBody)
	require.NotEmpty(t, elicitor.elicitationMessageID)
	require.Equal(t, 1, registry.calls)
	require.Equal(t, []interface{}{"HOME", "PATH"}, registry.names, "only accepted names reach native execution")
	require.Equal(t, []interface{}{"HOME", "PATH"}, call.Arguments["names"])

	stored, err := conv.GetConversation(ctx, nativePromptConversation, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	var toolParent string
	for _, transcriptTurn := range stored.GetTranscript() {
		for _, message := range transcriptTurn.GetMessages() {
			if message.MessageToolCall != nil && message.MessageToolCall.OpId == nativePromptOperation {
				if message.ParentMessageId != nil {
					toolParent = *message.ParentMessageId
				}
			}
		}
	}
	require.Equal(t, elicitor.elicitationMessageID, toolParent, "the observed tool parent must be the persisted assistant elicitation message")
	if tuple, ok := elicitor.captureBody["tuple"].(map[string]any); ok {
		require.Equal(t, elicitor.elicitationMessageID, tuple["queueParentMessageId"], "helper capture must return the observed elicitation parent")
	}

	// toolexec closes the original tool step; turn completion and final assistant
	// content belong to the surrounding model runner, so this fixture persists them.
	finalMessage := apiconv.NewMessage()
	finalMessage.SetId("native-prompt-final-message")
	finalMessage.SetConversationID(nativePromptConversation)
	finalMessage.SetTurnID(nativePromptTurn)
	finalMessage.SetParentMessageID(nativePromptModelMessage)
	finalMessage.SetRole("assistant")
	finalMessage.SetType("text")
	finalMessage.SetStatus("completed")
	finalMessage.SetContent("Requested environment access completed.")
	require.NoError(t, conv.PatchMessage(ctx, finalMessage))
	completedTurn := apiconv.NewTurn()
	completedTurn.SetId(nativePromptTurn)
	completedTurn.SetStatus("completed")
	require.NoError(t, conv.PatchTurn(ctx, completedTurn))

	receiptURL := helperServer.URL + "/prompt-approval-receipt/" + url.PathEscape(nativePromptProbeID) + "?id=" + url.QueryEscape(elicitor.captureBody["tuple"].(map[string]any)["approvalId"].(string))
	receiptRequest, err := http.NewRequest(http.MethodGet, receiptURL, nil)
	require.NoError(t, err)
	receiptRequest.Header.Set("Authorization", "Bearer "+nativePromptToken)
	receiptResponse, err := http.DefaultClient.Do(receiptRequest)
	require.NoError(t, err)
	defer receiptResponse.Body.Close()
	var receipt map[string]any
	require.NoError(t, json.NewDecoder(receiptResponse.Body).Decode(&receipt))
	require.Equal(t, http.StatusOK, receiptResponse.StatusCode, "receipt must read actual accepted message payload; response=%v", receipt)
	require.Equal(t, "accepted", receipt["elicitationStatus"])
	require.Equal(t, map[string]any{"editedFields": map[string]any{"names": []any{"HOME", "PATH"}}}, receipt["acceptedResponse"])
	require.Equal(t, "completed", receipt["nativeStatus"])
	require.NotContains(t, fmt.Sprint(receipt), "safe-fixture-home")
	require.NotContains(t, fmt.Sprint(receipt), "safe-fixture-path")

	approvalID := elicitor.captureBody["tuple"].(map[string]any)["approvalId"].(string)
	acceptedMessageID := receipt["acceptedResponseMessageId"].(string)
	acceptedPayloadID := receipt["acceptedResponsePayloadId"].(string)
	require.NotEmpty(t, acceptedMessageID)
	require.NotEmpty(t, acceptedPayloadID)
	stored, err = conv.GetConversation(ctx, nativePromptConversation, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	var acceptedStored *apiconv.Message
	for _, transcriptTurn := range stored.GetTranscript() {
		for _, message := range transcriptTurn.GetMessages() {
			if message.Id == acceptedMessageID {
				acceptedStored = message
			}
		}
	}
	require.NotNil(t, acceptedStored, "read the exact accepted user response back from the native store")
	require.Equal(t, "user", acceptedStored.Role)
	require.Equal(t, "elicitation_response", acceptedStored.Type)
	require.NotNil(t, acceptedStored.ElicitationId)
	require.Equal(t, approvalID, *acceptedStored.ElicitationId)
	require.NotNil(t, acceptedStored.ParentMessageId)
	require.Equal(t, elicitor.elicitationMessageID, *acceptedStored.ParentMessageId)
	require.NotNil(t, acceptedStored.TurnId)
	require.Equal(t, nativePromptTurn, *acceptedStored.TurnId)
	require.NotNil(t, acceptedStored.ElicitationPayloadId)
	require.Equal(t, acceptedPayloadID, *acceptedStored.ElicitationPayloadId)
	acceptedPayload, err := conv.GetPayload(ctx, acceptedPayloadID)
	require.NoError(t, err)
	require.NotNil(t, acceptedPayload.InlineBody)
	originalAcceptedBody := append([]byte(nil), (*acceptedPayload.InlineBody)...)
	originalAcceptedCompression := acceptedPayload.Compression
	receiptURL = helperServer.URL + "/prompt-approval-receipt/" + url.PathEscape(nativePromptProbeID) + "?id=" + url.QueryEscape(approvalID)

	// The owner binding is checked at the actual helper HTTP ingress.
	foreignStatus, _ := nativePromptGET(t, receiptURL, "foreign-native-prompt-token")
	require.Equal(t, http.StatusNotFound, foreignStatus)

	// A response with the right elicitation ID but a foreign parent must be rejected.
	foreignParent := apiconv.NewMessage()
	foreignParent.SetId(acceptedMessageID)
	foreignParent.SetParentMessageID("foreign-assistant-parent")
	require.NoError(t, conv.PatchMessage(ctx, foreignParent))
	foreignLinkedStatus, foreignLinkedBody := nativePromptGET(t, receiptURL, nativePromptToken)
	require.Equal(t, http.StatusConflict, foreignLinkedStatus, string(foreignLinkedBody))
	foreignParent.SetParentMessageID(elicitor.elicitationMessageID)
	require.NoError(t, conv.PatchMessage(ctx, foreignParent))
	restoredStatus, restoredBody := nativePromptGET(t, receiptURL, nativePromptToken)
	require.Equal(t, http.StatusOK, restoredStatus, string(restoredBody))

	foreignTurnRow := apiconv.NewTurn()
	foreignTurnRow.SetId("native-prompt-foreign-turn")
	foreignTurnRow.SetConversationID(nativePromptConversation)
	foreignTurnRow.SetStatus("running")
	require.NoError(t, conv.PatchTurn(ctx, foreignTurnRow))
	foreignTurn := apiconv.NewMessage()
	foreignTurn.SetId(acceptedMessageID)
	foreignTurn.SetTurnID("native-prompt-foreign-turn")
	require.NoError(t, conv.PatchMessage(ctx, foreignTurn))
	foreignTurnStatus, foreignTurnBody := nativePromptGET(t, receiptURL, nativePromptToken)
	require.Equal(t, http.StatusConflict, foreignTurnStatus, string(foreignTurnBody))
	foreignTurn.SetTurnID(nativePromptTurn)
	require.NoError(t, conv.PatchMessage(ctx, foreignTurn))
	restoredStatus, restoredBody = nativePromptGET(t, receiptURL, nativePromptToken)
	require.Equal(t, http.StatusOK, restoredStatus, string(restoredBody))

	// A response linked to another elicitation no longer satisfies the captured identity.
	missingAccepted := apiconv.NewMessage()
	missingAccepted.SetId(acceptedMessageID)
	missingAccepted.SetElicitationID("foreign-elicitation")
	require.NoError(t, conv.PatchMessage(ctx, missingAccepted))
	missingStatus, missingBody := nativePromptGET(t, receiptURL, nativePromptToken)
	require.Equal(t, http.StatusConflict, missingStatus, string(missingBody))
	missingAccepted.SetElicitationID(approvalID)
	require.NoError(t, conv.PatchMessage(ctx, missingAccepted))
	restoredStatus, restoredBody = nativePromptGET(t, receiptURL, nativePromptToken)
	require.Equal(t, http.StatusOK, restoredStatus, string(restoredBody))

	// A second accepted row on the same actual conversation/turn/prompt is ambiguous.
	duplicatePayloadBody := []byte(`{"editedFields":{"names":["HOME","PATH"]}}`)
	duplicatePayload := apiconv.NewPayload()
	duplicatePayload.SetId("native-prompt-duplicate-response-payload")
	duplicatePayload.SetKind("elicitation_response")
	duplicatePayload.SetMimeType("application/json")
	duplicatePayload.SetSizeBytes(len(duplicatePayloadBody))
	duplicatePayload.SetStorage("inline")
	duplicatePayload.SetInlineBody(duplicatePayloadBody)
	require.NoError(t, conv.PatchPayload(ctx, duplicatePayload))
	duplicateMessage := apiconv.NewMessage()
	duplicateMessage.SetId("native-prompt-duplicate-response-message")
	duplicateMessage.SetConversationID(nativePromptConversation)
	duplicateMessage.SetTurnID(nativePromptTurn)
	duplicateMessage.SetParentMessageID(elicitor.elicitationMessageID)
	duplicateMessage.SetRole("user")
	duplicateMessage.SetType("elicitation_response")
	duplicateMessage.SetStatus("accepted")
	duplicateMessage.SetElicitationID(approvalID)
	duplicateMessage.SetElicitationPayloadID("native-prompt-duplicate-response-payload")
	require.NoError(t, conv.PatchMessage(ctx, duplicateMessage))
	duplicateStatus, duplicateBody := nativePromptGET(t, receiptURL, nativePromptToken)
	require.Equal(t, http.StatusConflict, duplicateStatus, string(duplicateBody))
	require.NoError(t, conv.DeleteMessage(ctx, nativePromptConversation, "native-prompt-duplicate-response-message"))
	restoredStatus, restoredBody = nativePromptGET(t, receiptURL, nativePromptToken)
	require.Equal(t, http.StatusOK, restoredStatus, string(restoredBody))

	// Once a successful receipt pins response identity and bytes, later payload drift is rejected.
	drift := apiconv.NewPayload()
	drift.SetId(acceptedPayloadID)
	drift.SetInlineBody([]byte(`{"editedFields":{"names":["PATH","HOME"]}}`))
	drift.SetCompression("")
	require.NoError(t, conv.PatchPayload(ctx, drift))
	driftStatus, driftBody := nativePromptGET(t, receiptURL, nativePromptToken)
	require.Equal(t, http.StatusConflict, driftStatus, string(driftBody))
	drift.SetInlineBody(originalAcceptedBody)
	drift.SetCompression(originalAcceptedCompression)
	require.NoError(t, conv.PatchPayload(ctx, drift))
	restoredStatus, restoredBody = nativePromptGET(t, receiptURL, nativePromptToken)
	require.Equal(t, http.StatusOK, restoredStatus, string(restoredBody))
	require.Equal(t, 1, registry.calls, "negative receipt reads must not redispatch the native tool")
}
