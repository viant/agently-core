package sdk

import (
	"encoding/json"
	"testing"
)

func TestNormalizeFeedResponseDataRepairsBareRedactionMarker(t *testing.T) {
	input := json.RawMessage(`{"score":0.[REDACTED:CARD],"message":"keep [REDACTED:CARD] text"}`)
	actual := normalizeFeedResponseData(input)
	encoded, err := json.Marshal(map[string]interface{}{"data": actual, "ui": map[string]interface{}{"renderMode": "forge"}})
	if err != nil {
		t.Fatalf("marshal repaired feed response: %v", err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("repaired response is invalid JSON: %s", encoded)
	}
	if string(encoded) != `{"data":{"score":null,"message":"keep [REDACTED:CARD] text"},"ui":{"renderMode":"forge"}}` {
		t.Fatalf("unexpected repaired response: %s", encoded)
	}
}

func TestNormalizeFeedResponseDataDropsUnrepairableRawJSON(t *testing.T) {
	actual := normalizeFeedResponseData(json.RawMessage(`{"broken":`))
	if actual != nil {
		t.Fatalf("expected unrepairable feed data to be dropped, got %#v", actual)
	}
}

func TestNormalizeFeedConfigValueConvertsNestedYAMLMaps(t *testing.T) {
	input := map[string]interface{}{
		"ui": map[interface{}]interface{}{
			"containers": []interface{}{map[interface{}]interface{}{"id": "overview"}},
		},
	}
	encoded, err := json.Marshal(normalizeFeedConfigValue(input))
	if err != nil {
		t.Fatalf("marshal normalized feed config: %v", err)
	}
	if string(encoded) != `{"ui":{"containers":[{"id":"overview"}]}}` {
		t.Fatalf("unexpected normalized config: %s", encoded)
	}
}
