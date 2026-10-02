package toolapprovalqueue

import (
	"encoding/json"
	read "github.com/viant/agently-core/internal/datly/toolapprovalqueue/read"
	"testing"
)

var _ *read.ApprovalView = (*OutcomeRowView)(nil)

func TestApprovalOutcomeGeneratedAliasPreservesBinaryAndNull(t *testing.T) {
	v := &OutcomeRowView{Id: "owned", ToolName: "system/exec:execute", Arguments: []byte(`{"marker":"ORANGE-42 café"}`)}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 20 || string(wire["metadata"]) != "null" || string(wire["transitionAt"]) != "null" {
		t.Fatal("public nullable scalar shape changed")
	}
	var out OutcomeRowView
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if string(out.Arguments) != string(v.Arguments) {
		t.Fatal("binary argument bytes changed")
	}
}
