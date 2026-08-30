package toolexec

import (
	"encoding/json"
	"testing"
)

func TestNormalizeBareRedactionMarkersRepairsJSONScalar(t *testing.T) {
	input := `{"score":0.[REDACTED:CARD],"message":"keep [REDACTED:CARD] text"}`
	actual, ok := normalizeBareRedactionMarkers(input)
	if !ok {
		t.Fatal("expected repair")
	}
	if !json.Valid([]byte(actual)) {
		t.Fatalf("repaired result is invalid JSON: %s", actual)
	}
	if actual != `{"score":null,"message":"keep [REDACTED:CARD] text"}` {
		t.Fatalf("unexpected repair: %s", actual)
	}
}

func TestNormalizeBareRedactionMarkersLeavesValidJSONUntouched(t *testing.T) {
	if actual, ok := normalizeBareRedactionMarkers(`{"message":"[REDACTED:CARD]"}`); ok || actual != "" {
		t.Fatalf("valid JSON must remain untouched: %q %v", actual, ok)
	}
}
