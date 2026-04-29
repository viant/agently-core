package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"testing"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/binding"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestBuildHistory_PreservesUserMessageAndToolResult(t *testing.T) {
	now := time.Now().UTC()
	body := "{\"values\":{\"USER\":\"adrianwitas\"}}"
	turnID := "turn-1"
	parentID := "msg-user"

	testCases := []struct {
		name         string
		service      *Service
		responseBody *agconv.ModelCallStreamPayloadView
	}{
		{
			name:    "inline response payload",
			service: &Service{},
			responseBody: &agconv.ModelCallStreamPayloadView{
				InlineBody: strPtr(body),
			},
		},
		{
			name:    "gzip inline response payload",
			service: &Service{},
			responseBody: &agconv.ModelCallStreamPayloadView{
				InlineBody:  strPtr(gzipString(t, body)),
				Compression: "gzip",
			},
		},
		{
			name: "payload id response payload",
			service: &Service{
				conversation: &stubConversationClient{
					payloads: map[string]*apiconv.Payload{
						"payload-1": {
							Id:         "payload-1",
							InlineBody: ptrBytes([]byte(body)),
						},
					},
				},
			},
			responseBody: &agconv.ModelCallStreamPayloadView{
				Id: "payload-1",
			},
		},
		{
			name: "payload id gzip response payload",
			service: &Service{
				conversation: &stubConversationClient{
					payloads: map[string]*apiconv.Payload{
						"payload-gzip": {
							Id:          "payload-gzip",
							InlineBody:  ptrBytes([]byte(gzipString(t, body))),
							Compression: "gzip",
						},
					},
				},
			},
			responseBody: &agconv.ModelCallStreamPayloadView{
				Id: "payload-gzip",
			},
		},
		{
			name: "payload id preferred over corrupted inline response payload",
			service: &Service{
				conversation: &stubConversationClient{
					payloads: map[string]*apiconv.Payload{
						"payload-inline-corrupt": {
							Id:         "payload-inline-corrupt",
							InlineBody: ptrBytes([]byte(body)),
						},
					},
				},
			},
			responseBody: &agconv.ModelCallStreamPayloadView{
				Id:          "payload-inline-corrupt",
				InlineBody:  strPtr("not-a-valid-gzip-payload"),
				Compression: "gzip",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			transcript := apiconv.Transcript{
				&apiconv.Turn{
					Id: turnID,
					Message: []*agconv.MessageView{
						{
							Id:             parentID,
							ConversationId: "conv-1",
							TurnId:         strPtr(turnID),
							Role:           "user",
							Type:           "text",
							Content:        strPtr("Call system_os-getEnv to retrieve USER"),
							CreatedAt:      now,
							ToolMessage: []*agconv.ToolMessageView{
								{
									Id:        "tool-msg-1",
									CreatedAt: now.Add(time.Second),
									ToolCall: &agconv.ToolCallView{
										OpId:            "op-1",
										ToolName:        "system_os-getEnv",
										ResponsePayload: testCase.responseBody,
										RequestPayload:  &agconv.ModelCallStreamPayloadView{InlineBody: strPtr("{\"names\":[\"USER\"]}")},
									},
								},
							},
						},
					},
				},
			}

			history, err := testCase.service.buildHistory(context.Background(), transcript)
			if err != nil {
				t.Fatalf("buildHistory error: %v", err)
			}
			if len(history.Past) != 1 || len(history.Past[0].Messages) != 2 {
				t.Fatalf("expected user + tool result messages, got %#v", history.Past)
			}
			if history.Past[0].Messages[0].Kind != binding.MessageKindChatUser {
				t.Fatalf("expected first message to remain user chat, got %v", history.Past[0].Messages[0].Kind)
			}
			if history.Past[0].Messages[1].Kind != binding.MessageKindToolResult {
				t.Fatalf("expected second message to be tool result, got %v", history.Past[0].Messages[1].Kind)
			}
			if history.Past[0].Messages[1].Content != body {
				t.Fatalf("expected tool result body, got %q", history.Past[0].Messages[1].Content)
			}
		})
	}
}

