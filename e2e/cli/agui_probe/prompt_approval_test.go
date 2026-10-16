package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	messagemodel "github.com/viant/agently-core/model/message"
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/sdk/api"
)

func promptFixture() (*sdk.ConversationStateResponse, identity, *sdk.PendingElicitation, approvalTuple) {
	actual := identity{ConversationID: "conversation", TurnID: "turn"}
	pending := &sdk.PendingElicitation{ConversationID: "conversation", ElicitationID: "prompt", MessageID: "model-parent"}
	tuple := approvalTuple{ApprovalID: "prompt", ConversationID: "conversation", TurnID: "turn", QueueParentMessageID: "model-parent", OperationID: "operation", MessageID: "effect", RequestID: "request-original"}
	snapshot := &sdk.ConversationStateResponse{Conversation: &api.ConversationState{ConversationID: "conversation", Turns: []*api.TurnState{{TurnID: "turn", Elicitation: &api.ElicitationState{ElicitationID: "prompt", Status: api.ElicitationStatusPending}, Execution: &api.ExecutionState{Pages: []*api.ExecutionPageState{{ToolSteps: []*api.ToolStepState{{ParentMessageID: "model-parent", ToolCallID: "operation", ToolMessageID: "effect", RequestPayloadID: "request-original"}}}}}}}}}
	return snapshot, actual, pending, tuple
}
func TestPromptApprovalPinsOriginalEffectAndRejectsUnprovenChains(t *testing.T) {
	snapshot, actual, pending, tuple := promptFixture()
	if _, _, err := promptOriginal(snapshot, actual, pending); err != nil {
		t.Fatal(err)
	}
	root := snapshot.Conversation.Turns[0]
	root.Status = api.TurnStatusCompleted
	root.Elicitation.Status = api.ElicitationStatusAccepted
	root.Elicitation.ResponsePayload = json.RawMessage(`{"editedFields":{"names":["HOME","PATH"]}}`)
	step := root.Execution.Pages[0].ToolSteps[0]
	step.Status = "completed"
	step.ResponsePayloadID = "response-original"
	if _, _, err := promptCompleted(snapshot, tuple); err != nil {
		t.Fatal(err)
	}
	mutants := []struct {
		name   string
		change func(*sdk.ConversationStateResponse)
	}{
		{"foreignConversation", func(s *sdk.ConversationStateResponse) { s.Conversation.ConversationID = "foreign" }},
		{"foreignTurn", func(s *sdk.ConversationStateResponse) { s.Conversation.Turns[0].TurnID = "foreign" }},
		{"wrongElicitation", func(s *sdk.ConversationStateResponse) { s.Conversation.Turns[0].Elicitation.ElicitationID = "other" }},
		{"unaccepted", func(s *sdk.ConversationStateResponse) {
			s.Conversation.Turns[0].Elicitation.Status = api.ElicitationStatusDeclined
		}},
		{"missingEffect", func(s *sdk.ConversationStateResponse) { s.Conversation.Turns[0].Execution.Pages[0].ToolSteps = nil }},
		{"duplicateSameEffect", func(s *sdk.ConversationStateResponse) {
			p := s.Conversation.Turns[0].Execution.Pages[0]
			p.ToolSteps = append(p.ToolSteps, p.ToolSteps[0])
		}},
		{"differentOperation", func(s *sdk.ConversationStateResponse) {
			s.Conversation.Turns[0].Execution.Pages[0].ToolSteps[0].ToolCallID = "other"
		}},
		{"differentMessage", func(s *sdk.ConversationStateResponse) {
			s.Conversation.Turns[0].Execution.Pages[0].ToolSteps[0].ToolMessageID = "other"
		}},
		{"requestDrift", func(s *sdk.ConversationStateResponse) {
			s.Conversation.Turns[0].Execution.Pages[0].ToolSteps[0].RequestPayloadID = "other"
		}},
		{"responseMissing", func(s *sdk.ConversationStateResponse) {
			s.Conversation.Turns[0].Execution.Pages[0].ToolSteps[0].ResponsePayloadID = ""
		}},
	}
	raw, _ := json.Marshal(snapshot)
	for _, tc := range mutants {
		t.Run(tc.name, func(t *testing.T) {
			var changed sdk.ConversationStateResponse
			_ = json.Unmarshal(raw, &changed)
			tc.change(&changed)
			if _, _, err := promptCompleted(&changed, tuple); err == nil {
				t.Fatal("unproven original effect accepted")
			}
		})
	}
}
func TestPromptReceiptProjectionNeverExposesEnvironmentValues(t *testing.T) {
	actual := map[string]any{"values": map[string]any{"HOME": "private-fixture-value", "PATH": "private-fixture-value", "unknown": "private-fixture-value"}}
	raw, _ := json.Marshal(promptResultKeys(actual))
	var projected map[string]any
	_ = json.Unmarshal(raw, &projected)
	if projected["valueKeyCount"] != float64(3) {
		t.Fatal("total cardinality lost")
	}
	keys := projected["values"].(map[string]any)
	if len(keys) != 2 || keys["HOME"] != true || keys["PATH"] != true {
		t.Fatal("known presence projection differs")
	}
	if strings.Contains(string(raw), "private-fixture-value") || strings.Contains(string(raw), "unknown") {
		t.Fatal("private value or unknown field leaked")
	}
	if string(raw) == "" {
		t.Fatal("empty projection")
	}
	if promptResultKeys(map[string]any{}) != nil {
		t.Fatal("missing values shape accepted")
	}
}

