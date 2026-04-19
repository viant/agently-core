package callback

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viant/afs"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/tool"
	callbackrepo "github.com/viant/agently-core/workspace/repository/callback"
)

// stubRegistry captures Execute calls for assertions.
type stubRegistry struct {
	lastName string
	lastArgs map[string]interface{}
	result   string
	err      error
}

func (s *stubRegistry) Definitions() []llm.ToolDefinition { return nil }
func (s *stubRegistry) MatchDefinition(string) []*llm.ToolDefinition {
	return nil
}
func (s *stubRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) { return nil, false }
func (s *stubRegistry) MustHaveTools([]string) ([]llm.Tool, error)       { return nil, nil }
func (s *stubRegistry) Execute(_ context.Context, name string, args map[string]interface{}) (string, error) {
	s.lastName = name
	s.lastArgs = args
	return s.result, s.err
}
func (s *stubRegistry) SetDebugLogger(io.Writer)   {}
func (s *stubRegistry) Initialize(context.Context) {}

var _ tool.Registry = (*stubRegistry)(nil)

func newTestService(t *testing.T) (*Service, *stubRegistry) {
	t.Helper()
	// Point the workspace root at our testdata dir so the repo loads fixtures.
	abs, err := filepath.Abs(filepath.Join("..", "..", "workspace", "repository", "callback", "testdata"))
	if err != nil {
		t.Fatalf("abs testdata: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(abs, "callbacks")); statErr != nil {
		t.Fatalf("testdata/callbacks missing: %v", statErr)
	}
	t.Setenv("AGENTLY_WORKSPACE", abs)

	repo := callbackrepo.New(afs.New())
	stub := &stubRegistry{result: `{"ok":true}`}
	return New(repo, stub), stub
}

// TestDispatch_SimpleEcho verifies basic event lookup + placeholder rendering.
func TestDispatch_SimpleEcho(t *testing.T) {
	svc, stub := newTestService(t)

	out, err := svc.Dispatch(context.Background(), &DispatchInput{
		EventName:      "simple_echo",
		ConversationID: "conv-123",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if out.EventName != "simple_echo" {
		t.Errorf("expected eventName echo, got %q", out.EventName)
	}
	if stub.lastName != "test-echo" {
		t.Errorf("expected tool test-echo, got %q", stub.lastName)
	}
	if got, want := stub.lastArgs["event"], "simple_echo"; got != want {
		t.Errorf("args.event = %v; want %v", got, want)
	}
	if got, want := stub.lastArgs["convo"], "conv-123"; got != want {
		t.Errorf("args.convo = %v; want %v", got, want)
	}
}

// TestDispatch_SpoPayloadRendering covers the richer template: {{.today}},
// {{default}}, {{json .selectedRows}}, nested JSON assembly.
func TestDispatch_SpoPayloadRendering(t *testing.T) {
	svc, stub := newTestService(t)

	selectedRows := []interface{}{
		map[string]interface{}{"site_id": 1001, "action": "CUT"},
		map[string]interface{}{"site_id": 1002, "action": "TEST"},
	}

	out, err := svc.Dispatch(context.Background(), &DispatchInput{
		EventName:      "spo_planner_submit",
		ConversationID: "conv-xyz",
		TurnID:         "turn-42",
		Payload: map[string]interface{}{
			"selectedRows": selectedRows,
		},
		Context: map[string]interface{}{
			"agencyId": float64(5337),
		},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if stub.lastName != "steward-SaveRecommendation" {
		t.Fatalf("expected tool steward-SaveRecommendation, got %q", stub.lastName)
	}
	body, ok := stub.lastArgs["Body"].(map[string]interface{})
	if !ok {
		t.Fatalf("args.Body is not a map: %T %v", stub.lastArgs["Body"], stub.lastArgs["Body"])
	}
	if got, want := body["agency_id"], float64(5337); got != want {
		t.Errorf("Body.agency_id = %v; want %v", got, want)
	}
	if got, want := body["conversation_id"], "conv-xyz"; got != want {
		t.Errorf("Body.conversation_id = %v; want %v", got, want)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if id, _ := body["id"].(string); !strings.HasPrefix(id, "rec-5337-spo-"+today) {
		t.Errorf("Body.id unexpected shape: %q (expected prefix rec-5337-spo-%s)", id, today)
	}
	// supporting_metrics is the json-encoded selectedRows array — same shape back.
	sm, smOk := body["supporting_metrics"].([]interface{})
	if !smOk {
		t.Fatalf("Body.supporting_metrics is not an array: %T %v", body["supporting_metrics"], body["supporting_metrics"])
	}
	if len(sm) != 2 {
		t.Errorf("supporting_metrics len = %d; want 2", len(sm))
	}
	got, _ := json.Marshal(sm)
	want, _ := json.Marshal(selectedRows)
	if string(got) != string(want) {
		t.Errorf("supporting_metrics = %s; want %s", got, want)
	}
	out = out // suppress unused var
}

// TestDispatch_UnknownEvent returns an error without invoking any tool.
func TestDispatch_UnknownEvent(t *testing.T) {
	svc, stub := newTestService(t)

	_, err := svc.Dispatch(context.Background(), &DispatchInput{
		EventName: "no_such_event",
	})
	if err == nil {
		t.Fatal("expected unknown-event error, got nil")
	}
	if stub.lastName != "" {
		t.Errorf("unexpected tool invocation for unknown event: %q", stub.lastName)
	}
}

// TestDispatch_ReservedKeyShadowing ensures a Context key like `eventName`
// cannot override the dispatcher-set value.
func TestDispatch_ReservedKeyShadowing(t *testing.T) {
	svc, stub := newTestService(t)

	_, err := svc.Dispatch(context.Background(), &DispatchInput{
		EventName: "simple_echo",
		Context: map[string]interface{}{
			"eventName":      "hijack", // must be dropped
			"conversationId": "hijack", // must be dropped
		},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got := stub.lastArgs["event"]; got != "simple_echo" {
		t.Errorf("reserved key shadowing failed — args.event = %v", got)
	}
}

// TestDispatch_EmptyEventName is rejected at the entry point.
func TestDispatch_EmptyEventName(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Dispatch(context.Background(), &DispatchInput{})
	if err == nil {
		t.Fatal("expected error for empty eventName")
	}
}
