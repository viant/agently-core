package turnqueue

import (
	"encoding/json"
	read "github.com/viant/agently-core/internal/datly/turnqueue/read"
	"testing"
)

var _ *read.QueueRowView = (*QueueRowView)(nil)

func TestQueueRowAliasScalarNullAndZero(t *testing.T) {
	v := &QueueRowView{Id: "owned", ConversationId: "c", QueueSeq: 0}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 8 || string(wire["queueSeq"]) != "0" || string(wire["updatedAt"]) != "null" {
		t.Fatal("scalar shape/null/zero changed")
	}
}
