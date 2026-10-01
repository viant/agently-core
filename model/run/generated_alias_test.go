package run

import (
	"encoding/json"
	"testing"

	read "github.com/viant/agently-core/internal/datly/runsteps/read"
)

var _ *read.RunStepsView = (*RunStepsView)(nil)

func TestRunStepsGeneratedAliasJSON(t *testing.T) {
	raw, err := json.Marshal(&RunStepsView{StepType: "tool", MessageId: "m", Name: "search", Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err = json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row["stepType"] != "tool" || row["messageId"] != "m" || row["runId"] != nil {
		t.Fatalf("generated row semantics changed: %s", raw)
	}
	if _, ok := row["StepType"]; ok {
		t.Fatalf("legacy JSON casing leaked: %s", raw)
	}
	if _, ok := row["runId"]; !ok {
		t.Fatalf("nullable field omitted: %s", raw)
	}
}
