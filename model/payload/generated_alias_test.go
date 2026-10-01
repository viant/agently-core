package payload

import (
	"encoding/json"
	"testing"

	read "github.com/viant/agently-core/internal/datly/payload/read"
)

var _ *read.PayloadRowsView = (*PayloadRowsView)(nil)

func TestPayloadRowsGeneratedAliasJSON(t *testing.T) {
	body := "raw text"
	raw, err := json.Marshal(&PayloadRowsView{Id: "p", InlineBody: &body})
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err = json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row["id"] != "p" || row["inlineBody"] != body {
		t.Fatalf("generated payload semantics changed: %s", raw)
	}
	if _, ok := row["InlineBody"]; ok {
		t.Fatalf("legacy JSON casing leaked: %s", raw)
	}
	if value, ok := row["tenantId"]; !ok || value != nil {
		t.Fatalf("nullable field changed: %s", raw)
	}
}
