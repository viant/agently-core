package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/tool"
	approvalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	"github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/elicitation"
	"github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/agently-core/service/shared/toolapproval"
	"github.com/viant/agently-core/service/shared/toolexec"
	"github.com/viant/mcp-protocol/schema"
)

func TestBuildHistory_CompletedApprovalIsExecutionReview(t *testing.T) {
	now := time.Now().UTC()
	body := `{"values":{"HOME":"fixture-home","PATH":"fixture-path"}}`
	response := `{"editedFields":{"names":["HOME","PATH"]},"approvalDecision":{"fields":{"names":{"approved":["HOME","PATH"],"denied":["SHELL"]}}}}`
	request := []byte(`{"message":"Approve environment read","requestedSchema":{"type":"object","properties":{"_type":{"const":"tool_approval"},"_toolName":{"const":"system/os:getEnv"}}}}`)
	svc := &Service{conversation: &stubConversationClient{payloads: map[string]*apiconv.Payload{"review-request": {Id: "review-request", Kind: "elicitation_request", InlineBody: &request}}}}
	transcript := apiconv.Transcript{{Id: "turn", Message: []*conversationmodel.MessageView{
		{Id: "user", ConversationId: "conversation", Role: "user", Type: "text", Content: strPtr("Read the requested environment once"), CreatedAt: now},
		{Id: "model", ConversationId: "conversation", Role: "assistant", Type: "text", Status: strPtr("accepted"), ElicitationId: strPtr("review"), ElicitationPayloadId: strPtr("review-request"), Content: strPtr("Approve environment read"), CreatedAt: now.Add(time.Second)},
		{Id: "tool", ConversationId: "conversation", ParentMessageId: strPtr("model"), Role: "tool", Type: "tool_op", Content: strPtr(body), CreatedAt: now.Add(2 * time.Second), MessageToolCall: &conversationmodel.MessageToolCallView{OpId: "original-op", ToolName: "system_os-getEnv", Status: "completed", MessageRequestPayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr(`{"names":["HOME","SHELL","PATH"]}`)}, MessageResponsePayload: &conversationmodel.ModelCallStreamPayloadView{InlineBody: strPtr(body)}}},
		{Id: "answer", ConversationId: "conversation", ParentMessageId: strPtr("model"), Role: "user", Type: "elicitation_response", ElicitationId: strPtr("review"), Content: strPtr(response), CreatedAt: now.Add(3 * time.Second)},
	}}}
	result, err := svc.buildChronologicalHistory(context.Background(), transcript, nil, false)
	require.NoError(t, err)
	require.Empty(t, result.Elicitation, "completed review must not be appended after the tool result as a fresh elicitation answer")
	history := result.History
	var tool *binding.Message
	for _, turn := range history.Past {
		for _, msg := range turn.Messages {
			require.NotEqual(t, "answer", msg.ID, "a completed approval callback must not become a new user task")
			if msg.ToolOpID == "original-op" {
				tool = msg
			}
		}
	}
	require.NotNil(t, tool)
	output, review, found := strings.Cut(tool.Content, "\n\nExecution review for this completed tool call (persisted accepted user decision; review metadata):\n")
	require.True(t, found)
	require.Equal(t, body, output, "tool output remains byte-for-byte intact")
	require.JSONEq(t, `{"action":"accept","payload":`+response+`}`, review)
	require.Contains(t, review, "SHELL", "the denied selection remains visible in the execution review")
	require.Equal(t, []interface{}{"HOME", "SHELL", "PATH"}, tool.ToolArgs["names"], "original model call identity remains immutable")
	require.Equal(t, response, *transcript[0].Message[3].Content, "native audit/UI response remains intact")
	require.Equal(t, body, *transcript[0].Message[2].Content, "native tool output remains intact")
	reviews, hidden := svc.completedApprovalReviews(context.Background(), transcript, "tool")
	require.Empty(t, reviews)
	require.Empty(t, hidden, "a hidden result cannot absorb a visible decision")
	for _, tc := range []struct {
		name   string
		mutate func(apiconv.Transcript)
	}{
		{"authored assistant narration", func(v apiconv.Transcript) { v[0].Message[1].Content = strPtr("Public notes accompanying approval") }},
		{"pending review", func(v apiconv.Transcript) { v[0].Message[1].Status = strPtr("pending") }},
		{"declined review", func(v apiconv.Transcript) { v[0].Message[1].Status = strPtr("rejected") }},
		{"ambiguous responses", func(v apiconv.Transcript) {
			copied := *v[0].Message[3]
			copied.Id = "other-answer"
			v[0].Message = append(v[0].Message, &copied)
		}},
		{"pending execution", func(v apiconv.Transcript) { v[0].Message[2].MessageToolCall.Status = "running" }},
		{"failed execution", func(v apiconv.Transcript) { v[0].Message[2].MessageToolCall.Status = "failed" }},
		{"ordinary askUser", func(v apiconv.Transcript) { v[0].Message[2].MessageToolCall.ToolName = "message-askUser" }},
		{"different request parent", func(v apiconv.Transcript) { v[0].Message[3].ParentMessageId = strPtr("other") }},
		{"different elicitation", func(v apiconv.Transcript) { v[0].Message[3].ElicitationId = strPtr("other") }},
		{"different conversation", func(v apiconv.Transcript) { v[0].Message[3].ConversationId = "other" }},
		{"missing durable schema", func(v apiconv.Transcript) { v[0].Message[1].ElicitationPayloadId = strPtr("missing") }},
		{"ambiguous same tool", func(v apiconv.Transcript) {
			copied := *v[0].Message[2]
			copied.Id = "other-tool"
			v[0].Message = append(v[0].Message, &copied)
		}},
		{"empty tool result", func(v apiconv.Transcript) {
			v[0].Message[2].Content = strPtr("")
			v[0].Message[2].MessageToolCall.MessageResponsePayload.InlineBody = strPtr("")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(transcript)
			require.NoError(t, err)
			var candidate apiconv.Transcript
			require.NoError(t, json.Unmarshal(raw, &candidate))
			tc.mutate(candidate)
			reviews, hidden := svc.completedApprovalReviews(context.Background(), candidate)
			require.Empty(t, reviews)
			require.Empty(t, hidden, "unbound responses must retain their original model context")
		})
	}
}

