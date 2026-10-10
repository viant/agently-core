package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/sdk/api"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCanonicalSDKObserverPreservesActualEventsAndCurrentAssistant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ag-ui/run" || r.Method != "POST" {
			t.Errorf("unsupported path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-fixture" {
			t.Error("bearer forwarding lost")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "id: 1\ndata: {\"type\":\"RUN_STARTED\",\"threadId\":\"wire\",\"runId\":\"run\",\"metadata\":{\"agently\":{\"identityVersion\":\"1\",\"nativeTurnId\":\"native-turn\"}}}\n\n")
		io.WriteString(w, "id: 2\ndata: {\"type\":\"MESSAGES_SNAPSHOT\",\"messages\":[{\"id\":\"old\",\"role\":\"assistant\",\"content\":\"old reply must not be collected\"}]}\n\n")
		io.WriteString(w, "id: 3\ndata: {\"type\":\"TEXT_MESSAGE_START\",\"messageId\":\"current\",\"role\":\"assistant\"}\n\n")
		io.WriteString(w, "id: 4\ndata: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"messageId\":\"current\",\"delta\":\"actual-current-text\"}\n\n")
		io.WriteString(w, "id: 5\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":\"wire\",\"runId\":\"run\",\"outcome\":{\"type\":\"success\"}}\n\n")
	}))
	defer server.Close()
	observed := &observer{next: http.DefaultTransport}
	client, err := newClient(server.URL, "test-fixture", observed)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.RunAGUI(context.Background(), &agui.RunAgentInput{ThreadID: "wire", RunID: "run", Messages: []agui.Message{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	events := []json.RawMessage{}
	actual, err := sdk.CollectAGUI(stream, "native-conversation", func(e sdk.AGUIEvent) error { events = append(events, e.Data); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if actual.Content != "actual-current-text" || actual.TurnID != "native-turn" || actual.Outcome.Type != "success" || actual.LastEventID != "5" || len(events) != 5 {
		t.Fatalf("actual canonical evidence lost: %+v", actual)
	}
	requests := observed.observed()
	if len(requests) != 1 || requests[0].Path != "/v1/ag-ui/run" || requests[0].StatusCode != 200 {
		t.Fatal(requests)
	}
}
func TestProbeRejectsLegacyIdentityAndExternalTargetWithoutDispatch(t *testing.T) {
	h := &handler{runs: map[string]*observedRun{}}
	rec := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/run", strings.NewReader(`{"target":"https://external.invalid","probeId":"test","query":{"agentId":"simple","query":"hi"}}`))
	h.ServeHTTP(rec, request)
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
}

func TestAwaitNativeExactTurnNonterminalTerminalAndDeadline(t *testing.T) {
	t.Run("nonterminal then exact terminal", func(t *testing.T) {
		count := 0
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		turn, err := awaitExactTurn(ctx, "owned", time.Millisecond, func(context.Context) (*sdk.ConversationStateResponse, error) {
			count++
			status := api.TurnStatusRunning
			if count == 2 {
				status = api.TurnStatusCompleted
			}
			return &sdk.ConversationStateResponse{Conversation: &api.ConversationState{Turns: []*api.TurnState{{TurnID: "owned", Status: status}}}}, nil
		})
		if err != nil || count != 2 || turn.TurnID != "owned" || turn.Status != api.TurnStatusCompleted {
			t.Fatal("exact terminal observation failed")
		}
	})
	t.Run("foreign terminal cannot satisfy original", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		turn, err := awaitExactTurn(ctx, "owned", time.Millisecond, func(context.Context) (*sdk.ConversationStateResponse, error) {
			return &sdk.ConversationStateResponse{Conversation: &api.ConversationState{Turns: []*api.TurnState{{TurnID: "foreign", Status: api.TurnStatusCompleted}, {TurnID: "owned", Status: api.TurnStatusRunning}}}}, nil
		})
		if turn != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("foreign terminal incorrectly accepted")
		}
	})
	t.Run("expired budget reads nothing", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		count := 0
		_, err := awaitExactTurn(ctx, "owned", time.Millisecond, func(context.Context) (*sdk.ConversationStateResponse, error) { count++; return nil, nil })
		if count != 0 || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("expired original budget extended")
		}
	})
	t.Run("failed original remains failed", func(t *testing.T) {
		turn, err := awaitExactTurn(context.Background(), "owned", time.Millisecond, func(context.Context) (*sdk.ConversationStateResponse, error) {
			return &sdk.ConversationStateResponse{Conversation: &api.ConversationState{Turns: []*api.TurnState{{TurnID: "owned", Status: api.TurnStatusFailed}}}}, nil
		})
		if err != nil || turn.Status != api.TurnStatusFailed {
			t.Fatal("failed turn converted to success")
		}
	})
}

func TestObservationBudgetExpiryIsNotPermissionDenial(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if !observationBudgetExpired(ctx, context.DeadlineExceeded) {
		t.Fatal("expired observation must be timeout")
	}
	if observationBudgetExpired(context.Background(), errors.New("HTTP403 forbidden")) {
		t.Fatal("actual permission denial must remain permission denial")
	}
	if observationBudgetExpired(context.Background(), errors.New("HTTP401 unauthorized")) {
		t.Fatal("actual authentication denial must remain denial")
	}
}
