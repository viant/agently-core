package dispatchpayload

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHTTPArgumentsRespectFinalDispatchAndToolScope(t *testing.T) {
	const field = `{ "amount" : 9007199254740993, "text" : "<data>" }`
	var input HTTPArguments
	if err := json.Unmarshal([]byte(`{"definition":`+field+`,"title":"x","page":2}`), &input); err != nil {
		t.Fatal(err)
	}
	def := input.Values["definition"].(map[string]any)
	if def["amount"] != json.Number("9007199254740993") || input.Values["page"] != float64(2) {
		t.Fatal("incorrect dispatch values")
	}
	original := input.Context(context.Background(), "reports/update")
	_, wrong := ClaimHTTPArguments(original, "other/update", input.Values)
	if wrong != nil {
		t.Fatal("wrong tool claimed arguments")
	}
	_, wrong = ClaimHTTPArguments(original, "reports/update", map[string]any{"definition": def})
	if wrong != nil {
		t.Fatal("unrelated invocation claimed arguments")
	}
	ctx, raw := ClaimHTTPArguments(original, "reports/update", input.Values)
	_, nested := ClaimHTTPArguments(ctx, "reports/update", input.Values)
	if raw == nil || nested != nil {
		t.Fatal("incorrect scope")
	}
	wire := raw.Wire(input.Values)
	if string(wire["definition"].(json.RawMessage)) != field {
		t.Fatal("changed opaque field")
	}
	def["amount"] = json.Number("11")
	if changed := raw.Wire(input.Values)["definition"].(map[string]any)["amount"]; changed != json.Number("11") {
		t.Fatal("policy edit was undone")
	}
	delete(input.Values, "definition")
	if _, ok := raw.Wire(input.Values)["definition"]; ok {
		t.Fatal("removed argument was resurrected")
	}
}

func TestHTTPArgumentsRejectAmbiguousOpaqueMembers(t *testing.T) {
	for _, body := range []string{`{"definition":{},"Definition":{}}`, `{"sourceManifest":{},"sourceManifest":null}`} {
		var args HTTPArguments
		if err := json.Unmarshal([]byte(body), &args); err == nil {
			t.Fatal("accepted ambiguous input")
		}
	}
}
