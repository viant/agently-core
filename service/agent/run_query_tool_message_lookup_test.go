package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

type toolMessageLookupDataService struct {
	data.Service
	calls  int
	result map[string]string
	err    error
}

func (s *toolMessageLookupDataService) GetToolMessageIDsByTurn(context.Context, string, string) (map[string]string, error) {
	s.calls++
	return s.result, s.err
}

type dataServiceWithoutToolMessageLookup struct {
	data.Service
}

type toolMessageLookupConversationClient struct {
	apiconv.Client
	calls       int
	requestedID string
	input       apiconv.Input
	result      *apiconv.Conversation
	err         error
}

func (c *toolMessageLookupConversationClient) GetConversation(_ context.Context, id string, options ...apiconv.Option) (*apiconv.Conversation, error) {
	c.calls++
	c.requestedID = id
	c.input = apiconv.Input{}
	for _, option := range options {
		if option != nil {
			option(&c.input)
		}
	}
	return c.result, c.err
}

func TestService_LoadPersistedToolMessageIDs_PrimaryCapabilityOnceForMultipleResults(t *testing.T) {
	primary := &toolMessageLookupDataService{
		result: map[string]string{
			" op-a ": " message-a ",
			"op-b":   "message-b",
		},
	}
	conversation := &toolMessageLookupConversationClient{err: errors.New("fallback must not be called")}
	service := &Service{dataService: primary, conversation: conversation}

	got, err := service.loadPersistedToolMessageIDs(context.Background(), " conv-1 ", " turn-1 ")
	if err != nil {
		t.Fatalf("loadPersistedToolMessageIDs() error: %v", err)
	}
	want := map[string]string{"op-a": "message-a", "op-b": "message-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadPersistedToolMessageIDs() = %#v, want %#v", got, want)
	}
	if got["op-a"] == "" || got["op-b"] == "" {
		t.Fatalf("expected both remembered results to use the single loaded map: %#v", got)
	}
	if primary.calls != 1 {
		t.Fatalf("primary capability calls = %d, want 1", primary.calls)
	}
	if conversation.calls != 0 {
		t.Fatalf("fallback calls = %d, want 0", conversation.calls)
	}
}

