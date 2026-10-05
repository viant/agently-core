package agui

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWireEventSchemaRejectsInvalidFields(t *testing.T) {
	for _, raw := range []string{
		`{"type":"TEXT_MESSAGE_CONTENT","messageId":"m"}`,
		`{"type":"RUN_STARTED","threadId":"t","runId":"r","subagentRunId":"s"}`,
		`{"type":"CUSTOM","name":"x"}`,
		`{"type":"STATE_DELTA","delta":[{"op":"remove","path":"bad"}]}`,
		`{"type":"TEXT_MESSAGE_START","messageId":"m","role":"tool"}`,
		`{"type":"TEXT_MESSAGE_START","messageId":"m","timestamp":9007199254740992}`,
		`{"type":"RAW","event":{},"rawEvent":null}`,
		`{"type":"RUN_FINISHED","threadId":"t","runId":"r","outcome":{"type":"interrupt","interrupts":[]}}`,
		`{"type":"BOGUS"}`,
	} {
		if _, err := DecodeEvent([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestWireNullEmptyAndPatchExtrasRoundTrip(t *testing.T) {
	for _, raw := range []string{
		`{"type":"TEXT_MESSAGE_CONTENT","messageId":"","delta":"","timestamp":0}`,
		`{"type":"STATE_SNAPSHOT","snapshot":null}`,
		`{"type":"CUSTOM","name":"","value":null}`,
		`{"type":"RAW","event":null}`,
		`{"type":"STATE_DELTA","delta":[{"op":"remove","path":"","value":null,"vendor":{"n":9007199254740991}}]}`,
		`{"type":"TOOL_CALL_RESULT","messageId":"m","toolCallId":"c","content":[]}`,
		`{"type":"ACTIVITY_SNAPSHOT","messageId":"m","activityType":"x","content":{},"replace":false}`,
	} {
		value, err := DecodeEvent([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		json.Unmarshal([]byte(raw), &a)
		json.Unmarshal(encoded, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("lost data: %s => %s", raw, encoded)
		}
	}
}

func TestWireUnionRejectsAmbiguousConstruction(t *testing.T) {
	_, err := json.Marshal(WireEvent{RawEvent: &WireRawEvent{}, CustomEvent: &WireCustomEvent{}})
	if err == nil {
		t.Fatal("accepted multiple union variants")
	}
}
