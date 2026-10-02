package message

import (
	"encoding/json"
	base "github.com/viant/agently-core/internal/datly/message/base"
	"testing"
)

var _ *base.MessageBaseView = (*MessageRowsView)(nil)

func TestMessagePageGeneratedAliasPreservesNullZeroAndContent(t *testing.T) {
	text := "café 日本語"
	row := &MessageRowsView{Id: "owned", ConversationId: "conversation", Interim: 0, Content: &text}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	wire := map[string]json.RawMessage{}
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 29 || string(wire["interim"]) != "0" || string(wire["updatedAt"]) != "null" {
		t.Fatal("field presence/null/zero changed")
	}
	var decoded MessageRowsView
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Content == nil || *decoded.Content != text {
		t.Fatal("content changed")
	}
}