func TestService_LoadPersistedToolMessageIDs_SuccessfulEmptyPrimaryDoesNotFallback(t *testing.T) {
	primary := &toolMessageLookupDataService{result: map[string]string{}}
	conversation := &toolMessageLookupConversationClient{err: errors.New("fallback must not be called")}
	service := &Service{dataService: primary, conversation: conversation}

	got, err := service.loadPersistedToolMessageIDs(context.Background(), "conv-1", "turn-1")
	if err != nil {
		t.Fatalf("loadPersistedToolMessageIDs() error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("loadPersistedToolMessageIDs() = %#v, want empty", got)
	}
	if primary.calls != 1 || conversation.calls != 0 {
		t.Fatalf("calls primary=%d fallback=%d, want 1 and 0", primary.calls, conversation.calls)
	}
}

func TestService_LoadPersistedToolMessageIDs_MissingCapabilityFallsBackOnceWithExactScope(t *testing.T) {
	conversation := &toolMessageLookupConversationClient{result: fallbackToolMessageConversation()}
	service := &Service{
		dataService:  &dataServiceWithoutToolMessageLookup{},
		conversation: conversation,
	}

	got, err := service.loadPersistedToolMessageIDs(context.Background(), " conv-1 ", " turn-1 ")
	if err != nil {
		t.Fatalf("loadPersistedToolMessageIDs() error: %v", err)
	}
	want := map[string]string{
		"op-dupe":   "child-high-z",
		"op-other":  "child-other",
		"op-parent": "child-parent",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadPersistedToolMessageIDs() = %#v, want %#v", got, want)
	}
	assertToolMessageFallbackRequest(t, conversation)
}

func TestService_LoadPersistedToolMessageIDs_FallbackRejectsDifferentConversation(t *testing.T) {
	wrongConversation := fallbackToolMessageConversation()
	wrongConversation.Id = "conv-other"
	conversation := &toolMessageLookupConversationClient{result: wrongConversation}
	service := &Service{
		dataService:  &dataServiceWithoutToolMessageLookup{},
		conversation: conversation,
	}

	got, err := service.loadPersistedToolMessageIDs(context.Background(), "conv-1", "turn-1")
	if err != nil {
		t.Fatalf("loadPersistedToolMessageIDs() error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("different conversation result = %#v, want empty", got)
	}
	assertToolMessageFallbackRequest(t, conversation)
}

func TestService_LoadPersistedToolMessageIDs_PrimaryErrorFallsBackOnce(t *testing.T) {
	primary := &toolMessageLookupDataService{err: errors.New("primary unavailable")}
	conversation := &toolMessageLookupConversationClient{result: fallbackToolMessageConversation()}
	service := &Service{dataService: primary, conversation: conversation}

	got, err := service.loadPersistedToolMessageIDs(context.Background(), "conv-1", "turn-1")
	if err == nil {
		t.Fatal("expected the primary lookup failure to be reported")
	}
	if got["op-dupe"] != "child-high-z" {
		t.Fatalf("fallback result was not preserved after primary error: %#v", got)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}
	assertToolMessageFallbackRequest(t, conversation)
}

func TestService_LoadPersistedToolMessageIDs_CanceledPrimarySkipsFallback(t *testing.T) {
	primary := &toolMessageLookupDataService{err: errors.New("primary unavailable")}
	conversation := &toolMessageLookupConversationClient{result: fallbackToolMessageConversation()}
	service := &Service{dataService: primary, conversation: conversation}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := service.loadPersistedToolMessageIDs(ctx, "conv-1", "turn-1")
	if err == nil {
		t.Fatal("expected canceled primary lookup to return an error")
	}
	if len(got) != 0 {
		t.Fatalf("canceled lookup = %#v, want empty map", got)
	}
	if conversation.calls != 0 {
		t.Fatalf("fallback calls = %d, want 0 after cancellation", conversation.calls)
	}
}

func TestService_LoadPersistedToolMessageIDs_BothLookupsFailReturnsEmpty(t *testing.T) {
	primary := &toolMessageLookupDataService{err: errors.New("primary unavailable")}
	conversation := &toolMessageLookupConversationClient{err: errors.New("fallback unavailable")}
	service := &Service{dataService: primary, conversation: conversation}

	got, err := service.loadPersistedToolMessageIDs(context.Background(), "conv-1", "turn-1")
	if err == nil {
		t.Fatal("expected combined lookup error")
	}
	if len(got) != 0 {
		t.Fatalf("failed lookups = %#v, want empty map so replay is preserved", got)
	}
	if primary.calls != 1 || conversation.calls != 1 {
		t.Fatalf("calls primary=%d fallback=%d, want 1 each", primary.calls, conversation.calls)
	}
}

func assertToolMessageFallbackRequest(t *testing.T, client *toolMessageLookupConversationClient) {
	t.Helper()
	if client.calls != 1 {
		t.Fatalf("fallback calls = %d, want 1", client.calls)
	}
	if client.requestedID != "conv-1" {
		t.Fatalf("fallback conversation id = %q, want conv-1", client.requestedID)
	}
	if client.input.Since != "turn-1" || client.input.Has == nil || !client.input.Has.Since {
		t.Fatalf("fallback Since option = %#v, want turn-1 with marker", client.input)
	}
	if !client.input.IncludeTranscript || !client.input.Has.IncludeTranscript {
		t.Fatalf("fallback IncludeTranscript option = %#v, want true with marker", client.input)
	}
	if !client.input.IncludeToolCall || !client.input.Has.IncludeToolCall {
		t.Fatalf("fallback IncludeToolCall option = %#v, want true with marker", client.input)
	}
}

func fallbackToolMessageConversation() *apiconv.Conversation {
	turnID := "turn-1"
	otherTurnID := "turn-2"
	return &apiconv.Conversation{
		Id: "conv-1",
		Transcript: []*agconv.TranscriptView{
			{
				Id:             turnID,
				ConversationId: "conv-1",
				Message: []*agconv.MessageView{
					{
						Id:             "parent-normal",
						ConversationId: "conv-1",
						TurnId:         toolMessageLookupStringPtr(turnID),
						Type:           "text",
						ToolMessage: []*agconv.ToolMessageView{
							{Id: "child-low", ToolCall: fallbackToolCall(" op-dupe ", turnID, 1)},
							{Id: "child-high-a", ToolCall: fallbackToolCall("op-dupe", turnID, 3)},
							{Id: " child-high-z ", ToolCall: fallbackToolCall("op-dupe", turnID, 3)},
							{Id: " child-other ", ToolCall: fallbackToolCall(" op-other ", turnID, 1)},
							{Id: "child-wrong-tool-turn", ToolCall: fallbackToolCall("op-wrong-tool-turn", otherTurnID, 9)},
						},
					},
					{
						Id:             " parent-tool ",
						ConversationId: "conv-1",
						TurnId:         toolMessageLookupStringPtr(turnID),
						Type:           " TOOL_OP ",
						ToolMessage: []*agconv.ToolMessageView{
							{Id: "child-parent", ToolCall: fallbackToolCall(" op-parent ", turnID, 1)},
						},
					},
					{
						Id:             "wrong-conversation-message",
						ConversationId: "conv-other",
						TurnId:         toolMessageLookupStringPtr(turnID),
						Type:           "text",
						ToolMessage: []*agconv.ToolMessageView{
							{Id: "child-wrong-conversation", ToolCall: fallbackToolCall("op-wrong-conversation", turnID, 9)},
						},
					},
					{
						Id:             "wrong-turn-message",
						ConversationId: "conv-1",
						TurnId:         toolMessageLookupStringPtr(otherTurnID),
						Type:           "text",
						ToolMessage: []*agconv.ToolMessageView{
							{Id: "child-wrong-turn", ToolCall: fallbackToolCall("op-wrong-turn", turnID, 9)},
						},
					},
				},
			},
			{
				Id:             otherTurnID,
				ConversationId: "conv-1",
				Message: []*agconv.MessageView{
					{
						Id:             "later-parent",
						ConversationId: "conv-1",
						TurnId:         toolMessageLookupStringPtr(otherTurnID),
						Type:           "text",
						ToolMessage: []*agconv.ToolMessageView{
							{Id: "later-child", ToolCall: fallbackToolCall("op-later", otherTurnID, 10)},
						},
					},
				},
			},
			{
				Id:             turnID,
				ConversationId: "conv-other",
				Message: []*agconv.MessageView{
					{
						Id:             "wrong-turn-conversation-parent",
						ConversationId: "conv-other",
						TurnId:         toolMessageLookupStringPtr(turnID),
						Type:           "text",
						ToolMessage: []*agconv.ToolMessageView{
							{Id: "wrong-turn-conversation-child", ToolCall: fallbackToolCall("op-wrong-turn-conversation", turnID, 10)},
						},
					},
				},
			},
		},
	}
}

func fallbackToolCall(opID, turnID string, attempt int) *agconv.ToolCallView {
	return &agconv.ToolCallView{
		OpId:    opID,
		TurnId:  toolMessageLookupStringPtr(turnID),
		Attempt: attempt,
	}
}

func toolMessageLookupStringPtr(value string) *string {
	return &value
}