type nativeApprovalTestElicitor struct {
	service         *elicitation.Service
	reviewReference string
}

func (e nativeApprovalTestElicitor) ElicitToolApproval(ctx context.Context, turn *requestctx.TurnMeta, name string, cfg *llm.ApprovalConfig, args map[string]interface{}) (string, map[string]interface{}, error) {
	view := toolapproval.BuildView(name, args, cfg)
	req := &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: view.Message, RequestedSchema: buildApprovalRequestedSchema(name, view, cfg, args, "Accept", "Reject", "Cancel")}}
	if _, err := e.service.Record(ctx, turn, "assistant", req); err != nil {
		return "", nil, err
	}
	payload := map[string]interface{}{"editedFields": map[string]interface{}{"names": []interface{}{"HOME", "PATH"}}}
	if e.reviewReference != "" {
		payload["reviewReference"] = e.reviewReference
	}
	_, err := e.service.ResolveChecked(ctx, turn.ConversationID, req.ElicitationId, "accept", payload, "")
	return "accept", payload, err
}

type nativeApprovalTestRegistry struct {
	tool.Registry
	names []interface{}
	calls int
}

func (r *nativeApprovalTestRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) {
	return &llm.ToolDefinition{Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"names": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}}}}, true
}
func (r *nativeApprovalTestRegistry) Execute(_ context.Context, _ string, args map[string]interface{}) (string, error) {
	r.calls++
	r.names = args["names"].([]interface{})
	return `{"values":{"HOME":"fixture-home","PATH":"fixture-path"}}`, nil
}

