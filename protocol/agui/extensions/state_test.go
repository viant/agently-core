package extensions

import "testing"

func TestStateExtensionPayloadAndResult(t *testing.T) {
	for _, test := range []struct{ op, raw string }{
		{"state.get", `{}`}, {"state.patch", `{"patch":[]}`}, {"state.patch", `{"patch":[{"op":"add","path":"","value":null}],"ifMatch":"opaque"}`},
	} {
		if err := ValidateStatePayload(test.op, []byte(test.raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct{ op, raw string }{
		{"state.get", `{"userId":"spoof"}`}, {"state.patch", `{"patch":null}`}, {"state.patch", `{"patch":[],"ifMatch":null}`}, {"state.patch", `{"patch":[{"op":"bogus","path":""}]}`}, {"state.patch", `{"patch":[{"op":"add","path":"bad","value":1}]}`},
	} {
		if err := ValidateStatePayload(test.op, []byte(test.raw)); err == nil {
			t.Fatalf("accepted %s", test.raw)
		}
	}
	if err := ValidateStateResult([]byte(`{"version":"1","hash":"opaque","state":null}`)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStateEnvelope([]byte(`{"version":"1","operation":"state.patch","requestId":"r","payload":{"patch":[]}}`)); err != nil {
		t.Fatal(err)
	}
}