func TestBuildHistory_PreservesChildToolResultAlongsideUpdatePlan(t *testing.T) {
	now := time.Now().UTC()
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: "turn-1",
			Message: []*agconv.MessageView{
				{
					Id:             "msg-user",
					ConversationId: "conv-1",
					TurnId:         strPtr("turn-1"),
					Role:           "user",
					Type:           "text",
					Content:        strPtr("Recommend sitelists for audience 7180287"),
					CreatedAt:      now,
					ToolMessage: []*agconv.ToolMessageView{
						{
							Id:        "tool-msg-plan",
							CreatedAt: now.Add(time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:            "op-plan",
								ToolName:        "orchestration/updatePlan",
								ResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"plan":[{"step":"Inspect targeting","status":"in_progress"}]}`)},
							},
						},
						{
							Id:        "tool-msg-child",
							CreatedAt: now.Add(2 * time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:            "op-child",
								ToolName:        "llm/agents/run",
								ResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"answer":"Child agent found target site list 117385 but matching failed due to access constraints."}`)},
							},
						},
					},
				},
			},
		},
	}

	history, err := (&Service{}).buildHistory(context.Background(), transcript)
	if err != nil {
		t.Fatalf("buildHistory error: %v", err)
	}
	if len(history.Past) != 1 || len(history.Past[0].Messages) != 3 {
		t.Fatalf("expected user message plus both tool results, got %#v", history.Past)
	}
	if history.Past[0].Messages[1].Kind != binding.MessageKindToolResult {
		t.Fatalf("expected second message to be tool result, got %v", history.Past[0].Messages[1].Kind)
	}
	if got := history.Past[0].Messages[2].ToolName; got != "llm/agents/run" {
		t.Fatalf("expected preserved child tool result to be llm/agents/run, got %q", got)
	}
}

func TestBuildHistory_DoesNotDuplicateToolResultsWhenToolOpsExistAsRealMessages(t *testing.T) {
	now := time.Now().UTC()
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: "turn-1",
			Message: []*agconv.MessageView{
				{
					Id:             "msg-user",
					ConversationId: "conv-1",
					TurnId:         strPtr("turn-1"),
					Role:           "user",
					Type:           "text",
					Content:        strPtr("Recommend sitelists for audience 7180287"),
					CreatedAt:      now,
				},
				{
					Id:             "msg-assistant-1",
					ConversationId: "conv-1",
					TurnId:         strPtr("turn-1"),
					Role:           "assistant",
					Type:           "text",
					Content:        strPtr("I’ll inspect targeting first."),
					CreatedAt:      now.Add(time.Second),
					ToolMessage: []*agconv.ToolMessageView{
						{
							Id:        "tool-msg-plan",
							CreatedAt: now.Add(2 * time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:            "op-plan",
								ToolName:        "orchestration/updatePlan",
								ResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"plan":[{"step":"Inspect targeting","status":"in_progress"}]}`)},
							},
						},
					},
				},
				{
					Id:              "tool-msg-plan",
					ConversationId:  "conv-1",
					TurnId:          strPtr("turn-1"),
					ParentMessageId: strPtr("msg-assistant-1"),
					Role:            "tool",
					Type:            "tool_op",
					Content:         strPtr(`{"plan":[{"step":"Inspect targeting","status":"in_progress"}]}`),
					CreatedAt:       now.Add(2 * time.Second),
					ToolMessage: []*agconv.ToolMessageView{
						{
							Id:        "tool-msg-plan",
							CreatedAt: now.Add(2 * time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:            "op-plan",
								ToolName:        "orchestration/updatePlan",
								ResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"plan":[{"step":"Inspect targeting","status":"in_progress"}]}`)},
							},
						},
					},
				},
			},
		},
	}

	history, err := (&Service{}).buildHistory(context.Background(), transcript)
	if err != nil {
		t.Fatalf("buildHistory error: %v", err)
	}
	if len(history.Past) != 1 {
		t.Fatalf("expected a single turn in history, got %#v", history.Past)
	}
	gotToolResults := 0
	for _, msg := range history.Past[0].Messages {
		if msg.Kind == binding.MessageKindToolResult {
			gotToolResults++
		}
	}
	if gotToolResults != 1 {
		t.Fatalf("expected exactly one tool result in prompt history, got %d with history=%#v", gotToolResults, history.Past[0].Messages)
	}
}

