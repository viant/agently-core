package extensions

import (
	"encoding/json"
	"testing"
)

func TestGoalPayloadDefinitions(t *testing.T) {
	for _, operation := range GoalOperations {
		raw := []byte(`{}`)
		if operation == "goal.create" {
			raw = []byte(`{"objective":"complete work"}`)
		}
		if operation == "goal.update" {
			raw = []byte(`{"tokenBudget":0}`)
		}
		if err := ValidateGoalPayload(operation, raw); err != nil {
			t.Fatal(err)
		}
		if err := ValidateGoalPayload(operation, []byte(`{"userId":"forged"}`)); err == nil {
			t.Fatalf("accepted authority field for %s", operation)
		}
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(goalSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema["$defs"]) == 0 {
		t.Fatal("missing extension definitions")
	}
}

func TestGoalEnvelopeAndResultContracts(t *testing.T) {
	for _, raw := range []string{
		`{"version":"1","operation":"goal.get","requestId":"r"}`,
		`{"version":"1","operation":"goal.pause","requestId":"r","payload":{"reason":"user_requested"},"target":{"threadId":"t"}}`,
		`{"version":"1","operation":"goal.create","requestId":"r","payload":{"objective":"work","tokenBudget":0}}`,
	} {
		if err := ValidateGoalEnvelope([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`{"version":"2","operation":"goal.get","requestId":"r"}`,
		`{"version":"1","operation":"goal.create","requestId":"r"}`,
		`{"version":"1","operation":"goal.get","requestId":"r","payload":{"userId":"spoof"}}`,
		`{"version":"1","operation":"goal.get","requestId":"r","target":{"userId":"spoof"}}`,
	} {
		if err := ValidateGoalEnvelope([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if err := ValidateGoalResult([]byte(`{"version":"1","goal":null,"cleared":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGoalCapabilities([]byte(`{"version":"1","operations":["goal.get","goal.pause","goal.resume"]}`)); err != nil {
		t.Fatal(err)
	}
}
