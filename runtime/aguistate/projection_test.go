package aguistate

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSharedUpstreamConformanceProjection(t *testing.T) {
	data, err := os.ReadFile("../../protocol/agui/testdata/conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Input struct {
			State    json.RawMessage `json:"state"`
			Messages json.RawMessage `json:"messages"`
		} `json:"input"`
		Events                  []json.RawMessage `json:"events"`
		ExpectedMessages        json.RawMessage   `json:"expectedMessages"`
		ExpectedState           json.RawMessage   `json:"expectedState"`
		ExpectedNormalizedTypes []string          `json:"expectedNormalizedTypes"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	p, err := New(fixture.Input.State, fixture.Input.Messages)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for i, event := range fixture.Events {
		events, err := p.Apply(event)
		if err != nil {
			t.Fatalf("event %d %s: %v", i, event, err)
		}
		for _, normalized := range events {
			var e Object
			_ = json.Unmarshal(normalized, &e)
			kinds = append(kinds, field(e, "type"))
		}
	}
	if !reflect.DeepEqual(kinds, fixture.ExpectedNormalizedTypes) {
		t.Errorf("normalization mismatch got=%v want=%v", kinds, fixture.ExpectedNormalizedTypes)
	}
	state, messages, err := p.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2]json.RawMessage{"state": {state, fixture.ExpectedState}, "messages": {messages, fixture.ExpectedMessages}} {
		var actual, expected any
		_ = decode(pair[0], &actual)
		_ = decode(pair[1], &expected)
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("%s mismatch\ngot=%s\nwant=%s", name, pair[0], pair[1])
		}
	}
}

func apply(t *testing.T, p *Projection, events ...string) {
	t.Helper()
	for _, e := range events {
		if _, err := p.Apply([]byte(e)); err != nil {
			t.Fatalf("%s: %v", e, err)
		}
	}
}
func started(t *testing.T) *Projection {
	t.Helper()
	p, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, p, `{"type":"RUN_STARTED","threadId":"t","runId":"r"}`)
	return p
}

func TestLosslessMessagesAndToolResultOrdering(t *testing.T) {
	p := started(t)
	apply(t, p,
		`{"type":"TEXT_MESSAGE_START","messageId":"a","role":"assistant","metadata":{"keep":1}}`,
		`{"type":"TEXT_MESSAGE_CONTENT","messageId":"a","delta":"hello","metadata":{"trace":"opaque"}}`,
		`{"type":"TEXT_MESSAGE_END","messageId":"a"}`,
		`{"type":"TOOL_CALL_START","toolCallId":"call","toolCallName":"weather","parentMessageId":"a","metadata":{"tool":true}}`,
		`{"type":"TOOL_CALL_ARGS","toolCallId":"call","delta":"{\"city\":\"SF\"}"}`,
		`{"type":"TOOL_CALL_END","toolCallId":"call"}`,
		`{"type":"TEXT_MESSAGE_START","messageId":"b","role":"assistant"}`,
		`{"type":"TEXT_MESSAGE_CONTENT","messageId":"b","delta":"done"}`,
		`{"type":"TEXT_MESSAGE_END","messageId":"b"}`,
		`{"type":"TOOL_CALL_RESULT","toolCallId":"call","messageId":"result","content":[{"type":"text","text":"sunny"}],"metadata":{"source":"tool"}}`,
		`{"type":"REASONING_ENCRYPTED_VALUE","subtype":"tool-call","entityId":"call","encryptedValue":"secret-token"}`,
		`{"type":"RUN_FINISHED","threadId":"t","runId":"r"}`)
	var ids []string
	for _, m := range p.Messages {
		ids = append(ids, field(m, "id"))
	}
	if !reflect.DeepEqual(ids, []string{"a", "result", "b"}) {
		t.Fatal(ids)
	}
	if len(object(p.Messages[0]["metadata"])) != 2 {
		t.Fatal(p.Messages)
	}
	call, err := p.tool("call")
	if err != nil || field(call, "encryptedValue") != "secret-token" {
		t.Fatal(call, err)
	}
	if object(call["metadata"])["tool"] != true {
		t.Fatal(call)
	}
}

func TestStateAllPatchOperationsAndAtomicFailure(t *testing.T) {
	p := started(t)
	apply(t, p, `{"type":"STATE_SNAPSHOT","snapshot":{"a/b":{"~x":[1,2]},"remove":"yes"}}`,
		`{"type":"STATE_DELTA","delta":[{"op":"test","path":"/a~1b/~0x/0","value":1},{"op":"add","path":"/a~1b/~0x/-","value":3},{"op":"replace","path":"/a~1b/~0x/1","value":4},{"op":"copy","from":"/a~1b/~0x/2","path":"/copied"},{"op":"move","from":"/copied","path":"/moved"},{"op":"remove","path":"/remove"}]}`)
	before, _ := json.Marshal(p.State)
	if _, err := p.Apply([]byte(`{"type":"STATE_DELTA","delta":[{"op":"replace","path":"/moved","value":9},{"op":"test","path":"/moved","value":8}]}`)); err == nil {
		t.Fatal("failed patch accepted")
	}
	after, _ := json.Marshal(p.State)
	if string(before) != string(after) {
		t.Fatal("failed patch mutated state")
	}
	if _, err := p.Apply([]byte(`{"type":"STATE_DELTA","delta":[{"op":"remove","path":"/a~1b/~0x/-1"}]}`)); err == nil {
		t.Fatal("nonstandard negative index accepted")
	}
	apply(t, p, `{"type":"STATE_DELTA","delta":[{"op":"replace","path":"","value":[false,0,""]}]}`)
	if got, _ := json.Marshal(p.State); string(got) != `[false,0,""]` {
		t.Fatal(string(got))
	}
}

func TestConcurrentChunkLanesAndActivities(t *testing.T) {
	p := started(t)
	apply(t, p,
		`{"type":"SUBAGENT_STARTED","subagentRunId":"child1","name":"one"}`,
		`{"type":"SUBAGENT_STARTED","subagentRunId":"child2","name":"two"}`,
		`{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","subagentRunId":"child1","delta":"A","metadata":{"keep":true}}`,
		`{"type":"TEXT_MESSAGE_CHUNK","messageId":"b","subagentRunId":"child2","delta":"B"}`)
	if _, err := p.Apply([]byte(`{"type":"TEXT_MESSAGE_CHUNK","delta":"ambiguous"}`)); err == nil {
		t.Fatal("ambiguous lane accepted")
	}
	apply(t, p, `{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","delta":"1","rawEvent":{"opaque":0}}`,
		`{"type":"SUBAGENT_FINISHED","subagentRunId":"child1"}`,
		`{"type":"TEXT_MESSAGE_CHUNK","delta":"2"}`,
		`{"type":"SUBAGENT_ERROR","subagentRunId":"child2","message":"stopped"}`,
		`{"type":"ACTIVITY_SNAPSHOT","messageId":"activity","activityType":"progress","content":{"value":1}}`,
		`{"type":"ACTIVITY_SNAPSHOT","messageId":"activity","activityType":"progress","content":{"value":0},"replace":false}`,
		`{"type":"ACTIVITY_DELTA","messageId":"activity","activityType":"progress","patch":[{"op":"replace","path":"/value","value":2}]}`,
		`{"type":"STEP_STARTED","stepName":"plan"}`,
		`{"type":"STEP_FINISHED","stepName":"plan"}`,
		`{"type":"REASONING_START","messageId":"span"}`,
		`{"type":"REASONING_MESSAGE_CHUNK","messageId":"reasoning","delta":"reason"}`,
		`{"type":"REASONING_END","messageId":"span"}`,
		`{"type":"REASONING_ENCRYPTED_VALUE","subtype":"message","entityId":"reasoning","encryptedValue":"opaque"}`,
		`{"type":"TOOL_CALL_CHUNK","toolCallId":"call","toolCallName":"lookup","delta":"{}"}`,
		`{"type":"CUSTOM","name":"opaque.extension","value":false}`,
		`{"type":"TOOL_CALL_RESULT","toolCallId":"call","messageId":"result","content":"ok"}`,
		`{"type":"RAW","event":null}`,
		`{"type":"RUN_FINISHED","threadId":"t","runId":"r","outcome":{"type":"cancelled"}}`)
	if field(p.message("a"), "content") != "A1" || field(p.message("b"), "content") != "B2" {
		t.Fatal(p.Messages)
	}
	if field(p.message("reasoning"), "encryptedValue") != "opaque" {
		t.Fatal(p.Messages)
	}
}

func TestChunkOpenerChangesAndOutOfRunEventsRejected(t *testing.T) {
	p := started(t)
	apply(t, p, `{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","delta":"a"}`)
	before, _ := json.Marshal(p)
	if _, err := p.Apply([]byte(`{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","role":"user","delta":"b"}`)); err == nil {
		t.Fatal("role change accepted")
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("invalid event mutated projection")
	}
	apply(t, p, `{"type":"RUN_ERROR","message":"failure"}`)
	if _, err := p.Apply([]byte(`{"type":"CUSTOM","name":"late","value":{}}`)); err == nil {
		t.Fatal("post-terminal event accepted")
	}
}
