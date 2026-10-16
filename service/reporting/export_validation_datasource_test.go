package reporting

import (
	"encoding/json"
	"testing"
)

func TestResourceDatasourceValidationKeepsLegacyContractAndObjectShape(t *testing.T) {
	opaque := json.RawMessage(`{"kind":42,"limit":"producer-specific","term":"approved"}`)
	if err := validateReportFillRequest(opaque, 0); err == nil {
		t.Fatal("legacy request contract was weakened")
	}
	if err := validateReportFillRequest(opaque, 0, exportValidationOptions{resourceDatasourceRequests: true}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `[]`, `42`, `"token"`, `{`} {
		if err := validateReportFillRequest(json.RawMessage(raw), 0, exportValidationOptions{resourceDatasourceRequests: true}); err == nil {
			t.Fatalf("nonobject request accepted: %s", raw)
		}
	}
}

func TestDatasourceRenderModeCannotBeSetByClientJSON(t *testing.T) {
	var request RenderRequest
	if err := json.Unmarshal([]byte(`{"resourceDatasourceRequests":true,"ResourcePin":{"uri":"report://forged/example"}}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.resourceDatasourceRequests || request.ResourcePin != nil {
		t.Fatal("client JSON enabled host-owned render mode or pin")
	}
}
