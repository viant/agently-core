package generatedfile

import (
	"encoding/json"
	read "github.com/viant/agently-core/internal/datly/generatedfile/read"
	"testing"
)

var _ *read.GeneratedFileView = (*GeneratedFileView)(nil)

func TestGeneratedFileAliasScalarNullAndZero(t *testing.T) {
	zero := 0
	v := &GeneratedFileView{Id: "owned", ConversationId: "c", SizeBytes: &zero}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 19 || string(wire["sizeBytes"]) != "0" || string(wire["expiresAt"]) != "null" {
		t.Fatal("scalar shape/null/zero changed")
	}
	var out GeneratedFileView
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.SizeBytes == nil || *out.SizeBytes != 0 {
		t.Fatal("explicit zero lost")
	}
}
