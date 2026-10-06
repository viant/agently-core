package aguistate

import (
	"encoding/json"
	"testing"
)

func semanticProjection(t *testing.T) *Projection {
	t.Helper()
	p, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, p, `{"type":"RUN_STARTED","threadId":"t","runId":"r"}`)
	return p
}
func rejectAtomic(t *testing.T, p *Projection, event string) {
	t.Helper()
	before, _ := json.Marshal(p)
	if _, err := p.Apply(json.RawMessage(event)); err == nil {
		t.Fatalf("accepted %s", event)
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("rejected event mutated projection")
	}
}
func TestReasoningSpansUseMessageIdentity(t *testing.T) {
	p := semanticProjection(t)
	apply(t, p, `{"type":"REASONING_START","messageId":"a","subagentRunId":"s"}`)
	apply(t, p, `{"type":"REASONING_START","messageId":"b","subagentRunId":"s"}`)
	rejectAtomic(t, p, `{"type":"REASONING_END","messageId":"wrong"}`)
	rejectAtomic(t, p, `{"type":"REASONING_END","messageId":"a","subagentRunId":"other"}`)
	apply(t, p, `{"type":"REASONING_END","messageId":"a"}`)
	apply(t, p, `{"type":"REASONING_END","messageId":"b","subagentRunId":"s"}`)
	apply(t, p, `{"type":"RUN_FINISHED","threadId":"t","runId":"r"}`)
}
func TestRunFinishRequiresAllLanesClosedButErrorDoesNot(t *testing.T) {
	for _, event := range []string{
		`{"type":"STEP_STARTED","stepName":"work"}`,
		`{"type":"REASONING_START","messageId":"reason"}`,
		`{"type":"SUBAGENT_STARTED","subagentRunId":"s","name":"child"}`,
		`{"type":"TEXT_MESSAGE_START","messageId":"m","role":"assistant"}`,
	} {
		t.Run(event, func(t *testing.T) {
			p := semanticProjection(t)
			apply(t, p, event)
			rejectAtomic(t, p, `{"type":"RUN_FINISHED","threadId":"t","runId":"r"}`)
			apply(t, p, `{"type":"RUN_ERROR","message":"failure"}`)
			apply(t, p, `{"type":"RUN_STARTED","threadId":"t","runId":"next"}`)
			apply(t, p, `{"type":"RUN_FINISHED","threadId":"t","runId":"next"}`)
		})
	}
	p, _ := New(nil, nil)
	apply(t, p, `{"type":"RUN_ERROR","message":"initial failure"}`)
}
func TestActivityPatchRetainsObjectAtomically(t *testing.T) {
	p := semanticProjection(t)
	apply(t, p, `{"type":"ACTIVITY_SNAPSHOT","messageId":"a","activityType":"status","content":{"value":1}}`)
	for _, value := range []string{`null`, `[]`, `"scalar"`, `42`} {
		rejectAtomic(t, p, `{"type":"ACTIVITY_DELTA","messageId":"a","activityType":"status","patch":[{"op":"replace","path":"","value":`+value+`}]}`)
	}
	apply(t, p, `{"type":"ACTIVITY_DELTA","messageId":"a","activityType":"status","patch":[{"op":"replace","path":"","value":{"next":true}}]}`)
}
func TestSubagentAttributionAndLifecycleIdentity(t *testing.T) {
	p := semanticProjection(t)
	apply(t, p, `{"type":"STEP_STARTED","stepName":"work"}`)
	apply(t, p, `{"type":"STEP_STARTED","stepName":"work","subagentRunId":""}`)
	apply(t, p, `{"type":"STEP_FINISHED","stepName":"work"}`)
	rejectAtomic(t, p, `{"type":"RUN_FINISHED","threadId":"t","runId":"r"}`)
	apply(t, p, `{"type":"STEP_FINISHED","stepName":"work","subagentRunId":""}`)
	apply(t, p, `{"type":"SUBAGENT_STARTED","subagentRunId":"s","name":"child"}`)
	apply(t, p, `{"type":"SUBAGENT_FINISHED","subagentRunId":"s"}`)
	rejectAtomic(t, p, `{"type":"SUBAGENT_STARTED","subagentRunId":"s","name":"child"}`)
	apply(t, p, `{"type":"RUN_FINISHED","threadId":"t","runId":"r"}`)
}