func TestBuildHistory_ActualNativeEditedApprovalExecution(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
	root := t.TempDir()
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(root, "artifacts", "${userID}")))
	artifactID := "2f1c171e-0fe9-4aa7-8f17-5eb826f72042"
	macro := "${artifact[" + artifactID + "].payload;xform=base64}"
	artifactBytes := []byte("fixture-expanded-bytes-must-stay-private")
	_, err := scratchpad.New().PublishArtifact(ctx, artifactID, "fixture.bin", "application/octet-stream", "", bytes.NewReader(artifactBytes))
	require.NoError(t, err)
	server, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	conversation := apiconv.NewConversation()
	conversation.SetId("review-conversation")
	conversation.SetCreatedByUserID("user")
	require.NoError(t, conv.PatchConversations(ctx, conversation))
	turn := apiconv.NewTurn()
	turn.SetId("review-turn")
	turn.SetConversationID("review-conversation")
	turn.SetStatus("running")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	model := apiconv.NewMessage()
	model.SetId("review-model")
	model.SetConversationID("review-conversation")
	model.SetTurnID("review-turn")
	model.SetRole("assistant")
	model.SetType("text")
	model.SetContent("Read environment once")
	require.NoError(t, conv.PatchMessage(ctx, model))
	ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "review-conversation", TurnID: "review-turn", ParentMessageID: "review-model"})
	ctx = requestctx.WithModelMessageID(ctx, "review-model")
	ctx = approvalqueue.WithState(ctx)
	cfg := &llm.ApprovalConfig{Mode: llm.ApprovalModePrompt, UI: &llm.ApprovalUIBinding{Editable: []*llm.ApprovalEditableField{{Name: "names", Selector: "input.names", Kind: "checkbox_list"}}}}
	approvalqueue.MarkTool(ctx, "system/os:getEnv", cfg)
	ctx = toolapproval.WithElicitor(ctx, nativeApprovalTestElicitor{service: elicitation.New(conv, nil, router.New(), nil), reviewReference: macro})
	registry := &nativeApprovalTestRegistry{}
	args := map[string]interface{}{"names": []interface{}{"HOME", "SHELL", "PATH"}}
	call, _, err := toolexec.ExecuteToolStep(ctx, registry, toolexec.StepInfo{ID: "review-op", Name: "system/os:getEnv", Args: args}, conv)
	require.NoError(t, err)
	require.Equal(t, 1, registry.calls)
	require.Equal(t, []interface{}{"HOME", "PATH"}, registry.names, "denied field never reaches native execution")
	require.Equal(t, []interface{}{"HOME", "PATH"}, call.Arguments["names"], "live result records actual executed arguments")
	stored, err := conv.GetConversation(ctx, "review-conversation", apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	var persistedOriginalNames []string
	var persistedEditedNames []interface{}
	var persistedNativeRequestNames []interface{}
	for _, persistedTurn := range stored.GetTranscript() {
		for _, persistedMessage := range persistedTurn.GetMessages() {
			if persistedMessage.MessageToolCall != nil && persistedMessage.MessageToolCall.RequestPayloadId != nil {
				requestPayload, payloadErr := conv.GetPayload(ctx, *persistedMessage.MessageToolCall.RequestPayloadId)
				require.NoError(t, payloadErr)
				require.NotNil(t, requestPayload.InlineBody)
				requestBody := decodePayloadInlineBody(string(*requestPayload.InlineBody), requestPayload.Compression)
				var nativeRequest map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(requestBody), &nativeRequest))
				persistedNativeRequestNames = nativeRequest["names"].([]interface{})
			}
			if persistedMessage.ElicitationPayloadId == nil {
				continue
			}
			payload, payloadErr := conv.GetPayload(ctx, *persistedMessage.ElicitationPayloadId)
			require.NoError(t, payloadErr)
			require.NotNil(t, payload.InlineBody)
			switch payload.Kind {
			case "elicitation_request":
				var request map[string]interface{}
				require.NoError(t, json.Unmarshal(*payload.InlineBody, &request))
				schema := request["requestedSchema"].(map[string]interface{})
				properties := schema["properties"].(map[string]interface{})
				metaProperty := properties["_approvalMeta"].(map[string]interface{})
				var meta map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(metaProperty["const"].(string)), &meta))
				editors := meta["editors"].([]interface{})
				require.Len(t, editors, 1)
				options := editors[0].(map[string]interface{})["options"].([]interface{})
				for _, option := range options {
					persistedOriginalNames = append(persistedOriginalNames, option.(map[string]interface{})["id"].(string))
				}
			case "elicitation_response":
				var response map[string]interface{}
				require.NoError(t, json.Unmarshal(*payload.InlineBody, &response))
				edited := response["editedFields"].(map[string]interface{})
				persistedEditedNames = edited["names"].([]interface{})
			}
		}
	}
	require.Equal(t, []interface{}{"HOME", "SHELL", "PATH"}, persistedNativeRequestNames, "durable original native tool request keeps all submitted arguments")
	require.Equal(t, []string{"HOME", "SHELL", "PATH"}, persistedOriginalNames, "durable request schema keeps all pre-approval choices")
	require.Equal(t, []interface{}{"HOME", "PATH"}, persistedEditedNames, "durable elicitation response records only accepted choices")
	svc := &Service{conversation: conv}
	history, err := svc.buildChronologicalHistory(ctx, stored.GetTranscript(), nil, false)
	require.NoError(t, err)
	require.Empty(t, history.Elicitation)
	require.NotNil(t, history.History.Current)
	var completed *binding.Message
	for _, msg := range history.History.Current.Messages {
		if msg.ToolOpID == "review-op" {
			completed = msg
		}
		require.NotEqual(t, binding.MessageKindElicitAnswer, msg.Kind)
	}
	require.NotNil(t, completed)
	require.Equal(t, []interface{}{"HOME", "SHELL", "PATH"}, completed.ToolArgs["names"], "durable original review identity remains immutable")
	require.True(t, strings.HasPrefix(completed.Content, call.Result))
	require.Contains(t, completed.Content, `"denied":["SHELL"]`)
	require.Contains(t, completed.Content, macro, "accepted review retains the full literal artifact reference")
	require.NotContains(t, completed.Content, string(artifactBytes))
	for _, turn := range stored.GetTranscript() {
		for _, msg := range turn.GetMessages() {
			if msg.Type == "elicitation_response" {
				require.Contains(t, msg.GetContent(), macro)
				require.NotContains(t, msg.GetContent(), string(artifactBytes))
				payload, err := conv.GetPayload(ctx, *msg.ElicitationPayloadId)
				require.NoError(t, err)
				require.Contains(t, string(*payload.InlineBody), macro)
				require.NotContains(t, string(*payload.InlineBody), string(artifactBytes))
			}
		}
	}
}
