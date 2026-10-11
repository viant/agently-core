package api

import (
	"encoding/json"
	"testing"
)

func TestFetchDatasourcePreservesFullSelectionNumbers(t *testing.T) {
	raw := []byte(`{"id":"rows","windowParameters":{"ids":[9007199254740993,"opaque"],"parents":{"account":9007199254740995}}}`)
	var input FetchDatasourceInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	ids := input.WindowParameters["ids"].([]any)
	if ids[0].(json.Number).String() != "9007199254740993" || ids[1] != "opaque" {
		t.Fatal("selection changed")
	}
	encoded, _ := json.Marshal(input.WindowParameters)
	if string(encoded) != `{"ids":[9007199254740993,"opaque"],"parents":{"account":9007199254740995}}` {
		t.Fatalf("full selection changed: %s", encoded)
	}
}