func TestBuildHistory_PrefersConcreteToolResultOverAssistantWrapperWithStaleStatusText(t *testing.T) {
	now := time.Now().UTC()
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: "turn-1",
			Message: []*agconv.MessageView{
				{
					Id:             "msg-user",
					ConversationId: "conv-1",
					TurnId:         strPtr("turn-1"),
					Role:           "user",
					Type:           "text",
					Content:        strPtr("Troubleshoot order"),
					CreatedAt:      now,
				},
				{
					Id:             "msg-progress",
					ConversationId: "conv-1",
					TurnId:         strPtr("turn-1"),
					Role:           "assistant",
					Type:           "text",
					Content:        strPtr("I’m continuing the blocker cross-check."),
					CreatedAt:      now.Add(time.Second),
					ToolMessage: []*agconv.ToolMessageView{
						{
							Id:        "tool-msg-status",
							CreatedAt: now.Add(2 * time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:            "op-status",
								ToolName:        "llm/agents/status",
								ResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`The order has some display delivery but almost no video/CTV`)},
							},
						},
					},
				},
				{
					Id:              "tool-msg-status",
					ConversationId:  "conv-1",
					TurnId:          strPtr("turn-1"),
					ParentMessageId: strPtr("msg-progress"),
					Role:            "tool",
					Type:            "tool_op",
					Content:         strPtr(`{"conversationId":"child-1","hasFinalResponse":true,"message":"<!-- DATA:delivery_by_day rows=13 source=steward-MetricsAdCube -->"}`),
					CreatedAt:       now.Add(3 * time.Second),
					ToolMessage: []*agconv.ToolMessageView{
						{
							Id:        "tool-msg-status",
							CreatedAt: now.Add(3 * time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:            "op-status",
								ToolName:        "llm/agents/status",
								ResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"conversationId":"child-1","hasFinalResponse":true,"message":"<!-- DATA:delivery_by_day rows=13 source=steward-MetricsAdCube -->"}`)},
							},
						},
					},
				},
			},
		},
	}

	history, err := (&Service{}).buildHistory(context.Background(), transcript)
	if err != nil {
		t.Fatalf("buildHistory error: %v", err)
	}
	if len(history.Past) != 1 {
		t.Fatalf("expected a single turn in history, got %#v", history.Past)
	}
	if len(history.Past[0].Messages) != 3 {
		t.Fatalf("expected user + assistant progress + final tool result, got %#v", history.Past[0].Messages)
	}
	if history.Past[0].Messages[1].Kind != binding.MessageKindChatAssistant {
		t.Fatalf("expected assistant wrapper to remain chat context, got %v", history.Past[0].Messages[1].Kind)
	}
	if got := history.Past[0].Messages[1].Content; got != "I’m continuing the blocker cross-check." {
		t.Fatalf("expected assistant progress content, got %q", got)
	}
	if history.Past[0].Messages[2].Kind != binding.MessageKindToolResult {
		t.Fatalf("expected final concrete tool row to be the tool result, got %v", history.Past[0].Messages[2].Kind)
	}
	if got := history.Past[0].Messages[2].ToolName; got != "llm/agents/status" {
		t.Fatalf("expected llm/agents/status tool result, got %q", got)
	}
	if got := history.Past[0].Messages[2].Content; got != `{"conversationId":"child-1","hasFinalResponse":true,"message":"<!-- DATA:delivery_by_day rows=13 source=steward-MetricsAdCube -->"}` {
		t.Fatalf("expected concrete final tool payload, got %q", got)
	}
}

func TestBuildHistory_RecognizesDirectToolCallOnConcreteToolRow(t *testing.T) {
	now := time.Now().UTC()
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: "turn-1",
			Message: []*agconv.MessageView{
				{
					Id:             "msg-user",
					ConversationId: "conv-1",
					TurnId:         strPtr("turn-1"),
					Role:           "user",
					Type:           "text",
					Content:        strPtr("Troubleshoot order"),
					CreatedAt:      now,
				},
				{
					Id:              "tool-msg-status-1",
					ConversationId:  "conv-1",
					TurnId:          strPtr("turn-1"),
					ParentMessageId: strPtr("msg-user"),
					Role:            "tool",
					Type:            "tool_op",
					Content:         strPtr(`{"conversationId":"child-1","hasFinalResponse":true,"message":"config result"}`),
					CreatedAt:       now.Add(time.Second),
					MessageToolCall: &agconv.MessageToolCallView{
						OpId:                   "async-status:child-1",
						ToolName:               "llm/agents/status",
						MessageRequestPayload:  &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"conversationId":"child-1"}`)},
						MessageResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"conversationId":"child-1","hasFinalResponse":true,"message":"config result"}`)},
					},
				},
				{
					Id:              "tool-msg-status-2",
					ConversationId:  "conv-1",
					TurnId:          strPtr("turn-1"),
					ParentMessageId: strPtr("msg-user"),
					Role:            "tool",
					Type:            "tool_op",
					Content:         strPtr(`{"conversationId":"child-2","hasFinalResponse":true,"message":"inventory result"}`),
					CreatedAt:       now.Add(2 * time.Second),
					MessageToolCall: &agconv.MessageToolCallView{
						OpId:                   "async-status:child-2",
						ToolName:               "llm/agents/status",
						MessageRequestPayload:  &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"conversationId":"child-2"}`)},
						MessageResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"conversationId":"child-2","hasFinalResponse":true,"message":"inventory result"}`)},
					},
				},
				{
					Id:              "tool-msg-status-3",
					ConversationId:  "conv-1",
					TurnId:          strPtr("turn-1"),
					ParentMessageId: strPtr("msg-user"),
					Role:            "tool",
					Type:            "tool_op",
					Content:         strPtr(`{"conversationId":"child-3","hasFinalResponse":true,"message":"performance result"}`),
					CreatedAt:       now.Add(3 * time.Second),
					MessageToolCall: &agconv.MessageToolCallView{
						OpId:                   "async-status:child-3",
						ToolName:               "llm/agents/status",
						MessageRequestPayload:  &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"conversationId":"child-3"}`)},
						MessageResponsePayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"conversationId":"child-3","hasFinalResponse":true,"message":"performance result"}`)},
					},
				},
			},
		},
	}

	history, err := (&Service{}).buildHistory(context.Background(), transcript)
	if err != nil {
		t.Fatalf("buildHistory error: %v", err)
	}
	if len(history.Past) != 1 {
		t.Fatalf("expected a single turn in history, got %#v", history.Past)
	}
	gotToolResults := 0
	for _, msg := range history.Past[0].Messages {
		if msg.Kind != binding.MessageKindToolResult {
			continue
		}
		if got := msg.ToolName; got != "llm/agents/status" {
			t.Fatalf("expected llm/agents/status tool result, got %q", got)
		}
		gotToolResults++
	}
	if gotToolResults != 3 {
		t.Fatalf("expected 3 concrete status tool results, got %d with history=%#v", gotToolResults, history.Past[0].Messages)
	}
}

func TestBuildHistory_SkipsInjectedDocumentToolResultWhenMessagesArePersisted(t *testing.T) {
	now := time.Now().UTC()
	turnID := "turn-1"
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: turnID,
			Message: []*agconv.MessageView{
				{
					Id:             "msg-user",
					ConversationId: "conv-1",
					TurnId:         strPtr(turnID),
					Role:           "user",
					Type:           "text",
					Content:        strPtr("Use prompt profile performance_analysis"),
					CreatedAt:      now,
					ToolMessage: []*agconv.ToolMessageView{
						{
							Id:        "tool-msg-prompt",
							CreatedAt: now.Add(time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:     "op-prompt",
								ToolName: "prompt-get",
								ResponsePayload: &agconv.ModelCallStreamPayloadView{
									InlineBody: strPtr(`{"id":"performance_analysis","injected":true,"messages":[{"role":"system","text":"You are a performance analyst."},{"role":"user","text":"Analyze the provided metrics."}]}`),
								},
							},
						},
					},
				},
				{
					Id:             "msg-system",
					ConversationId: "conv-1",
					TurnId:         strPtr(turnID),
					Role:           "system",
					Type:           "text",
					Content:        strPtr("You are a performance analyst."),
					CreatedAt:      now.Add(2 * time.Second),
				},
				{
					Id:             "msg-user-injected",
					ConversationId: "conv-1",
					TurnId:         strPtr(turnID),
					Role:           "user",
					Type:           "text",
					Content:        strPtr("Analyze the provided metrics."),
					CreatedAt:      now.Add(3 * time.Second),
				},
			},
		},
	}

	history, err := (&Service{}).buildHistory(context.Background(), transcript)
	if err != nil {
		t.Fatalf("buildHistory error: %v", err)
	}
	if len(history.Past) != 1 {
		t.Fatalf("expected a single turn in history, got %#v", history.Past)
	}
	gotToolResults := 0
	gotUsers := 0
	for _, msg := range history.Past[0].Messages {
		switch msg.Kind {
		case binding.MessageKindToolResult:
			gotToolResults++
		case binding.MessageKindChatUser:
			gotUsers++
		}
	}
	if gotToolResults != 0 {
		t.Fatalf("expected injected-document tool result to be omitted from prompt history, got %#v", history.Past[0].Messages)
	}
	if gotUsers != 2 {
		t.Fatalf("expected persisted injected messages to remain in history, got %#v", history.Past[0].Messages)
	}
}

func TestShouldSkipInjectedDocumentToolResultBody(t *testing.T) {
	testCases := []struct {
		body string
		want bool
	}{
		{body: `{"injected":true}`, want: true},
		{body: `{"includedDocument":true}`, want: true},
		{body: `{"injected":false,"includedDocument":false}`, want: false},
		{body: `{"result":"ok"}`, want: false},
		{body: `not-json`, want: false},
	}
	for _, testCase := range testCases {
		if got := shouldSkipInjectedDocumentToolResultBody(testCase.body); got != testCase.want {
			t.Fatalf("body=%q: got %v, want %v", testCase.body, got, testCase.want)
		}
	}
}

func TestBuildHistory_PlacesCurrentTurnInCurrentNotPast(t *testing.T) {
	now := time.Now().UTC()
	turnID := "turn-current"
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: "turn-past",
			Message: []*agconv.MessageView{
				{
					Id:        "msg-past",
					TurnId:    strPtr("turn-past"),
					Role:      "user",
					Type:      "text",
					Content:   strPtr("older message"),
					CreatedAt: now,
				},
			},
		},
		&apiconv.Turn{
			Id: turnID,
			Message: []*agconv.MessageView{
				{
					Id:        "msg-current-user",
					TurnId:    strPtr(turnID),
					Role:      "user",
					Type:      "text",
					Content:   strPtr("Recommend sitelists for audience 7180287"),
					CreatedAt: now.Add(time.Second),
				},
				{
					Id:        "msg-current-assistant",
					TurnId:    strPtr(turnID),
					Role:      "assistant",
					Type:      "text",
					Content:   strPtr("I’ll inspect targeting first."),
					CreatedAt: now.Add(2 * time.Second),
				},
			},
		},
	}

	ctx := context.Background()
	ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "conv-1", TurnID: turnID})

	result, err := (&Service{}).buildChronologicalHistory(ctx, transcript, nil, false)
	if err != nil {
		t.Fatalf("buildChronologicalHistory error: %v", err)
	}
	if len(result.History.Past) != 1 {
		t.Fatalf("expected only committed turns in Past, got %#v", result.History.Past)
	}
	if result.History.Current == nil || result.History.Current.ID != turnID {
		t.Fatalf("expected current turn %q in History.Current, got %#v", turnID, result.History.Current)
	}
	if len(result.History.Messages) != 1 {
		t.Fatalf("expected legacy Messages to contain only past-turn messages, got %#v", result.History.Messages)
	}
}

func ptrBytes(value []byte) *[]byte {
	return &value
}

func gzipString(t *testing.T, value string) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte(value)); err != nil {
		t.Fatalf("gzip write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close failed: %v", err)
	}
	return buffer.String()
}
