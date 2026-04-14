package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"testing"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/prompt"
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
			if history.Past[0].Messages[0].Kind != prompt.MessageKindChatUser {
				t.Fatalf("expected first message to remain user chat, got %v", history.Past[0].Messages[0].Kind)
			}
			if history.Past[0].Messages[1].Kind != prompt.MessageKindToolResult {
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
	if history.Past[0].Messages[1].Kind != prompt.MessageKindToolResult {
		t.Fatalf("expected second message to be tool result, got %v", history.Past[0].Messages[1].Kind)
	}
	if got := history.Past[0].Messages[2].ToolName; got != "llm/agents/run" {
		t.Fatalf("expected preserved child tool result to be llm/agents/run, got %q", got)
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