func TestPromptReceiptUsesOriginalSDKHTTPPayloadsAndSameOwner(t *testing.T) {
	snapshot, actual, pending, _ := promptFixture()
	original := `{"names":["HOME","SHELL","PATH"]}`
	acceptedBody := `{"editedFields":{"names":["HOME","PATH"]}}`
	acceptedPayloadID := "accepted-payload"
	acceptedMessageID := "accepted-user-message"
	pageHasMore := false
	result := `{"values":{"HOME":"must-not-appear-in-probe-output","PATH":"must-not-appear-in-probe-output"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture-owner" {
			t.Error("unexpected request authority or mutation")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/transcript"):
			if r.URL.Query().Get("includeToolCalls") != "true" {
				t.Error("tool transcript not requested")
			}
			_ = json.NewEncoder(w).Encode(snapshot)
		case strings.Contains(r.URL.Path, "/elicitations"):
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []any{pending}})
		case strings.Contains(r.URL.Path, "/messages"):
			eid, parent, turn := pending.ElicitationID, pending.MessageID, actual.TurnID
			_ = json.NewEncoder(w).Encode(&sdk.MessagePage{HasMore: pageHasMore, Rows: []*messagemodel.MessageRowsView{{Id: acceptedMessageID, ConversationId: actual.ConversationID, TurnId: &turn, ParentMessageId: &parent, ElicitationId: &eid, ElicitationPayloadId: &acceptedPayloadID, Role: "user", Type: "elicitation_response"}}})
		case strings.Contains(r.URL.Path, "/payload/accepted"):
			_, _ = io.WriteString(w, acceptedBody)
		case strings.HasSuffix(r.URL.Path, "/payload/request-original"):
			_, _ = io.WriteString(w, original)
		case strings.HasSuffix(r.URL.Path, "/payload/response-original"):
			_, _ = io.WriteString(w, result)
		case strings.HasSuffix(r.URL.Path, "/conversations/conversation"):
			_, _ = io.WriteString(w, `{"id":"conversation"}`)
		default:
			t.Errorf("unexpected SDK route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	h := &handler{runs: map[string]*observedRun{"probe": {identity: actual, target: upstream.URL, ownerHash: digest("fixture-owner")}}}
	request := func(path, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("/prompt-approval-capture/probe?id=prompt", "other-owner"); w.Code != 404 {
		t.Fatal("foreign owner read original tuple")
	}
	if w := request("/prompt-approval-capture/probe?id=prompt", "fixture-owner"); w.Code != 200 {
		t.Fatalf("original capture status %d: %s", w.Code, w.Body.String())
	}
	root := snapshot.Conversation.Turns[0]
	root.Status = api.TurnStatusCompleted
	root.Elicitation.Status = api.ElicitationStatusAccepted
	root.Elicitation.ResponsePayload = json.RawMessage(`{"editedFields":{"names":["HOME","PATH"]}}`)
	step := root.Execution.Pages[0].ToolSteps[0]
	step.Status = "completed"
	step.ResponsePayloadID = "response-original"
	w := request("/prompt-approval-receipt/probe?id=prompt", "fixture-owner")
	if w.Code != 200 {
		t.Fatalf("receipt status %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "must-not-appear") {
		t.Fatal("environment value exposed")
	}
	var receipt map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	if receipt["effectCount"] != float64(1) || receipt["sameOwner"] != true {
		t.Fatal("original effect receipt differs")
	}
	// Canonical persisted transcript does not expose this response body.
	root.Elicitation.ResponsePayload = nil
	if w := request("/prompt-approval-receipt/probe?id=prompt", "fixture-owner"); w.Code != 200 {
		t.Fatal("actual linked response was not used")
	}
	for _, bad := range []string{`null`, `{}`, `{"editedFields":{"names":[]}}`} {
		acceptedBody = bad
		if w := request("/prompt-approval-receipt/probe?id=prompt", "fixture-owner"); w.Code != 409 {
			t.Fatal("missing actual accepted edit evidence accepted")
		}
	}
	acceptedBody = `{"editedFields":{"names":["HOME","PATH"]}}`
	for _, mutate := range []func(){func() { acceptedPayloadID = "accepted-drift" }, func() { acceptedMessageID = "accepted-user-drift" }, func() { acceptedBody = `{"editedFields":{"names":["PATH","HOME"]}}` }, func() { pageHasMore = true }} {
		mutate()
		if w := request("/prompt-approval-receipt/probe?id=prompt", "fixture-owner"); w.Code != 409 {
			t.Fatal("accepted response identity/body drift or incomplete page accepted")
		}
		acceptedPayloadID = "accepted-payload"
		acceptedMessageID = "accepted-user-message"
		acceptedBody = `{"editedFields":{"names":["HOME","PATH"]}}`
		pageHasMore = false
	}
	if w := request("/prompt-approval-receipt/probe?id=foreign", "fixture-owner"); w.Code != 409 {
		t.Fatal("uncaptured foreign tuple replay accepted")
	}
	page := root.Execution.Pages[0]
	page.ToolSteps = append(page.ToolSteps, step)
	if w := request("/prompt-approval-receipt/probe?id=prompt", "fixture-owner"); w.Code != 409 {
		t.Fatal("duplicate effect accepted")
	}
	page.ToolSteps = page.ToolSteps[:1]
	// A changed original persisted request is never replaced with expected fixture names.
	original = `{"names":["HOME","PATH"]}`
	if w := request("/prompt-approval-receipt/probe?id=prompt", "fixture-owner"); w.Code != 409 {
		t.Fatal("original request drift accepted")
	}
}

func TestOriginalPromptNamesAreASetWithoutMutatingOriginalOrder(t *testing.T) {
	for _, names := range [][]any{{"HOME", "SHELL", "PATH"}, {"PATH", "HOME", "SHELL"}, {"SHELL", "PATH", "HOME"}} {
		args := map[string]any{"names": names}
		before, _ := json.Marshal(args)
		projected := promptOriginalNameSet(args).(map[string]any)
		after, _ := json.Marshal(args)
		if string(before) != string(after) {
			t.Fatal("projection mutated original immutable request")
		}
		if projected["nameCount"] != 3 || len(projected["names"].(map[string]any)) != 3 {
			t.Fatal("original complete set lost")
		}
	}
	for _, names := range [][]any{{"HOME", "HOME", "PATH"}, {"HOME", "PATH", "UNKNOWN"}, {"HOME", "PATH"}} {
		projected := promptOriginalNameSet(map[string]any{"names": names}).(map[string]any)
		if projected["nameCount"] == 3 && len(projected["names"].(map[string]any)) == 3 {
			t.Fatal("incomplete or duplicate set falsely projected as complete")
		}
	}
	if promptOriginalNameSet(map[string]any{"names": []any{"HOME", 7, "PATH"}}) != nil {
		t.Fatal("nonstring name shape accepted")
	}
}

func TestPromptAcceptedMessageRequiresExactLinkedUniqueResponse(t *testing.T) {
	_, _, _, tuple := promptFixture()
	id, turn, parent, eid, payload := "accepted", tuple.TurnID, tuple.QueueParentMessageID, tuple.ApprovalID, "accepted-payload"
	good := &sdk.MessagePage{Rows: []*messagemodel.MessageRowsView{{Id: id, ConversationId: tuple.ConversationID, TurnId: &turn, ParentMessageId: &parent, ElicitationId: &eid, ElicitationPayloadId: &payload, Role: "user", Type: "elicitation_response"}}}
	if mid, pid, err := promptAcceptedMessage(good, tuple); err != nil || mid != id || pid != payload {
		t.Fatal("exact accepted response rejected")
	}
	raw, _ := json.Marshal(good)
	mutants := []struct {
		name   string
		mutate func(*sdk.MessagePage)
	}{
		{"incompletePage", func(p *sdk.MessagePage) { p.HasMore = true }},
		{"missing", func(p *sdk.MessagePage) { p.Rows = nil }},
		{"duplicate", func(p *sdk.MessagePage) { p.Rows = append(p.Rows, p.Rows[0]) }},
		{"foreignConversation", func(p *sdk.MessagePage) { p.Rows[0].ConversationId = "foreign" }},
		{"foreignTurn", func(p *sdk.MessagePage) { x := "foreign"; p.Rows[0].TurnId = &x }},
		{"wrongParent", func(p *sdk.MessagePage) { x := "foreign"; p.Rows[0].ParentMessageId = &x }},
		{"wrongElicitation", func(p *sdk.MessagePage) { x := "foreign"; p.Rows[0].ElicitationId = &x }},
		{"wrongRole", func(p *sdk.MessagePage) { p.Rows[0].Role = "assistant" }},
		{"wrongType", func(p *sdk.MessagePage) { p.Rows[0].Type = "text" }},
		{"missingPayload", func(p *sdk.MessagePage) { p.Rows[0].ElicitationPayloadId = nil }},
	}
	for _, tc := range mutants {
		t.Run(tc.name, func(t *testing.T) {
			var changed sdk.MessagePage
			_ = json.Unmarshal(raw, &changed)
			tc.mutate(&changed)
			if _, _, err := promptAcceptedMessage(&changed, tuple); err == nil {
				t.Fatal("unproven accepted response accepted")
			}
		})
	}
}
