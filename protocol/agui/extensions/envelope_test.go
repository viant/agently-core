package extensions

import "testing"

func TestWorkspaceAndRunEnvelopesRequireVersionedRequestIdentity(t *testing.T) {
	cases := []struct {
		name      string
		validate  func([]byte) error
		operation string
		payload   string
	}{
		{"workspace", ValidateWorkspaceEnvelope, "workspace.metadata.get", `{}`},
		{"run", ValidateRunEnvelope, "run.get", `{"runId":"target"}`},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			valid := `{"version":"1","operation":"` + item.operation + `","requestId":"request","target":{"threadId":"thread"},"payload":` + item.payload + `}`
			if err := item.validate([]byte(valid)); err != nil {
				t.Fatal(err)
			}
			invalid := []string{
				`{"version":"1","operation":"` + item.operation + `","payload":` + item.payload + `}`,
				`{"version":"1","operation":"` + item.operation + `","requestId":"","payload":` + item.payload + `}`,
				`{"version":"2","operation":"` + item.operation + `","requestId":"r","payload":` + item.payload + `}`,
				`{"version":"1","operation":"undeclared","requestId":"r","payload":{}}`,
				`{"version":"1","operation":"` + item.operation + `","requestId":"r","target":{"threadId":"t","principal":"foreign"},"payload":` + item.payload + `}`,
				`{"version":"1","operation":"` + item.operation + `","requestId":"r","payload":{"principal":"foreign"}}`,
			}
			for _, raw := range invalid {
				if err := item.validate([]byte(raw)); err == nil {
					t.Fatalf("invalid envelope accepted: %s", raw)
				}
			}
		})
	}
}
