package reactor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	messagesvc "github.com/viant/agently-core/protocol/tool/service/message"
	memory "github.com/viant/agently-core/runtime/requestctx"
	core "github.com/viant/agently-core/service/core"
)

type messageSaveRegistry struct {
	reactorScriptedRegistry
	message *messagesvc.Service
	saved   []string
}

func (r *messageSaveRegistry) Execute(ctx context.Context, _ string, arguments map[string]interface{}) (string, error) {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return "", err
	}
	var input messagesvc.AddInput
	if err = json.Unmarshal(raw, &input); err != nil {
		return "", err
	}
	add, err := r.message.Method("add")
	if err != nil {
		return "", err
	}
	var output messagesvc.AddOutput
	if err = add(ctx, &input, &output); err != nil {
		return "", err
	}
	r.saved = append(r.saved, output.MessageID)
	raw, err = json.Marshal(output)
	return string(raw), err
}

func TestRun_ReportSaveFailureAllowsCorrectedNextIteration(t *testing.T) {
	store := convmem.New()
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "example", TurnID: "report-turn"})
	ctx = memory.WithConversationID(ctx, "example")
	conversation := apiconv.NewConversation()
	conversation.SetId("example")
	require.NoError(t, store.PatchConversations(ctx, conversation))
	registry := &messageSaveRegistry{message: messagesvc.New(store)}
	response := func(id, content string) *llm.GenerateResponse {
		return &llm.GenerateResponse{ResponseID: id, Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "message-add", Arguments: map[string]interface{}{"content": content}}}}, FinishReason: "tool_calls"}}}
	}
	invalid := "```forge-report\n" + `{"version":1,"id":"example","sequence":1,"mode":"start","title":"A "quoted" title"}` + "\n```"
	valid := "```forge-report\n" + `{"version":1,"id":"example","sequence":1,"mode":"start","grammar":"report-document-v1","title":"A \"quoted\" title","blocks":[{"id":"finding","kind":"markdownBlock","markdown":"Observed value."}]}` + "\n```"
	model := &plannedCallsModel{response: response("initial-save", invalid)}
	service := New(core.New(plannedCallsFinder{model: model}, registry, store), registry, store, nil, nil)
	input := &core.GenerateInput{ModelSelection: llm.ModelSelection{Model: "mock"}, UserID: "example-user", Prompt: &binding.Prompt{Text: "Create the report."}, Binding: &binding.Binding{}}
	_, err := service.Run(ctx, input, &core.GenerateOutput{})
	require.NoError(t, err, "a correctable save error must reach the next model iteration")
	require.Empty(t, registry.saved)
	results := service.TurnToolResults("report-turn")
	require.Len(t, results, 1)
	require.Contains(t, results[0].Result, "report was not saved")
	require.Contains(t, results[0].Result, "retry message:add")
	require.NotEmpty(t, results[0].ResultMessageID)

	model.response = response("corrected-save", valid)
	_, err = service.Run(ctx, input, &core.GenerateOutput{})
	require.NoError(t, err)
	require.Equal(t, 2, model.calls)
	require.Len(t, registry.saved, 1)
	message, err := store.GetMessage(ctx, registry.saved[0])
	require.NoError(t, err)
	require.Equal(t, valid, message.GetContent())
	require.Len(t, service.TurnToolResults("report-turn"), 2)
}
