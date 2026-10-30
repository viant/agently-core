package reporting

import (
	"encoding/json"
	identity "github.com/viant/agently-core/protocol/resource"
	"strings"
	"testing"
)

func TestExportConsumerDoesNotSupplyDatasourcePins(t *testing.T) {
	request := SubmitExportRequest{DependencyPins: map[string]identity.ResolvedResource{"rows": {URI: "datasource://example/rows", ResourceCandidate: identity.ResourceCandidate{ContentFingerprint: "private-source-hash"}}}, DependencyToken: "private-operation-proof"}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"dependencyPins", "private-source-hash", "private-operation-proof"} {
		if strings.Contains(string(raw), hidden) {
			t.Fatalf("consumer leaked %s", hidden)
		}
	}
}
